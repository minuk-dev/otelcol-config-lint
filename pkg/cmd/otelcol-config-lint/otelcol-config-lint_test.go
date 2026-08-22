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

	"github.com/samber/lo"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	otelcolconfiglint "github.com/minuk-dev/otelcol-config-lint/pkg/cmd/otelcol-config-lint"
)

// repoSchemas is the committed schema fixture. The binary reads the published
// registry over HTTP, so every test that needs schemas injects this instead --
// one release, enough to exercise every path, and no network.
const repoSchemas = "../../../testdata/schemas"

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

// lint invokes the "run" subcommand, which is where every lint flag lives. The
// repository's schemas are injected so no test reaches the network; a test that
// passes its own --schema-location still wins, since locations are searched in
// the order given.
func lint(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()

	return run(t, stdin, append([]string{"run", "--schema-location", repoSchemas}, args...)...)
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

	code, out, errOut := lint(t, "", "--min-severity", "error", validConfig)
	if code != 0 {
		t.Fatalf("exit %d, stdout=%q stderr=%q", code, out, errOut)
	}

	if out != "" {
		t.Errorf("a clean run should print nothing, got %q", out)
	}
}

func TestInvalidFileFails(t *testing.T) {
	t.Parallel()

	code, out, _ := lint(t, "", badConfig)
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

	code, out, _ := lint(t, "receivers:\n  otlp:\n", "-")
	if code != 1 {
		t.Fatalf("want exit 1, got %d: %s", code, out)
	}

	if !strings.Contains(out, "stdin:") {
		t.Errorf("stdin findings should be reported against \"stdin\":\n%s", out)
	}
}

func TestJSONOutput(t *testing.T) {
	t.Parallel()

	code, out, _ := lint(t, "", "--output", "json", badConfig)
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

	_, out, _ := lint(t, "", "--output", "github", badConfig)
	if !strings.HasPrefix(out, "::error file=") {
		t.Errorf("want workflow commands, got:\n%s", out)
	}

	if strings.Contains(out, "\nhint:") {
		t.Error("newlines inside an annotation must be escaped")
	}
}

func TestJUnitAndTAPOutput(t *testing.T) {
	t.Parallel()

	_, junit, _ := lint(t, "", "--output", "junit", badConfig)
	if !strings.Contains(junit, "<testsuite") || !strings.Contains(junit, "<failure") {
		t.Errorf("unexpected junit output:\n%s", junit)
	}

	_, tap, _ := lint(t, "", "--output", "tap", badConfig)
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
	if code, _, _ := lint(t, src, "-"); code != 0 {
		t.Errorf("warnings alone should not fail by default, got exit %d", code)
	}

	if code, _, _ := lint(t, src, "--fail-on", "warning", "-"); code != 1 {
		t.Errorf("--fail-on warning should fail, got exit %d", code)
	}
}

func TestDisableAndSeverityOverrides(t *testing.T) {
	t.Parallel()

	disabled := "unknown-top-level-key,invalid-value,undefined-reference,unknown-component"

	code, out, _ := lint(t, "", "--disable", disabled, badConfig)
	if strings.Contains(out, "[invalid-value]") {
		t.Errorf("disabled rules must not report:\n%s", out)
	}

	if code != 0 {
		t.Logf("remaining findings:\n%s", out)
	}

	_, out, _ = lint(t, "", "--severity", "missing-batch=warning", "--min-severity", "warning", validConfig)
	if strings.Contains(out, "[missing-batch]") {
		t.Errorf("the valid config should not be missing batch:\n%s", out)
	}

	if code, _, errOut := lint(t, "", "--disable", "no-such-rule", badConfig); code != 2 ||
		!strings.Contains(errOut, "unknown rule") {
		t.Errorf("an unknown rule should be a usage error, got %d: %s", code, errOut)
	}
}

func TestCollectorVersionSelectsTheSchema(t *testing.T) {
	t.Parallel()

	// The logging exporter was removed upstream, so it is valid in v0.110.0
	// and unknown in the latest release.
	src := "receivers:\n  otlp:\n    protocols:\n      grpc:\nexporters:\n  logging:\n" +
		"service:\n  pipelines:\n    traces:\n      receivers: [otlp]\n      exporters: [logging]\n"

	if code, out, _ := lint(t, src, "--collector-version", "v0.110.0", "--min-severity", "error", "-"); code != 0 {
		t.Errorf("logging should exist in v0.110.0, got exit %d:\n%s", code, out)
	}

	code, out, _ := lint(t, src, "--collector-version", "v0.157.0", "-")
	if code != 1 || !strings.Contains(out, "unknown-component") {
		t.Errorf("logging should be unknown in v0.157.0, got exit %d:\n%s", code, out)
	}
}

// TestDistributionSelectsTheBinary is the bug #8 describes: filelog ships in
// contrib but not in core, so a config using it starts fine on otelcol-contrib
// and fails on plain otelcol with `unknown type: "filelog"`.
func TestDistributionSelectsTheBinary(t *testing.T) {
	t.Parallel()

	src := "receivers:\n  filelog:\n    include: [/var/log/app.log]\nexporters:\n  debug:\n" +
		"service:\n  pipelines:\n    logs:\n      receivers: [filelog]\n      exporters: [debug]\n"

	if code, out, _ := lint(t, src, "--distribution", "contrib",
		"--collector-version", "v0.157.0", "--min-severity", "error", "-"); code != 0 {
		t.Errorf("filelog ships in contrib, got exit %d:\n%s", code, out)
	}

	code, out, _ := lint(t, src, "--distribution", "core",
		"--collector-version", "v0.157.0", "--min-severity", "error", "-")
	if code != 1 || !strings.Contains(out, "unknown-component") {
		t.Fatalf("filelog is not in core, got exit %d:\n%s", code, out)
	}

	// The fix is switching binaries, not correcting a typo, so the hint has to
	// say where it does ship rather than suggest a near-miss name.
	if !strings.Contains(out, "not in the core distribution") || !strings.Contains(out, "contrib") {
		t.Errorf("the hint should name the distributions that carry it:\n%s", out)
	}
}

// TestTheDistributionIsNamedInDiagnostics keeps the report unambiguous: the
// same config and release can pass or fail depending on the binary.
func TestTheDistributionIsNamedInDiagnostics(t *testing.T) {
	t.Parallel()

	src := "receivers:\n  nosuchreceiver:\nexporters:\n  debug:\n" +
		"service:\n  pipelines:\n    logs:\n      receivers: [nosuchreceiver]\n      exporters: [debug]\n"

	_, out, _ := lint(t, src, "--distribution", "core", "--collector-version", "v0.157.0", "-")
	if !strings.Contains(out, "(core)") {
		t.Errorf("the diagnostic should name the distribution checked against:\n%s", out)
	}
}

func TestUnknownVersionFallsBackToTheNearestOlder(t *testing.T) {
	t.Parallel()

	code, _, errOut := lint(t, "", "--collector-version", "v0.155.0", "--min-severity", "error", validConfig)
	if code != 0 {
		t.Fatalf("want a fallback, got exit %d: %s", code, errOut)
	}

	if !strings.Contains(errOut, "falling back to") {
		t.Errorf("the fallback should be announced: %q", errOut)
	}
}

func TestSchemaLocationOverridesTheBuiltins(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	const component = `{"custom":{"type":"custom","signals":["logs"]}}`

	schemaJSON := `{"collectorVersion":"v9.9.9","components":` +
		`{"receiver":` + component + `,"exporter":` + component + `}}`

	err := os.WriteFile(filepath.Join(dir, "v9.9.9.json"), []byte(schemaJSON), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	src := "receivers:\n  custom:\nexporters:\n  custom:\n" +
		"service:\n  pipelines:\n    logs:\n      receivers: [custom]\n      exporters: [custom]\n"

	code, out, errOut := lint(t, src,
		"--schema-location", dir, "--collector-version", "v9.9.9", "--min-severity", "error", "-")
	if code != 0 {
		t.Errorf("a project schema should be honoured, got exit %d:\n%s%s", code, out, errOut)
	}
}

func TestIgnoreMissingSchemas(t *testing.T) {
	t.Parallel()

	src := "receivers:\n  mycorp_custom:\nexporters:\n  debug:\n" +
		"service:\n  pipelines:\n    logs:\n      receivers: [mycorp_custom]\n      exporters: [debug]\n"
	if code, _, _ := lint(t, src, "-"); code != 1 {
		t.Error("an unknown component should fail by default")
	}

	if code, out, _ := lint(t, src, "--ignore-missing-schemas", "-"); code != 0 {
		t.Errorf("--ignore-missing-schemas should tolerate it, got exit %d:\n%s", code, out)
	}
}

func TestStrictPromotesUnknownFields(t *testing.T) {
	t.Parallel()

	src := "receivers:\n  otlp:\n    protocols:\n      grpc:\nexporters:\n  debug:\n    verbosty: normal\n" +
		"service:\n  pipelines:\n    traces:\n      receivers: [otlp]\n      exporters: [debug]\n"
	if code, _, _ := lint(t, src, "-"); code != 0 {
		t.Error("an unknown field is only a warning by default")
	}

	if code, _, _ := lint(t, src, "--strict", "-"); code != 1 {
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

	if code, out, _ := lint(t, src, "--config", path, "-"); code != 0 {
		t.Errorf("the settings file should select v0.110.0, got exit %d:\n%s", code, out)
	}

	if code, _, errOut := lint(t, "", "--config", filepath.Join(dir, "missing.yaml"), validConfig); code != 2 ||
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

	code, out, _ := lint(t, src, "--config", path, "--collector-version", "v0.157.0", "-")
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

	code, out, errOut := lint(t, "", "--config", path, "--min-severity", "error", validConfig)
	if code != 0 {
		t.Fatalf("exit %d, stdout=%q stderr=%q", code, out, errOut)
	}

	if out != "" {
		t.Errorf("the default text output should stay in force, got %q", out)
	}
}

// TestOptionsFsRunsEntirelyInMemory pins that Options.Fs governs every file the
// command reads -- the settings file, the schema location and the configs it
// walks to -- so an embedder can run a lint against a tree that was never
// written to disk.
func TestOptionsFsRunsEntirelyInMemory(t *testing.T) {
	t.Parallel()

	fsys := afero.NewMemMapFs()

	memWrite(t, fsys, "/schemas/v0.157.0.json",
		`{"collectorVersion":"v0.157.0","components":{`+
			`"receiver":{"otlp":{"type":"otlp","signals":["traces"]}},`+
			`"exporter":{"debug":{"type":"debug","signals":["traces"]}}}}`)
	memWrite(t, fsys, "/etc/settings.yaml", "schemaLocations: [/schemas]\nminSeverity: error\n")
	memWrite(t, fsys, "/configs/agent.yaml",
		"receivers:\n  otlp:\nexporters:\n  debug:\n"+
			"service:\n  pipelines:\n    traces:\n      receivers: [otlp]\n      exporters: [debug]\n")

	var stdout, stderr bytes.Buffer

	cmd := otelcolconfiglint.NewCommand(&otelcolconfiglint.GlobalCmdOptions{Fs: fsys})
	cmd.SetArgs([]string{"run", "--config", "/etc/settings.yaml", "/configs"})
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	code := otelcolconfiglint.ExitCode(cmd.Execute())
	if code != 0 {
		t.Fatalf("exit %d, stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func memWrite(t *testing.T, fsys afero.Fs, path, content string) {
	t.Helper()

	err := afero.WriteFile(fsys, path, []byte(content), 0o600)
	if err != nil {
		t.Fatal(err)
	}
}

func TestExcludeSkipsFilesInADirectoryWalk(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, "broken.yaml"), []byte("receivers:\n  otlp:\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	if code, _, _ := lint(t, "", dir); code != 1 {
		t.Error("the broken file should be linted without --exclude")
	}

	if code, _, errOut := lint(t, "", "--exclude", "broken.yaml", dir); code != 2 ||
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

	code, out, errOut := lint(t, "", "--summary", "--min-severity", "error", dir)
	if code != 0 {
		t.Fatalf("exit %d, stdout=%q stderr=%q", code, out, errOut)
	}

	if !strings.Contains(out, "2 file(s) checked") {
		t.Errorf("both files in the directory should have been checked:\n%s", out)
	}
}

func TestListRulesAndVersions(t *testing.T) {
	t.Parallel()

	code, out, _ := run(t, "", "list", "rules")
	if code != 0 || !strings.Contains(out, "unknown-component") {
		t.Errorf("list rules output looks wrong (exit %d):\n%s", code, out)
	}

	code, out, _ = run(t, "", "list", "versions", "--schema-location", repoSchemas)
	if code != 0 || !strings.Contains(out, "(latest)") || !strings.Contains(out, "components") {
		t.Errorf("list versions output looks wrong (exit %d):\n%s", code, out)
	}
}

// TestListRulesHonoursTheSeverityFlags pins that the overrides still reach the
// listing now that it is a subcommand with its own flag set.
func TestListRulesHonoursTheSeverityFlags(t *testing.T) {
	t.Parallel()

	code, out, errOut := run(t, "", "list", "rules", "--severity", "missing-batch=error")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}

	if !strings.Contains(out, "(overridden)") {
		t.Errorf("--severity should be marked as an override:\n%s", out)
	}

	if code, _, errOut := run(t, "", "list", "rules", "--disable", "no-such-rule"); code != 2 ||
		!strings.Contains(errOut, "unknown rule") {
		t.Errorf("an unknown rule should be a usage error, got %d: %s", code, errOut)
	}
}

// TestListVersionsHonoursTheSchemaLocation pins that the subcommand reports
// the schemas the run would actually use, not only the built-in ones.
func TestListVersionsHonoursTheSchemaLocation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	schemaJSON := `{"collectorVersion":"v9.9.9","components":{}}`

	err := os.WriteFile(filepath.Join(dir, "v9.9.9.json"), []byte(schemaJSON), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	code, out, errOut := run(t, "", "list", "versions", "--schema-location", dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}

	if !strings.Contains(out, "v9.9.9") {
		t.Errorf("a project schema should be listed:\n%s", out)
	}
}

// TestListSubcommandsRejectLintFlags pins the point of the split: the listings
// no longer advertise or accept the flags that only shape a lint run.
func TestListSubcommandsRejectLintFlags(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"list", "rules", "--strict"},
		{"list", "versions", "--output", "json"},
	} {
		code, _, errOut := run(t, "", args...)
		if code != 2 || !strings.Contains(errOut, "unknown flag") {
			t.Errorf("%v should be a usage error, got %d: %s", args, code, errOut)
		}
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

// TestABareInvocationPrintsHelp pins that the root command does no work of its
// own: with no subcommand there is nothing to run, so it lists the ones there
// are instead of failing.
func TestABareInvocationPrintsHelp(t *testing.T) {
	t.Parallel()

	code, out, errOut := run(t, "")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}

	for _, want := range []string{"run", "list", "version"} {
		if !strings.Contains(out, want) {
			t.Errorf("the help should list %q:\n%s", want, out)
		}
	}
}

func TestNoArgumentsPrintsUsage(t *testing.T) {
	t.Parallel()

	code, _, errOut := lint(t, "")
	if code != 2 || !strings.Contains(errOut, "Usage:") {
		t.Errorf("want usage on exit 2, got %d: %s", code, errOut)
	}
}

func TestAnUnknownFlagIsAUsageError(t *testing.T) {
	t.Parallel()

	code, _, errOut := lint(t, "", "--no-such-flag", validConfig)
	if code != 2 || !strings.Contains(errOut, "Usage:") {
		t.Errorf("want usage on exit 2, got %d: %s", code, errOut)
	}
}

// TestTheRootRejectsLintFlags pins that the lint flags moved to "run" rather
// than being shared, so a stale command line fails loudly.
func TestTheRootRejectsLintFlags(t *testing.T) {
	t.Parallel()

	code, _, errOut := run(t, "", "--strict", validConfig)
	if code != 2 || !strings.Contains(errOut, "unknown flag") {
		t.Errorf("want a usage error, got %d: %s", code, errOut)
	}
}

// TestFindingsDoNotPrintUsage keeps the common case readable: a config with
// findings is not a misuse of the command.
func TestFindingsDoNotPrintUsage(t *testing.T) {
	t.Parallel()

	code, _, errOut := lint(t, "", badConfig)
	if code != 1 {
		t.Fatalf("want exit 1, got %d", code)
	}

	if strings.Contains(errOut, "Usage:") {
		t.Errorf("findings should not print the usage text:\n%s", errOut)
	}
}

func TestSummaryCountsFiles(t *testing.T) {
	t.Parallel()

	_, out, _ := lint(t, "", "--summary", "--min-severity", "error", validConfig, badConfig)
	if !strings.Contains(out, "2 file(s) checked, 1 valid, 1 invalid") {
		t.Errorf("unexpected summary:\n%s", out)
	}
}

// TestAFileNamedTwiceIsCheckedOnce pins that naming a file directly and also
// walking into the directory holding it does not check it twice.
func TestAFileNamedTwiceIsCheckedOnce(t *testing.T) {
	t.Parallel()

	_, out, _ := lint(t, "", "--summary", "--min-severity", "error",
		validConfig, filepath.Join(validConfig, "agent.yaml"))
	if !strings.Contains(out, "1 file(s) checked") {
		t.Errorf("the file should have been de-duplicated:\n%s", out)
	}
}

// TestReportOrderDoesNotDependOnArgumentOrder pins that results come out in
// path order, so the same set of files reads the same however it was named.
func TestReportOrderDoesNotDependOnArgumentOrder(t *testing.T) {
	t.Parallel()

	_, forwards, _ := lint(t, "", "--output", "tap", validConfig, invalidConfig)
	_, backwards, _ := lint(t, "", "--output", "tap", invalidConfig, validConfig)

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

// limiterConfig is a config whose only interesting part is a memory_limiter
// with a fixed limit: room enough in a gateway's container, far too much in an
// agent's.
const limiterConfig = `
receivers:
  otlp:
    protocols:
      grpc:
processors:
  memory_limiter:
    check_interval: 1s
    limit_mib: 512
    spike_limit_mib: 128
  batch:
exporters:
  debug:
service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [memory_limiter, batch]
      exporters: [debug]
`

// limiterWith is limiterConfig with the memory_limiter settings replaced, for
// the cases where the limiter itself is what is wrong. The settings are written
// already indented by four spaces.
func limiterWith(settings string) string {
	const declared = "    check_interval: 1s\n    limit_mib: 512\n    spike_limit_mib: 128\n"

	return strings.Replace(limiterConfig, declared, settings+"\n", 1)
}

// writeFile writes a fixture, creating the directories leading to it.
func writeFile(t *testing.T, path, content string) {
	t.Helper()

	err := os.MkdirAll(filepath.Dir(path), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(path, []byte(content), 0o600)
	if err != nil {
		t.Fatal(err)
	}
}

// finding is one diagnostic of a JSON report, as a test reads it.
type finding struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Docs     string `json:"docs"`
}

// fileReport is one file's entry in a JSON report.
type fileReport struct {
	Filename    string    `json:"filename"`
	Diagnostics []finding `json:"diagnostics"`
}

// findings returns the diagnostics of a JSON report, keyed by file base name.
func findings(t *testing.T, out string) map[string][]finding {
	t.Helper()

	var report struct {
		Files []fileReport `json:"files"`
	}

	err := json.Unmarshal([]byte(out), &report)
	if err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}

	byFile := lo.GroupBy(report.Files, func(f fileReport) string { return filepath.Base(f.Filename) })

	return lo.MapValues(byFile, func(files []fileReport, _ string) []finding {
		return lo.FlatMap(files, func(f fileReport, _ int) []finding { return f.Diagnostics })
	})
}

// rulesFired returns the rules each file in a JSON report was flagged by.
func rulesFired(t *testing.T, out string) map[string][]string {
	t.Helper()

	return lo.MapValues(findings(t, out), func(found []finding, _ string) []string {
		return lo.Map(found, func(d finding, _ int) string { return d.Rule })
	})
}

// TestEnvironmentIsResolvedPerFile is the point of the whole kubernetes block:
// one run over one directory, two workloads, and only the one that does not fit
// its container is flagged.
func TestEnvironmentIsResolvedPerFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "configs", "agent-node.yaml"), limiterConfig)
	writeFile(t, filepath.Join(dir, "configs", "gateway", "gw.yaml"), limiterConfig)
	writeFile(t, filepath.Join(dir, "configs", "legacy", "old.yaml"), limiterConfig)

	settings := filepath.Join(dir, "settings.yaml")
	writeFile(t, settings, `
kubernetes:
  memoryRequest: 512Mi
  memoryLimit: 512Mi
  overrides:
    - paths: ["agent-*.yaml"]
      memoryRequest: 256Mi
      memoryLimit: 256Mi
    - paths: ["gw.yaml"]
      memoryRequest: 4Gi
      memoryLimit: 4Gi
    - paths: ["old.yaml"]
      enabled: false
`)

	_, out, errOut := lint(t, "", "--config", settings, "--output", "json", filepath.Join(dir, "configs"))

	fired := rulesFired(t, out)
	if !slices.Contains(fired["agent-node.yaml"], "memory-limiter-sizing") {
		t.Errorf("512Mi does not fit a 256Mi container: %v\n%s", fired, errOut)
	}

	if slices.Contains(fired["gw.yaml"], "memory-limiter-sizing") {
		t.Errorf("512Mi fits a 4Gi container: %v", fired)
	}

	if slices.Contains(fired["old.yaml"], "memory-limiter-sizing") {
		t.Errorf("an override that turns kubernetes off should opt the file out: %v", fired)
	}
}

// TestMemoryFlagsCoverTheSingleFileCase pins that the flags feed the defaults
// and imply that the config runs in Kubernetes.
func TestMemoryFlagsCoverTheSingleFileCase(t *testing.T) {
	t.Parallel()

	_, out, _ := lint(t, limiterConfig, "--memory-limit", "256Mi", "--output", "json", "-")
	if !slices.Contains(rulesFired(t, out)["stdin"], "memory-limiter-sizing") {
		t.Errorf("--memory-limit alone should be enough to size the limiter:\n%s", out)
	}

	_, out, _ = lint(t, limiterConfig, "--memory-limit", "4Gi", "--output", "json", "-")
	if slices.Contains(rulesFired(t, out)["stdin"], "memory-limiter-sizing") {
		t.Errorf("512Mi fits a 4Gi container:\n%s", out)
	}
}

// TestFlagsWinOverTheKubernetesBlock keeps the environment on the same
// precedence rule as every other option.
func TestFlagsWinOverTheKubernetesBlock(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.yaml")
	writeFile(t, settings, "kubernetes:\n  memoryLimit: 4Gi\n")

	_, out, _ := lint(t, limiterConfig, "--config", settings, "--memory-limit", "256Mi", "--output", "json", "-")
	if !slices.Contains(rulesFired(t, out)["stdin"], "memory-limiter-sizing") {
		t.Errorf("--memory-limit should beat the settings file:\n%s", out)
	}
}

func TestBadMemoryQuantityIsAUsageError(t *testing.T) {
	t.Parallel()

	code, _, errOut := lint(t, "", "--memory-limit", "512MB", validConfig)
	if code != 2 || !strings.Contains(errOut, "not a memory quantity") {
		t.Errorf("a bad quantity should stop the run, got exit %d: %s", code, errOut)
	}

	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.yaml")
	writeFile(t, settings, "kubernetes:\n  overrides:\n    - paths: [\"[bad\"]\n      memoryLimit: 1Gi\n")

	code, _, errOut = lint(t, "", "--config", settings, validConfig)
	if code != 2 || !strings.Contains(errOut, "override 1") {
		t.Errorf("a malformed glob should stop the run, got exit %d: %s", code, errOut)
	}
}

// TestVerboseSaysWhichEnvironmentAFileGot answers "why was this file not
// checked" without anyone re-reading the glob list.
func TestVerboseSaysWhichEnvironmentAFileGot(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "agent-node.yaml"), limiterConfig)

	_, _, errOut := lint(t, "", "--verbose", "--memory-limit", "256Mi", "--memory-request", "256Mi", dir)
	if !strings.Contains(errOut, "agent-node.yaml: kubernetes, memory request 256Mi, memory limit 256Mi") {
		t.Errorf("a verbose run should say what each file resolved to:\n%s", errOut)
	}

	_, _, errOut = lint(t, "", "--verbose", dir)
	if strings.Contains(errOut, "no deployment environment") {
		t.Errorf("with no environment configured there is nothing to say:\n%s", errOut)
	}
}

// TestVerboseSaysWhenAFileHasNoEnvironment is the other half of the question
// --verbose answers: with a policy in force, a file the policy opts out has to
// say so, or a rule that stayed silent looks like a rule that passed.
func TestVerboseSaysWhenAFileHasNoEnvironment(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "configs", "old.yaml"), limiterConfig)

	settings := filepath.Join(dir, "settings.yaml")
	writeFile(t, settings, "kubernetes:\n  memoryLimit: 256Mi\n  overrides:\n"+
		"    - paths: [\"old.yaml\"]\n      enabled: false\n")

	_, out, errOut := lint(t, "", "--config", settings, "--verbose", filepath.Join(dir, "configs"))

	assert.Contains(t, errOut, "old.yaml: no deployment environment", "an opted-out file should say so")
	assert.NotContains(t, out, "memory-limiter-sizing", "an opted-out file is not sized")
}

// TestMemoryLimiterConfigReachesTheCommandLine pins that the rule's findings
// survive the whole path a user sees -- the real schema, the severity gate and
// the text formatter -- and that a config the collector refuses to start on
// fails the run.
func TestMemoryLimiterConfigReachesTheCommandLine(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		settings string
		says     string
	}{
		"a check_interval of zero": {
			settings: "    check_interval: 0s\n    limit_mib: 512",
			says:     "'check_interval' must be greater than zero",
		},
		"no limit at all": {
			settings: "    check_interval: 1s",
			says:     "'limit_mib' or 'limit_percentage' must be greater than zero",
		},
		"a spike at the limit": {
			settings: "    check_interval: 1s\n    limit_mib: 512\n    spike_limit_mib: 512",
			says:     "'spike_limit_mib' must be smaller than 'limit_mib'",
		},
		"a percentage above a hundred": {
			settings: "    check_interval: 1s\n    limit_percentage: 120",
			says:     "less than or equal to hundred",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			code, out, errOut := lint(t, limiterWith(tt.settings), "-")
			require.Equal(t, 1, code, "a limiter the collector rejects should fail the run: %s%s", out, errOut)

			assert.Contains(t, out, tt.says)
			assert.Contains(t, out, "[memory-limiter-config]")
		})
	}
}

// TestAWorkingLimiterPassesTheCommandLine is the other side of the rule: the
// configuration upstream recommends comes through clean, down to the last
// remark, or the rule is noise a project will turn off.
func TestAWorkingLimiterPassesTheCommandLine(t *testing.T) {
	t.Parallel()

	code, out, errOut := lint(t, limiterConfig, "--fail-on", "info", "--min-severity", "warning", "-")
	require.Equal(t, 0, code, "a recommended limiter should pass: %s%s", out, errOut)
}

// TestFindingsCiteUpstreamInEveryFormat pins that the citation reaches whoever
// is reading. A link the reporter drops is not a citation, and the machine
// formats are where a reviewer meets the finding.
func TestFindingsCiteUpstreamInEveryFormat(t *testing.T) {
	t.Parallel()

	const docs = "processor/memorylimiterprocessor/README.md"

	src := limiterWith("    check_interval: 1s")

	_, text, _ := lint(t, src, "-")
	assert.Contains(t, text, "docs: https://", "the text report should cite upstream")
	assert.Contains(t, text, docs)

	_, out, _ := lint(t, src, "--output", "json", "-")
	cited := lo.ContainsBy(findings(t, out)["stdin"], func(d finding) bool {
		return d.Rule == "memory-limiter-config" && strings.Contains(d.Docs, docs)
	})
	assert.True(t, cited, "the JSON report should carry a docs field:\n%s", out)

	_, github, _ := lint(t, src, "--output", "github", "-")
	assert.Contains(t, github, "%0Adocs: https://", "an annotation carries the citation, newline escaped")
}

// TestSizingIsAWarningNotAGate pins the severity the rule was given: a limiter
// that merely leaves too little headroom is a remark, and only a stricter gate
// turns it into a failure.
func TestSizingIsAWarningNotAGate(t *testing.T) {
	t.Parallel()

	// 512Mi enforced in a 600Mi container: above the documented 80% ceiling,
	// but not yet a limit the kernel wins.
	const container = "600Mi"

	code, out, errOut := lint(t, limiterConfig, "--memory-limit", container, "-")
	require.Equal(t, 0, code, "a sizing warning does not fail the run: %s%s", out, errOut)
	require.Contains(t, out, "[memory-limiter-sizing]")

	code, _, _ = lint(t, limiterConfig, "--memory-limit", container, "--fail-on", "warning", "-")
	assert.Equal(t, 1, code, "--fail-on warning should make the sizing warning fail")

	code, out, _ = lint(t, limiterConfig, "--memory-limit", container, "--disable", "memory-limiter-sizing", "-")
	assert.Equal(t, 0, code)
	assert.NotContains(t, out, "memory-limiter-sizing", "the rule is switchable off like any other")
}

// TestAnOverrideReplacesTheDefaults pins the documented merge rule: what a file
// resolves to is stated in one place, so an override naming only a limit does
// not inherit the default request.
func TestAnOverrideReplacesTheDefaults(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "configs", "agent-node.yaml"), limiterConfig)
	writeFile(t, filepath.Join(dir, "configs", "other.yaml"), limiterConfig)

	settings := filepath.Join(dir, "settings.yaml")
	writeFile(t, settings, `
kubernetes:
  memoryRequest: 128Mi
  memoryLimit: 4Gi
  overrides:
    - paths: ["agent-*.yaml"]
      memoryLimit: 4Gi
`)

	_, out, _ := lint(t, "", "--config", settings, "--output", "json", filepath.Join(dir, "configs"))

	found := findings(t, out)
	said := func(file string) string {
		return strings.Join(lo.Map(found[file], func(d finding, _ int) string { return d.Message }), "\n")
	}

	assert.Contains(t, said("other.yaml"), "memory request of 128Mi",
		"a file no override matches takes the defaults")
	assert.NotContains(t, said("agent-node.yaml"), "memory request",
		"an override states the whole environment, so the default request is gone")
}

// TestTheFirstMatchingOverrideWins pins the order rule, which is what lets a
// single file be carved out of a pattern that also covers it.
func TestTheFirstMatchingOverrideWins(t *testing.T) {
	t.Parallel()

	const overrides = `
kubernetes:
  memoryLimit: 4Gi
  overrides:
    - paths: [%q]
      memoryLimit: %s
    - paths: [%q]
      memoryLimit: %s
`

	tests := map[string]struct {
		settings string
		sized    bool
	}{
		"the roomy container is named first": {
			settings: fmt.Sprintf(overrides, "*.yaml", "4Gi", "agent-node.yaml", "128Mi"),
			sized:    false,
		},
		"the tight container is named first": {
			settings: fmt.Sprintf(overrides, "agent-node.yaml", "128Mi", "*.yaml", "4Gi"),
			sized:    true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "configs", "agent-node.yaml"), limiterConfig)

			settings := filepath.Join(dir, "settings.yaml")
			writeFile(t, settings, tt.settings)

			_, out, _ := lint(t, "", "--config", settings, "--output", "json", filepath.Join(dir, "configs"))

			fired := rulesFired(t, out)["agent-node.yaml"]
			if tt.sized {
				assert.Contains(t, fired, "memory-limiter-sizing", "the first matching override decides")
			} else {
				assert.NotContains(t, fired, "memory-limiter-sizing", "the first matching override decides")
			}
		})
	}
}

// TestStdinTakesTheDefaultEnvironment pins what the README promises about a
// config with no path: it is reported as "stdin", which the overrides are not
// meant to match, so it resolves to the defaults rather than to whichever
// pattern happens to be broad.
func TestStdinTakesTheDefaultEnvironment(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.yaml")
	writeFile(t, settings, "kubernetes:\n  memoryLimit: 256Mi\n  overrides:\n"+
		"    - paths: [\"*.yaml\"]\n      memoryLimit: 4Gi\n")

	_, out, _ := lint(t, limiterConfig, "--config", settings, "--output", "json", "-")

	assert.Contains(t, rulesFired(t, out)["stdin"], "memory-limiter-sizing",
		"stdin should have taken the 256Mi default")
}

// TestAMemoryQuantityThatDoesNotFitIsRejected pins the boundary of the byte
// count. 8Ei is 2^63, the first size an int64 cannot hold; converting it would
// give a different number per architecture, so it has to stop the run the way
// any other unreadable quantity does.
func TestAMemoryQuantityThatDoesNotFitIsRejected(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{"--memory-limit", "--memory-request"} {
		t.Run(flag, func(t *testing.T) {
			t.Parallel()

			code, _, errOut := lint(t, "", flag, "8Ei", validConfig)
			assert.Equal(t, 2, code, "%s 8Ei should stop the run: %s", flag, errOut)
			assert.Contains(t, errOut, "does not fit in a byte count")
		})
	}
}

// TestALimitTooLargeToSizeIsNotGivenANumber pins that a limit_mib no byte count
// can hold leaves the file unsized. Multiplying it out wraps, and the wrapped
// product lands back in a plausible range: the limiter below would otherwise be
// reported as enforcing exactly the container's 512Mi, a figure written nowhere.
func TestALimitTooLargeToSizeIsNotGivenANumber(t *testing.T) {
	t.Parallel()

	// 2^44 MiB is 2^64 bytes plus the 512Mi a wrap would leave behind.
	src := limiterWith("    check_interval: 1s\n    limit_mib: 17592186044928")

	_, out, _ := lint(t, src, "--memory-limit", "512Mi", "--output", "json", "-")

	assert.NotContains(t, rulesFired(t, out)["stdin"], "memory-limiter-sizing",
		"a limit that does not fit in a byte count cannot be sized")
}
