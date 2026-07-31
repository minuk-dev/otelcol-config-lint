package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minuk-dev/otel-collector-config-linter/internal/cli"
)

const (
	validConfig = "../../testdata/valid"
	badConfig   = "../../testdata/invalid/typos.yaml"
)

// run executes the command and returns its exit code and streams.
func run(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := cli.Run(args, strings.NewReader(stdin), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestValidDirectoryPasses(t *testing.T) {
	code, out, errOut := run(t, "", "-min-severity", "error", validConfig)
	if code != 0 {
		t.Fatalf("exit %d, stdout=%q stderr=%q", code, out, errOut)
	}
	if out != "" {
		t.Errorf("a clean run should print nothing, got %q", out)
	}
}

func TestInvalidFileFails(t *testing.T) {
	code, out, _ := run(t, "", badConfig)
	if code != 1 {
		t.Fatalf("want exit 1, got %d", code)
	}
	for _, want := range []string{"unknown-top-level-key", "invalid-value", "undefined-reference"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}

func TestStdin(t *testing.T) {
	code, out, _ := run(t, "receivers:\n  otlp:\n", "-")
	if code != 1 {
		t.Fatalf("want exit 1, got %d: %s", code, out)
	}
	if !strings.Contains(out, "stdin:") {
		t.Errorf("stdin findings should be reported against \"stdin\":\n%s", out)
	}
}

func TestJSONOutput(t *testing.T) {
	code, out, _ := run(t, "", "-output", "json", badConfig)
	if code != 1 {
		t.Fatalf("want exit 1, got %d", code)
	}
	var report struct {
		Files []struct {
			Filename    string `json:"filename"`
			Status      string `json:"status"`
			Diagnostics []struct {
				Rule     string `json:"rule"`
				Severity string `json:"severity"`
				Position struct {
					Line int `json:"line"`
				} `json:"position"`
			} `json:"diagnostics"`
		} `json:"files"`
		Summary struct {
			Invalid int `json:"invalid"`
		} `json:"summary"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if len(report.Files) != 1 || report.Summary.Invalid != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if len(report.Files[0].Diagnostics) == 0 || report.Files[0].Diagnostics[0].Position.Line == 0 {
		t.Errorf("diagnostics should carry positions: %+v", report.Files[0])
	}
}

func TestGitHubOutput(t *testing.T) {
	_, out, _ := run(t, "", "-output", "github", badConfig)
	if !strings.HasPrefix(out, "::error file=") {
		t.Errorf("want workflow commands, got:\n%s", out)
	}
	if strings.Contains(out, "\nhint:") {
		t.Error("newlines inside an annotation must be escaped")
	}
}

func TestJUnitAndTAPOutput(t *testing.T) {
	_, junit, _ := run(t, "", "-output", "junit", badConfig)
	if !strings.Contains(junit, "<testsuite") || !strings.Contains(junit, "<failure") {
		t.Errorf("unexpected junit output:\n%s", junit)
	}
	_, tap, _ := run(t, "", "-output", "tap", badConfig)
	if !strings.HasPrefix(tap, "1..1\nnot ok 1 - ") {
		t.Errorf("unexpected tap output:\n%s", tap)
	}
}

func TestFailOnWarningTightensTheGate(t *testing.T) {
	src := "receivers:\n  otlp:\n    protocols:\n      grpc:\nexporters:\n  debug:\n" +
		"processors:\n  batch:\nservice:\n  pipelines:\n    traces:\n" +
		"      receivers: [otlp]\n      processors: [batch]\n      exporters: [debug]\n" +
		"extensions:\n  zpages:\n"
	// The unused zpages extension is a warning, so the gate decides the outcome.
	if code, _, _ := run(t, src, "-"); code != 0 {
		t.Errorf("warnings alone should not fail by default, got exit %d", code)
	}
	if code, _, _ := run(t, src, "-fail-on", "warning", "-"); code != 1 {
		t.Errorf("-fail-on warning should fail, got exit %d", code)
	}
}

func TestDisableAndSeverityOverrides(t *testing.T) {
	code, out, _ := run(t, "", "-disable", "unknown-top-level-key,invalid-value,undefined-reference,unknown-component", badConfig)
	if strings.Contains(out, "[invalid-value]") {
		t.Errorf("disabled rules must not report:\n%s", out)
	}
	if code != 0 {
		t.Logf("remaining findings:\n%s", out)
	}

	_, out, _ = run(t, "", "-severity", "missing-batch=warning", "-min-severity", "warning", validConfig)
	if strings.Contains(out, "[missing-batch]") {
		t.Errorf("the valid config should not be missing batch:\n%s", out)
	}

	if code, _, errOut := run(t, "", "-disable", "no-such-rule", badConfig); code != 2 ||
		!strings.Contains(errOut, "unknown rule") {
		t.Errorf("an unknown rule should be a usage error, got %d: %s", code, errOut)
	}
}

func TestCollectorVersionSelectsTheCatalog(t *testing.T) {
	// The logging exporter was removed upstream, so it is valid in v0.110.0
	// and unknown in the latest release.
	src := "receivers:\n  otlp:\n    protocols:\n      grpc:\nexporters:\n  logging:\n" +
		"service:\n  pipelines:\n    traces:\n      receivers: [otlp]\n      exporters: [logging]\n"

	if code, out, _ := run(t, src, "-collector-version", "v0.110.0", "-min-severity", "error", "-"); code != 0 {
		t.Errorf("logging should exist in v0.110.0, got exit %d:\n%s", code, out)
	}
	code, out, _ := run(t, src, "-collector-version", "v0.157.0", "-")
	if code != 1 || !strings.Contains(out, "unknown-component") {
		t.Errorf("logging should be unknown in v0.157.0, got exit %d:\n%s", code, out)
	}
}

func TestUnknownVersionFallsBackToTheNearestOlder(t *testing.T) {
	code, _, errOut := run(t, "", "-collector-version", "v0.155.0", "-min-severity", "error", validConfig)
	if code != 0 {
		t.Fatalf("want a fallback, got exit %d: %s", code, errOut)
	}
	if !strings.Contains(errOut, "falling back to") {
		t.Errorf("the fallback should be announced: %q", errOut)
	}
}

func TestCatalogLocationOverridesTheBuiltins(t *testing.T) {
	dir := t.TempDir()
	catalogJSON := `{"collectorVersion":"v9.9.9","components":{"receiver":{"custom":{"type":"custom","signals":["logs"]}},` +
		`"exporter":{"custom":{"type":"custom","signals":["logs"]}}}}`
	if err := os.WriteFile(filepath.Join(dir, "v9.9.9.json"), []byte(catalogJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	src := "receivers:\n  custom:\nexporters:\n  custom:\n" +
		"service:\n  pipelines:\n    logs:\n      receivers: [custom]\n      exporters: [custom]\n"

	code, out, errOut := run(t, src, "-catalog-location", dir, "-collector-version", "v9.9.9", "-min-severity", "error", "-")
	if code != 0 {
		t.Errorf("a project catalog should be honoured, got exit %d:\n%s%s", code, out, errOut)
	}
}

func TestIgnoreMissingSchemas(t *testing.T) {
	src := "receivers:\n  mycorp_custom:\nexporters:\n  debug:\n" +
		"service:\n  pipelines:\n    logs:\n      receivers: [mycorp_custom]\n      exporters: [debug]\n"
	if code, _, _ := run(t, src, "-"); code != 1 {
		t.Error("an unknown component should fail by default")
	}
	if code, out, _ := run(t, src, "-ignore-missing-schemas", "-"); code != 0 {
		t.Errorf("-ignore-missing-schemas should tolerate it, got exit %d:\n%s", code, out)
	}
}

func TestStrictPromotesUnknownFields(t *testing.T) {
	src := "receivers:\n  otlp:\n    protocols:\n      grpc:\nexporters:\n  debug:\n    verbosty: normal\n" +
		"service:\n  pipelines:\n    traces:\n      receivers: [otlp]\n      exporters: [debug]\n"
	if code, _, _ := run(t, src, "-"); code != 0 {
		t.Error("an unknown field is only a warning by default")
	}
	if code, _, _ := run(t, src, "-strict", "-"); code != 1 {
		t.Error("-strict should make an unknown field fail")
	}
}

func TestSettingsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.yaml")
	if err := os.WriteFile(path, []byte("collectorVersion: v0.110.0\nminSeverity: error\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	src := "receivers:\n  otlp:\n    protocols:\n      grpc:\nexporters:\n  logging:\n" +
		"service:\n  pipelines:\n    traces:\n      receivers: [otlp]\n      exporters: [logging]\n"

	if code, out, _ := run(t, src, "-config", path, "-"); code != 0 {
		t.Errorf("the settings file should select v0.110.0, got exit %d:\n%s", code, out)
	}
	if code, _, errOut := run(t, "", "-config", filepath.Join(dir, "missing.yaml"), validConfig); code != 2 ||
		!strings.Contains(errOut, "missing.yaml") {
		t.Errorf("an explicit settings file must exist, got %d: %s", code, errOut)
	}
}

func TestExcludeSkipsFilesInADirectoryWalk(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.yaml"), []byte("receivers:\n  otlp:\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := run(t, "", dir); code != 1 {
		t.Error("the broken file should be linted without -exclude")
	}
	if code, _, errOut := run(t, "", "-exclude", "broken.yaml", dir); code != 2 ||
		!strings.Contains(errOut, "no YAML files") {
		t.Errorf("-exclude should have skipped everything, got %d: %s", code, errOut)
	}
}

func TestListRulesAndVersions(t *testing.T) {
	code, out, _ := run(t, "", "-list-rules")
	if code != 0 || !strings.Contains(out, "unknown-component") {
		t.Errorf("-list-rules output looks wrong (exit %d):\n%s", code, out)
	}
	code, out, _ = run(t, "", "-list-versions")
	if code != 0 || !strings.Contains(out, "(latest)") || !strings.Contains(out, "components") {
		t.Errorf("-list-versions output looks wrong (exit %d):\n%s", code, out)
	}
}

func TestNoArgumentsPrintsUsage(t *testing.T) {
	code, _, errOut := run(t, "")
	if code != 2 || !strings.Contains(errOut, "Usage:") {
		t.Errorf("want usage on exit 2, got %d: %s", code, errOut)
	}
}

func TestSummaryCountsFiles(t *testing.T) {
	_, out, _ := run(t, "", "-summary", "-min-severity", "error", validConfig, badConfig)
	if !strings.Contains(out, "2 file(s) checked, 1 valid, 1 invalid") {
		t.Errorf("unexpected summary:\n%s", out)
	}
}
