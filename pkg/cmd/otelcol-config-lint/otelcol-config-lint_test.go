package otelcolconfiglint_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	otelcolconfiglint "github.com/minuk-dev/otelcol-config-lint/pkg/cmd/otelcol-config-lint"
)

const (
	validConfig   = "../../../testdata/valid"
	badConfig     = "../../../testdata/invalid/typos.yaml"
	invalidConfig = "../../../testdata/invalid"
)

// run executes the command and returns its exit code and streams. The wiring
// mirrors cmd/otelcol-config-lint/main.go, so what the tests assert on is what
// the binary does.
func run(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()

	var stdout, stderr bytes.Buffer

	cmd := otelcolconfiglint.NewCommand(nil)
	cmd.SetArgs(args)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	err := cmd.Execute()
	if err != nil && !errors.Is(err, otelcolconfiglint.ErrFilesInvalid) {
		cmd.PrintErrf("otelcol-config-lint: %v\n", err)
	}

	return otelcolconfiglint.ExitCode(err), stdout.String(), stderr.String()
}

func TestExitCode(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		in   error
		want int
	}{
		"a clean run": {in: nil, want: otelcolconfiglint.ExitOK},
		"findings":    {in: otelcolconfiglint.ErrFilesInvalid, want: otelcolconfiglint.ExitInvalid},
		"wrapped findings": {
			in:   fmt.Errorf("lint: %w", otelcolconfiglint.ErrFilesInvalid),
			want: otelcolconfiglint.ExitInvalid,
		},
		"a command failure": {in: otelcolconfiglint.ErrNoInput, want: otelcolconfiglint.ExitUsage},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := otelcolconfiglint.ExitCode(tt.in); got != tt.want {
				t.Errorf("ExitCode(%v) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestValidDirectoryPasses(t *testing.T) {
	t.Parallel()

	code, out, errOut := run(t, "", "--min-severity", "error", validConfig)
	if code != 0 {
		t.Fatalf("exit %d, stdout=%q stderr=%q", code, out, errOut)
	}

	if out != "" {
		t.Errorf("a clean run should print nothing, got %q", out)
	}
}

func TestInvalidFileFails(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

	code, out, _ := run(t, "receivers:\n  otlp:\n", "-")
	if code != 1 {
		t.Fatalf("want exit 1, got %d: %s", code, out)
	}

	if !strings.Contains(out, "stdin:") {
		t.Errorf("stdin findings should be reported against \"stdin\":\n%s", out)
	}
}

func TestJSONOutput(t *testing.T) {
	t.Parallel()

	code, out, _ := run(t, "", "--output", "json", badConfig)
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

	err := json.Unmarshal([]byte(out), &report)
	if err != nil {
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
	t.Parallel()

	_, out, _ := run(t, "", "--output", "github", badConfig)
	if !strings.HasPrefix(out, "::error file=") {
		t.Errorf("want workflow commands, got:\n%s", out)
	}

	if strings.Contains(out, "\nhint:") {
		t.Error("newlines inside an annotation must be escaped")
	}
}

func TestJUnitAndTAPOutput(t *testing.T) {
	t.Parallel()

	_, junit, _ := run(t, "", "--output", "junit", badConfig)
	if !strings.Contains(junit, "<testsuite") || !strings.Contains(junit, "<failure") {
		t.Errorf("unexpected junit output:\n%s", junit)
	}

	_, tap, _ := run(t, "", "--output", "tap", badConfig)
	if !strings.HasPrefix(tap, "1..1\nnot ok 1 - ") {
		t.Errorf("unexpected tap output:\n%s", tap)
	}
}

func TestFailOnWarningTightensTheGate(t *testing.T) {
	t.Parallel()

	src := "receivers:\n  otlp:\n    protocols:\n      grpc:\nexporters:\n  debug:\n" +
		"processors:\n  batch:\nservice:\n  pipelines:\n    traces:\n" +
		"      receivers: [otlp]\n      processors: [batch]\n      exporters: [debug]\n" +
		"extensions:\n  zpages:\n"
	// The unused zpages extension is a warning, so the gate decides the outcome.
	if code, _, _ := run(t, src, "-"); code != 0 {
		t.Errorf("warnings alone should not fail by default, got exit %d", code)
	}

	if code, _, _ := run(t, src, "--fail-on", "warning", "-"); code != 1 {
		t.Errorf("--fail-on warning should fail, got exit %d", code)
	}
}

func TestDisableAndSeverityOverrides(t *testing.T) {
	t.Parallel()

	disabled := "unknown-top-level-key,invalid-value,undefined-reference,unknown-component"

	code, out, _ := run(t, "", "--disable", disabled, badConfig)
	if strings.Contains(out, "[invalid-value]") {
		t.Errorf("disabled rules must not report:\n%s", out)
	}

	if code != 0 {
		t.Logf("remaining findings:\n%s", out)
	}

	_, out, _ = run(t, "", "--severity", "missing-batch=warning", "--min-severity", "warning", validConfig)
	if strings.Contains(out, "[missing-batch]") {
		t.Errorf("the valid config should not be missing batch:\n%s", out)
	}

	if code, _, errOut := run(t, "", "--disable", "no-such-rule", badConfig); code != 2 ||
		!strings.Contains(errOut, "unknown rule") {
		t.Errorf("an unknown rule should be a usage error, got %d: %s", code, errOut)
	}
}

func TestCollectorVersionSelectsTheCatalog(t *testing.T) {
	t.Parallel()

	// The logging exporter was removed upstream, so it is valid in v0.110.0
	// and unknown in the latest release.
	src := "receivers:\n  otlp:\n    protocols:\n      grpc:\nexporters:\n  logging:\n" +
		"service:\n  pipelines:\n    traces:\n      receivers: [otlp]\n      exporters: [logging]\n"

	if code, out, _ := run(t, src, "--collector-version", "v0.110.0", "--min-severity", "error", "-"); code != 0 {
		t.Errorf("logging should exist in v0.110.0, got exit %d:\n%s", code, out)
	}

	code, out, _ := run(t, src, "--collector-version", "v0.157.0", "-")
	if code != 1 || !strings.Contains(out, "unknown-component") {
		t.Errorf("logging should be unknown in v0.157.0, got exit %d:\n%s", code, out)
	}
}

func TestUnknownVersionFallsBackToTheNearestOlder(t *testing.T) {
	t.Parallel()

	code, _, errOut := run(t, "", "--collector-version", "v0.155.0", "--min-severity", "error", validConfig)
	if code != 0 {
		t.Fatalf("want a fallback, got exit %d: %s", code, errOut)
	}

	if !strings.Contains(errOut, "falling back to") {
		t.Errorf("the fallback should be announced: %q", errOut)
	}
}

func TestCatalogLocationOverridesTheBuiltins(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	const component = `{"custom":{"type":"custom","signals":["logs"]}}`

	catalogJSON := `{"collectorVersion":"v9.9.9","components":` +
		`{"receiver":` + component + `,"exporter":` + component + `}}`

	err := os.WriteFile(filepath.Join(dir, "v9.9.9.json"), []byte(catalogJSON), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	src := "receivers:\n  custom:\nexporters:\n  custom:\n" +
		"service:\n  pipelines:\n    logs:\n      receivers: [custom]\n      exporters: [custom]\n"

	code, out, errOut := run(t, src,
		"--catalog-location", dir, "--collector-version", "v9.9.9", "--min-severity", "error", "-")
	if code != 0 {
		t.Errorf("a project catalog should be honoured, got exit %d:\n%s%s", code, out, errOut)
	}
}

func TestIgnoreMissingSchemas(t *testing.T) {
	t.Parallel()

	src := "receivers:\n  mycorp_custom:\nexporters:\n  debug:\n" +
		"service:\n  pipelines:\n    logs:\n      receivers: [mycorp_custom]\n      exporters: [debug]\n"
	if code, _, _ := run(t, src, "-"); code != 1 {
		t.Error("an unknown component should fail by default")
	}

	if code, out, _ := run(t, src, "--ignore-missing-schemas", "-"); code != 0 {
		t.Errorf("--ignore-missing-schemas should tolerate it, got exit %d:\n%s", code, out)
	}
}

func TestStrictPromotesUnknownFields(t *testing.T) {
	t.Parallel()

	src := "receivers:\n  otlp:\n    protocols:\n      grpc:\nexporters:\n  debug:\n    verbosty: normal\n" +
		"service:\n  pipelines:\n    traces:\n      receivers: [otlp]\n      exporters: [debug]\n"
	if code, _, _ := run(t, src, "-"); code != 0 {
		t.Error("an unknown field is only a warning by default")
	}

	if code, _, _ := run(t, src, "--strict", "-"); code != 1 {
		t.Error("--strict should make an unknown field fail")
	}
}

func TestSettingsFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	path := filepath.Join(dir, "settings.yaml")

	err := os.WriteFile(path, []byte("collectorVersion: v0.110.0\nminSeverity: error\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	src := "receivers:\n  otlp:\n    protocols:\n      grpc:\nexporters:\n  logging:\n" +
		"service:\n  pipelines:\n    traces:\n      receivers: [otlp]\n      exporters: [logging]\n"

	if code, out, _ := run(t, src, "--config", path, "-"); code != 0 {
		t.Errorf("the settings file should select v0.110.0, got exit %d:\n%s", code, out)
	}

	if code, _, errOut := run(t, "", "--config", filepath.Join(dir, "missing.yaml"), validConfig); code != 2 ||
		!strings.Contains(errOut, "missing.yaml") {
		t.Errorf("an explicit settings file must exist, got %d: %s", code, errOut)
	}
}

// TestFlagsWinOverTheSettingsFile pins the precedence rule: the file states the
// project policy, an explicit flag overrides it for a single run.
func TestFlagsWinOverTheSettingsFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	path := filepath.Join(dir, "settings.yaml")

	err := os.WriteFile(path, []byte("collectorVersion: v0.110.0\nminSeverity: error\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	// logging exists in v0.110.0 but not in v0.157.0, so the version that
	// actually took effect is visible in the result.
	src := "receivers:\n  otlp:\n    protocols:\n      grpc:\nexporters:\n  logging:\n" +
		"service:\n  pipelines:\n    traces:\n      receivers: [otlp]\n      exporters: [logging]\n"

	code, out, _ := run(t, src, "--config", path, "--collector-version", "v0.157.0", "-")
	if code != 1 || !strings.Contains(out, "unknown-component") {
		t.Errorf("--collector-version should beat the settings file, got exit %d:\n%s", code, out)
	}
}

// TestAnEmptySettingsFileKeepsTheDefaults guards against a merge that treats an
// absent field as an explicit empty value.
func TestAnEmptySettingsFileKeepsTheDefaults(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	path := filepath.Join(dir, "settings.yaml")

	err := os.WriteFile(path, []byte("strict: false\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	code, out, errOut := run(t, "", "--config", path, "--min-severity", "error", validConfig)
	if code != 0 {
		t.Fatalf("exit %d, stdout=%q stderr=%q", code, out, errOut)
	}

	if out != "" {
		t.Errorf("the default text output should stay in force, got %q", out)
	}
}

func TestExcludeSkipsFilesInADirectoryWalk(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, "broken.yaml"), []byte("receivers:\n  otlp:\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	if code, _, _ := run(t, "", dir); code != 1 {
		t.Error("the broken file should be linted without --exclude")
	}

	if code, _, errOut := run(t, "", "--exclude", "broken.yaml", dir); code != 2 ||
		!strings.Contains(errOut, "no YAML files") {
		t.Errorf("--exclude should have skipped everything, got %d: %s", code, errOut)
	}
}

// TestDirectoryWalkFindsEveryFile pins that a directory expands to the config
// files inside it, not to the directory itself.
func TestDirectoryWalkFindsEveryFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	good := "receivers:\n  otlp:\n    protocols:\n      grpc:\nexporters:\n  debug:\n" +
		"service:\n  pipelines:\n    traces:\n      receivers: [otlp]\n      exporters: [debug]\n"

	for _, name := range []string{"a.yaml", "b.yml"} {
		err := os.WriteFile(filepath.Join(dir, name), []byte(good), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	// A file the walk must ignore.
	err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not yaml"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	code, out, errOut := run(t, "", "--summary", "--min-severity", "error", dir)
	if code != 0 {
		t.Fatalf("exit %d, stdout=%q stderr=%q", code, out, errOut)
	}

	if !strings.Contains(out, "2 file(s) checked") {
		t.Errorf("both files in the directory should have been checked:\n%s", out)
	}
}

func TestListRulesAndVersions(t *testing.T) {
	t.Parallel()

	code, out, _ := run(t, "", "--list-rules")
	if code != 0 || !strings.Contains(out, "unknown-component") {
		t.Errorf("--list-rules output looks wrong (exit %d):\n%s", code, out)
	}

	code, out, _ = run(t, "", "--list-versions")
	if code != 0 || !strings.Contains(out, "(latest)") || !strings.Contains(out, "components") {
		t.Errorf("--list-versions output looks wrong (exit %d):\n%s", code, out)
	}
}

// TestVersion pins that the subcommand and the built-in flag agree, so neither
// can drift into printing something different.
func TestVersion(t *testing.T) {
	t.Parallel()

	code, out, errOut := run(t, "", "version")
	if code != 0 || !strings.HasPrefix(out, "otelcol-config-lint ") {
		t.Errorf("version output looks wrong (exit %d): %q %q", code, out, errOut)
	}

	code, flagOut, _ := run(t, "", "--version")
	if code != 0 || flagOut != out {
		t.Errorf("--version should match the subcommand (exit %d): %q vs %q", code, flagOut, out)
	}
}

// TestVersionTakesNoArguments keeps the subcommand from silently swallowing a
// path the user meant to lint.
func TestVersionTakesNoArguments(t *testing.T) {
	t.Parallel()

	code, _, errOut := run(t, "", "version", validConfig)
	if code != 2 || !strings.Contains(errOut, validConfig) {
		t.Errorf("want the stray argument reported on exit 2, got %d: %s", code, errOut)
	}
}

func TestNoArgumentsPrintsUsage(t *testing.T) {
	t.Parallel()

	code, _, errOut := run(t, "")
	if code != 2 || !strings.Contains(errOut, "Usage:") {
		t.Errorf("want usage on exit 2, got %d: %s", code, errOut)
	}
}

func TestAnUnknownFlagIsAUsageError(t *testing.T) {
	t.Parallel()

	code, _, errOut := run(t, "", "--no-such-flag", validConfig)
	if code != 2 || !strings.Contains(errOut, "Usage:") {
		t.Errorf("want usage on exit 2, got %d: %s", code, errOut)
	}
}

// TestFindingsDoNotPrintUsage keeps the common case readable: a config with
// findings is not a misuse of the command.
func TestFindingsDoNotPrintUsage(t *testing.T) {
	t.Parallel()

	code, _, errOut := run(t, "", badConfig)
	if code != 1 {
		t.Fatalf("want exit 1, got %d", code)
	}

	if strings.Contains(errOut, "Usage:") {
		t.Errorf("findings should not print the usage text:\n%s", errOut)
	}
}

func TestSummaryCountsFiles(t *testing.T) {
	t.Parallel()

	_, out, _ := run(t, "", "--summary", "--min-severity", "error", validConfig, badConfig)
	if !strings.Contains(out, "2 file(s) checked, 1 valid, 1 invalid") {
		t.Errorf("unexpected summary:\n%s", out)
	}
}

// TestAFileNamedTwiceIsCheckedOnce pins that naming a file directly and also
// walking into the directory holding it does not check it twice.
func TestAFileNamedTwiceIsCheckedOnce(t *testing.T) {
	t.Parallel()

	_, out, _ := run(t, "", "--summary", "--min-severity", "error",
		validConfig, filepath.Join(validConfig, "agent.yaml"))
	if !strings.Contains(out, "1 file(s) checked") {
		t.Errorf("the file should have been de-duplicated:\n%s", out)
	}
}

// TestReportOrderDoesNotDependOnArgumentOrder pins that results come out in
// path order, so the same set of files reads the same however it was named.
func TestReportOrderDoesNotDependOnArgumentOrder(t *testing.T) {
	t.Parallel()

	_, forwards, _ := run(t, "", "--output", "tap", validConfig, invalidConfig)
	_, backwards, _ := run(t, "", "--output", "tap", invalidConfig, validConfig)

	if forwards != backwards {
		t.Errorf("argument order changed the report:\n%s\nvs\n%s", forwards, backwards)
	}

	// The paths themselves must be sorted, not merely stable.
	var got []string

	for line := range strings.SplitSeq(forwards, "\n") {
		_, path, found := strings.Cut(line, " - ")
		if found {
			got = append(got, path)
		}
	}

	if !slices.IsSorted(got) {
		t.Errorf("results are not in path order: %v", got)
	}
}
