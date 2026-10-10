package otelcolconfiglint_test

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	otelcolconfiglint "github.com/minuk-dev/otelcol-config-lint/pkg/cmd/otelcol-config-lint"
	runcmd "github.com/minuk-dev/otelcol-config-lint/pkg/cmd/otelcol-config-lint/run"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
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
		"a command failure": {in: runcmd.ErrNoInput, want: otelcolconfiglint.ExitUsage},
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

func TestDynamicReferencesKeepLiteralFailures(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile(filepath.Join(validConfig, "agent.yaml"))
	require.NoError(t, err)

	for _, tt := range []struct {
		name, receivers string
		code            int
	}{
		{name: "unresolved", receivers: `"${env:RECEIVER_ID}"`, code: 0},
		{name: "literal missing beside unresolved", receivers: `"${env:RECEIVER_ID}", missing`, code: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dynamic := strings.ReplaceAll(string(src), "receivers: [otlp]", "receivers: ["+tt.receivers+"]")
			code, stdout, stderr := lint(t, dynamic,
				"--collector-version", "v0.157.0", "--distribution", "core", "--min-severity", "warning", "-")
			assert.Equal(t, tt.code, code, "%s\n%s", stdout, stderr)
			assert.NotContains(t, stdout, "RECEIVER_ID")
			assert.NotContains(t, stdout, "unused-component")
			assert.Empty(t, stderr)

			if tt.code != 0 {
				assert.Contains(t, stdout, `"missing"`)
				assert.Contains(t, stdout, "undefined-reference")
			}
		})
	}
}

func TestPreCancelledRunCannotPass(t *testing.T) {
	t.Parallel()

	for _, earlyExit := range []bool{false, true} {
		t.Run(strconv.FormatBool(earlyExit), func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			var stdout bytes.Buffer

			cmd := otelcolconfiglint.NewCommand(nil)
			cmd.SetArgs([]string{"run", "--no-config", "--schema-location", repoSchemas,
				"--exit-on-error=" + strconv.FormatBool(earlyExit), filepath.Join(validConfig, "agent.yaml")})
			cmd.SetOut(&stdout)
			cmd.SetErr(&bytes.Buffer{})
			err := cmd.ExecuteContext(ctx)
			require.ErrorIs(t, err, context.Canceled)
			assert.Equal(t, otelcolconfiglint.ExitUsage, otelcolconfiglint.ExitCode(err))
			assert.Empty(t, stdout.String())
		})
	}
}

func TestEmbeddedFlagAnnotatesMultipleConfigMaps(t *testing.T) {
	t.Parallel()

	src := `kind: ConfigMap
metadata:
  name: agent
data:
  config: |
    receivers:
      otlp:
    service:
      pipelines:
        traces:
          receivers: [missing]
          exporters: [debug]
`
	src += "---\n" + strings.Replace(src, "name: agent", "name: gateway", 1)
	code, out, errOut := lint(t, src, "--embedded", "--output", "github", "-")
	require.Equal(t, 1, code, errOut)
	assert.Contains(t, out, "file=stdin,line=11,col=23")
	assert.Contains(t, out, "agent/config")
	assert.Contains(t, out, "gateway/config")
}

func TestTrailingConfigDocumentsFail(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile(filepath.Join(validConfig, "agent.yaml"))
	require.NoError(t, err)

	for name, trailing := range map[string]string{
		"valid": "service: {}\n", "empty": "", "malformed": "this is: [broken\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			content := string(src) + "\n---\n" + trailing
			file, err := os.CreateTemp(t.TempDir(), "config-*.yaml")
			require.NoError(t, err)
			_, err = file.WriteString(content)
			require.NoError(t, err)
			require.NoError(t, file.Close())

			for inputName, input := range map[string]string{"file": file.Name(), "stdin": "-"} {
				t.Run(inputName, func(t *testing.T) {
					t.Parallel()

					code, out, errOut := lint(t, content, "--no-config", input)
					assert.Equal(t, 1, code, errOut)
					assert.Contains(t, out, "yaml-syntax")

					if name == "malformed" {
						assert.Contains(t, out, "expected")
					} else {
						assert.Contains(t, out, "more than one document")
					}
				})
			}
		})
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

func TestMalformedServiceReferencesFail(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile(filepath.Join(validConfig, "agent.yaml"))
	require.NoError(t, err)

	for slot, refs := range map[string]string{
		"receivers": "otlp", "processors": "memory_limiter, batch",
		"exporters": "debug", "extensions": "zpages",
	} {
		t.Run(slot, func(t *testing.T) {
			t.Parallel()

			bad := strings.Replace(string(src), slot+": ["+refs+"]",
				slot+": ["+refs+", {bogus: true}]", 1)
			require.NotEqual(t, string(src), bad)

			code, out, errOut := lint(t, bad, "--no-config", "--collector-version", "v0.157.0", "--output", "json", "-")
			require.Equal(t, 1, code, errOut)

			var report struct {
				Files []struct {
					Status      string           `json:"status"`
					Diagnostics diag.Diagnostics `json:"diagnostics"`
				} `json:"files"`
			}

			require.NoError(t, json.Unmarshal([]byte(out), &report))
			require.Len(t, report.Files, 1)
			assert.Equal(t, "invalid", report.Files[0].Status)
			found := slices.DeleteFunc(report.Files[0].Diagnostics, func(d diag.Diagnostic) bool {
				return d.Rule != "wrong-node-type"
			})
			require.Len(t, found, 1)
			d := found[0]
			assert.Equal(t, "wrong-node-type", d.Rule)
			assert.Equal(t, diag.Error, d.Severity)
			assert.Positive(t, d.Position.Line)
			assert.Positive(t, d.Position.Column)
		})
	}
}

type countedFS struct {
	afero.Fs

	path  string
	opens atomic.Int64
}

func (f *countedFS) Open(name string) (afero.File, error) {
	if name == f.path {
		f.opens.Add(1)
	}

	return f.Fs.Open(name)
}

type cancelOnOpenFS struct {
	afero.Fs

	path   string
	cancel context.CancelFunc
}

func (f *cancelOnOpenFS) Open(name string) (afero.File, error) {
	if name == f.path {
		f.cancel()
	}

	return f.Fs.Open(name)
}

func TestCancelledRunStopsReadingLaterFiles(t *testing.T) {
	t.Parallel()

	for _, earlyExit := range []bool{false, true} {
		t.Run(strconv.FormatBool(earlyExit), func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			first := filepath.Join(validConfig, "agent.yaml")
			later := filepath.Join(t.TempDir(), "later.yaml")
			require.NoError(t, os.WriteFile(later, []byte("service: {}\n"), 0o600))
			fsys := &countedFS{
				Fs:    &cancelOnOpenFS{Fs: afero.NewOsFs(), path: first, cancel: cancel},
				path:  later,
				opens: atomic.Int64{},
			}

			var stdout bytes.Buffer

			cmd := otelcolconfiglint.NewCommand(&otelcolconfiglint.GlobalCmdOptions{Fs: fsys})
			cmd.SetArgs([]string{"run", "--no-config", "--schema-location", repoSchemas,
				"--concurrency=1", "--exit-on-error=" + strconv.FormatBool(earlyExit), first, later})
			cmd.SetOut(&stdout)
			cmd.SetErr(&bytes.Buffer{})
			err := cmd.ExecuteContext(ctx)
			require.ErrorIs(t, err, context.Canceled)
			assert.Equal(t, otelcolconfiglint.ExitUsage, otelcolconfiglint.ExitCode(err))
			assert.Zero(t, fsys.opens.Load())
			assert.Empty(t, stdout.String())
		})
	}
}

func TestExitOnErrorStopsReadingLaterFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	first, later := filepath.Join(dir, "a.yaml"), filepath.Join(dir, "b.yaml")
	require.NoError(t, os.WriteFile(first, []byte("receivers: [\n"), 0o600))
	require.NoError(t, os.WriteFile(later, []byte("service: {}\n"), 0o600))

	fsys := &countedFS{Fs: afero.NewOsFs(), path: later, opens: atomic.Int64{}}
	cmd := otelcolconfiglint.NewCommand(&otelcolconfiglint.GlobalCmdOptions{Fs: fsys})
	cmd.SetArgs([]string{"run", "--schema-location", repoSchemas, "--exit-on-error", first, later})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	if code := otelcolconfiglint.ExitCode(cmd.Execute()); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}

	if got := fsys.opens.Load(); got != 0 {
		t.Fatalf("later file was read %d times after first failure", got)
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

func TestStdinAndFilesKeepPathOrder(t *testing.T) {
	t.Parallel()

	for _, earlyExit := range []bool{false, true} {
		t.Run(strconv.FormatBool(earlyExit), func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(validConfig, "agent.yaml")
			src, err := os.ReadFile(path)
			require.NoError(t, err)
			code, out, errOut := lint(t, string(src), "--no-config", "--min-severity", "error",
				"--output", "json", "--verbose", "--exit-on-error="+strconv.FormatBool(earlyExit), path, "-")
			require.Zero(t, code, errOut)

			var report struct {
				Files []struct {
					Filename string `json:"filename"`
				} `json:"files"`
			}

			require.NoError(t, json.Unmarshal([]byte(out), &report))
			require.Len(t, report.Files, 2)
			assert.Equal(t, "stdin", report.Files[0].Filename)
			assert.Equal(t, path, report.Files[1].Filename)
		})
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

func TestJUnitFailureThresholds(t *testing.T) {
	t.Parallel()

	const src = "receivers:\n  otlp:\n    protocols:\n      grpc:\n        endpiont: localhost:4317\n"

	for _, tt := range []struct {
		name        string
		severity    string
		failOn      string
		minSeverity string
		wantCode    int
	}{
		{name: "warning passes", severity: "warning", failOn: "error", minSeverity: "info", wantCode: 0},
		{name: "warning fails", severity: "warning", failOn: "warning", minSeverity: "info", wantCode: 1},
		{name: "hidden warning fails", severity: "warning", failOn: "warning", minSeverity: "error", wantCode: 1},
		{name: "info passes", severity: "info", failOn: "warning", minSeverity: "info", wantCode: 0},
		{name: "info fails", severity: "info", failOn: "info", minSeverity: "info", wantCode: 1},
		{name: "hidden info fails", severity: "info", failOn: "info", minSeverity: "error", wantCode: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			input, ruleName := src, "unknown-field"
			if tt.severity == "info" {
				// unknown-field sets each finding's severity; use a rule that accepts overrides for info.
				input, ruleName = "recievers: {}\n", "unknown-top-level-key"
			}

			code, out, errOut := lint(t, input,
				"--no-config", "--collector-version", "v0.157.0", "--default", "none", "--enable", ruleName,
				"--strict=false", "--severity", ruleName+"="+tt.severity, "--fail-on", tt.failOn,
				"--min-severity", tt.minSeverity, "--output", "junit", "-",
			)
			require.Equal(t, tt.wantCode, code, errOut)

			var report struct {
				Tests    int `xml:"tests,attr"`
				Failures int `xml:"failures,attr"`
				Errors   int `xml:"errors,attr"`
				Cases    []struct {
					Failures []struct {
						Message string `xml:"message,attr"`
					} `xml:"failure"`
				} `xml:"testcase"`
			}

			require.NoError(t, xml.Unmarshal([]byte(out), &report))
			assert.Equal(t, 1, report.Tests)
			assert.Equal(t, tt.wantCode, report.Failures)
			assert.Zero(t, report.Errors)
			require.Len(t, report.Cases, 1)
			require.Len(t, report.Cases[0].Failures, tt.wantCode)

			if tt.wantCode != 0 {
				assert.NotEmpty(t, report.Cases[0].Failures[0].Message)
			}
		})
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

// TestUnknownVersionEndsTheRun pins that a release the registry does not carry
// is a usage error rather than a green run against some other release. The
// exit code is the only thing CI reads.
func TestUnknownVersionEndsTheRun(t *testing.T) {
	t.Parallel()

	code, _, errOut := lint(t, "", "--collector-version", "v0.155.0", "--min-severity", "error", validConfig)
	require.Equal(t, 2, code, "an unavailable release should be a usage error: %s", errOut)

	assert.Contains(t, errOut, "v0.155.0", "the error should name what was asked for")
	assert.Contains(t, errOut, "the nearest release available is v0.110.0",
		"the error should name the nearest release, so the fix is one edit away")
	assert.Contains(t, errOut, "--allow-nearest-fallback",
		"the error should name the flag that accepts the older schema")
}

// TestAllowNearestFallbackChecksAgainstTheOlderRelease pins the opt-in, for a
// repository deliberately tracking ahead of the registry.
func TestAllowNearestFallbackChecksAgainstTheOlderRelease(t *testing.T) {
	t.Parallel()

	code, _, errOut := lint(t, "", "--collector-version", "v0.155.0", "--allow-nearest-fallback",
		"--min-severity", "error", validConfig)
	require.Equal(t, 0, code, "want a fallback, got exit %d: %s", code, errOut)

	assert.Contains(t, errOut, "falling back to", "the fallback should still be announced")
}

func TestSchemaIdentityMismatchEndsTheRun(t *testing.T) {
	t.Parallel()

	for name, tt := range map[string]struct {
		version, distribution, wantErr string
	}{
		"version":      {"v0.110.0", "core", `collectorVersion is "v0.110.0", want "v0.157.0"`},
		"distribution": {"v0.157.0", "contrib", `distribution is "contrib", want "core"`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			path := filepath.Join(dir, "v0.157.0.json")
			body := `{"collectorVersion":"` + tt.version + `","distribution":"` + tt.distribution +
				`","components":{"receiver":{"otlp":{}}}}`
			require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

			for mode, version := range map[string]string{
				"file": "v0.157.0", "latest": "latest", "fallback": "v0.158.0",
			} {
				t.Run(mode, func(t *testing.T) {
					t.Parallel()

					location := dir
					if mode == "file" {
						location = path
					}

					code, out, errOut := run(t, "receivers: {}", "run", "--no-config",
						"--schema-location", location, "--collector-version", version, "--distribution", "core",
						"--allow-nearest-fallback", "--ignore-missing-schemas", "--output", "json", "-")
					require.Equal(t, otelcolconfiglint.ExitUsage, code, "%s", errOut)
					assert.Empty(t, out)
					assert.Contains(t, errOut, "load schema")
					assert.Contains(t, errOut, "schema identity mismatch")
					assert.Contains(t, errOut, tt.wantErr)
					assert.Contains(t, errOut, location)
					assert.Contains(t, errOut, "--schema-location")

					if mode == "fallback" {
						assert.Contains(t, errOut, "falling back to v0.157.0")
					} else {
						assert.NotContains(t, errOut, "falling back")
					}
				})
			}
		})
	}
}

// TestAllowNearestFallbackIsAlsoASettingsKey pins that the opt-in can be
// committed, since a repository tracking ahead of the registry does so for
// every run rather than one.
func TestAllowNearestFallbackIsAlsoASettingsKey(t *testing.T) {
	t.Parallel()

	path := writeSettings(t, "run:\n  allowNearestFallback: true\n  collectorVersion: v0.155.0\n")

	code, _, errOut := lint(t, "", "--config", path, "--min-severity", "error", validConfig)
	require.Equal(t, 0, code, "the key should permit the fallback: %s", errOut)

	assert.Contains(t, errOut, "falling back to", "the fallback should still be announced")
}

// TestNoNearestReleaseIsStillAUsageError pins the case with nothing older to
// name: the store's own error is what a reader gets, rather than a hint that
// would name no release.
func TestNoNearestReleaseIsStillAUsageError(t *testing.T) {
	t.Parallel()

	code, _, errOut := lint(t, "", "--collector-version", "v0.1.0", "--allow-nearest-fallback",
		"--min-severity", "error", validConfig)
	require.Equal(t, 2, code, "a release older than every schema should not resolve: %s", errOut)

	assert.Contains(t, errOut, "no schema for collector version v0.1.0")
	assert.NotContains(t, errOut, "the nearest release available")
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

func TestEmptySchemaEndsTheRun(t *testing.T) {
	t.Parallel()

	const src = `receivers:
  imaginaryreceiver: {}
exporters:
  imaginaryexporter: {}
service:
  pipelines:
    traces:
      receivers: [imaginaryreceiver]
      exporters: [imaginaryexporter]
`

	for name, input := range map[string]string{
		"empty object":     `{}`,
		"null":             `null`,
		"empty inventory":  `{"collectorVersion":"v0.157.0","components":{}}`,
		"empty kinds":      `{"components":{"receiver":{},"processor":{},"exporter":{},"connector":{},"extension":{}}}`,
		"YAML empty kinds": "components:\n  receiver: {}\n  exporter: {}\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(dir, "contrib"), 0o700))
			path := filepath.Join(dir, "contrib", "v0.157.0.json")
			require.NoError(t, os.WriteFile(path, []byte(input), 0o600))

			index := `{"distributions":{"contrib":["v0.157.0"]},"extensions":{"contrib":".json"}}`
			require.NoError(t, os.WriteFile(filepath.Join(dir, "index.json"), []byte(index), 0o600))

			srv := httptest.NewServer(http.FileServer(http.Dir(dir)))
			t.Cleanup(srv.Close)

			for location, target := range map[string]string{
				"local file":      path,
				"local registry":  dir,
				"remote registry": srv.URL,
			} {
				t.Run(location, func(t *testing.T) {
					t.Parallel()

					versions := map[string]string{"exact": "v0.157.0"}
					if location != "local file" {
						versions["latest"] = "latest"
						versions["fallback"] = "v0.158.0"
					}

					for mode, version := range versions {
						t.Run(mode, func(t *testing.T) {
							t.Parallel()

							code, out, errOut := run(t, src, "run", "--no-config", "--no-cache",
								"--insecure-schema-location", "--schema-location", target,
								"--collector-version", version, "--allow-nearest-fallback",
								"--ignore-missing-schemas", "--output", "json", "-")
							require.Equal(t, otelcolconfiglint.ExitUsage, code, "%s", errOut)
							assert.Empty(t, out, "an unusable schema must not emit a lint result")

							if strings.Contains(input, "collectorVersion") {
								assert.Contains(t, errOut, "load schema: component inventory is empty")
							} else {
								assert.Contains(t, errOut, `schema identity mismatch: collectorVersion is ""`)
							}

							assert.Contains(t, errOut, "--schema-location")

							if mode == "fallback" {
								assert.Contains(t, errOut, "falling back to v0.157.0")
							}
						})
					}
				})
			}
		})
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

// TestAPlainHTTPSchemaLocationIsRefused pins that the schema a run reasons
// from may not arrive over a transport anyone on the path can rewrite, and
// that the refusal is reported where a bad flag is.
func TestAPlainHTTPSchemaLocationIsRefused(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.FileServer(http.Dir(repoSchemas)))
	defer srv.Close()

	code, _, errOut := run(t, "", "run", "--no-config", "--schema-location", srv.URL, validConfig)
	require.Equal(t, otelcolconfiglint.ExitUsage, code, "an http:// location should not run: %s", errOut)
	assert.Contains(t, errOut, "http", "the message should say what was refused")

	// The escape hatch is what a registry served on localhost is read under.
	code, _, errOut = run(t, "", "run", "--no-config", "--insecure-schema-location",
		"--schema-location", srv.URL, validConfig)
	assert.Equal(t, otelcolconfiglint.ExitOK, code, "the opt-in should permit the location: %s", errOut)
}

// TestListVersionsRefusesAPlainHTTPLocation pins that the listings hold to the
// same rule: they read the same registries a run does.
func TestListVersionsRefusesAPlainHTTPLocation(t *testing.T) {
	t.Parallel()

	code, _, errOut := run(t, "", "list", "versions", "--no-config", "--schema-location", "http://example.invalid")
	require.Equal(t, otelcolconfiglint.ExitUsage, code, "an http:// location should not be listed: %s", errOut)
	assert.Contains(t, errOut, "http", "the message should say what was refused")
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

	byFile := make(map[string][]finding)

	for _, f := range report.Files {
		base := filepath.Base(f.Filename)
		byFile[base] = append(byFile[base], f.Diagnostics...)
	}

	return byFile
}

// rulesFired returns the rules each file in a JSON report was flagged by.
func rulesFired(t *testing.T, out string) map[string][]string {
	t.Helper()

	rules := make(map[string][]string)

	for file, found := range findings(t, out) {
		for _, d := range found {
			rules[file] = append(rules[file], d.Rule)
		}
	}

	return rules
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

	code, out, errOut := lint(t, limiterConfig, "--fail-on", "warning", "--min-severity", "warning", "-")
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
	cited := false

	for _, d := range findings(t, out)["stdin"] {
		if d.Rule == "memory-limiter-config" && strings.Contains(d.Docs, docs) {
			cited = true

			break
		}
	}

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
		messages := make([]string, 0, len(found[file]))
		for _, d := range found[file] {
			messages = append(messages, d.Message)
		}

		return strings.Join(messages, "\n")
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

// TestSharedFlagsAgreeAcrossCommands pins that a flag two commands both take
// means the same thing on both: same shorthand, same default, same help. A
// command declaring its own flags is what keeps the help of each one short,
// and this is what makes doing so safe.
func TestSharedFlagsAgreeAcrossCommands(t *testing.T) {
	t.Parallel()

	// The flags more than one command declares. Each command owns its own, so
	// what keeps them from drifting is this test rather than one declaration.
	shared := map[string][][]string{
		"distribution":    {{"run"}, {"list", "versions"}},
		"schema-location": {{"run"}, {"list", "versions"}},
		"default":         {{"run"}, {"list", "rules"}},
		"enable":          {{"run"}, {"list", "rules"}},
		"disable":         {{"run"}, {"list", "rules"}},
		"severity":        {{"run"}, {"list", "rules"}},
		"config":          {{"run"}, {"list", "rules"}, {"list", "versions"}},
		"no-config":       {{"run"}, {"list", "rules"}, {"list", "versions"}},
	}

	for name, paths := range shared {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// A command tree of its own per subtest. cobra merges a parent's
			// persistent flags into a child the first time either is looked
			// at, so subtests sharing one root write to it as they read it.
			root := otelcolconfiglint.NewCommand(nil)

			var first []string

			for _, path := range paths {
				where := strings.Join(path, " ")

				cmd, _, err := root.Find(path)
				require.NoErrorf(t, err, "%s should exist", where)

				flag := cmd.Flags().Lookup(name)
				require.NotNilf(t, flag, "%s should take --%s", where, name)

				// Compared as one value so a difference names the flag rather
				// than one of its three parts.
				spelling := []string{flag.Shorthand, flag.DefValue, flag.Usage}

				if first == nil {
					first = spelling

					continue
				}

				assert.Equalf(t, first, spelling, "%s declares --%s differently", where, name)
			}
		})
	}
}

func TestNullSettingDefaults(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile(filepath.Join(validConfig, "agent.yaml"))
	require.NoError(t, err)

	for _, tt := range []struct {
		name string
		old  string
		new  string
		code int
		rule string
		path string
	}{
		{
			name: "required interval", old: "check_interval: 1s", new: "check_interval: %s",
			code: 1, rule: "memory-limiter-config", path: "processors.memory_limiter.check_interval",
		},
		{
			name: "required limit", old: "limit_mib: 512", new: "limit_mib: %s",
			code: 1, rule: "memory-limiter-config", path: "processors.memory_limiter.limit_mib",
		},
		{
			name: "null batch size decodes to zero", old: "send_batch_size: 8192",
			new:  "send_batch_size: %s\n    send_batch_max_size: 1000",
			code: 0, rule: "", path: "",
		},
		{
			name: "valid null batch size with large cap", old: "send_batch_size: 8192",
			new: "send_batch_size: %s\n    send_batch_max_size: 8192", code: 0, rule: "", path: "",
		},
		{name: "null timeout decodes to zero", old: "timeout: 5s", new: "timeout: %s", code: 0, rule: "", path: ""},
		{
			name: "uncapped batches", old: "send_batch_size: 8192",
			new: "send_batch_size: 8192\n    send_batch_max_size: %s", code: 0, rule: "", path: "",
		},
		{
			name: "default spike", old: "spike_limit_mib: 128", new: "spike_limit_mib: %s", code: 0, rule: "", path: "",
		},
		{
			name: "percentage limit with default fixed limit", old: "limit_mib: 512",
			new: "limit_mib: %s\n    limit_percentage: 80\n    spike_limit_percentage: 20", code: 0, rule: "", path: "",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for name, value := range map[string]string{"null": "null", "shorthand": "~", "empty": ""} {
				t.Run(name, func(t *testing.T) {
					t.Parallel()

					input := strings.Replace(string(src), tt.old, fmt.Sprintf(tt.new, value), 1)
					require.NotEqual(t, string(src), input)
					code, out, errOut := lint(t, input, "--no-config", "--collector-version", "v0.157.0",
						"--min-severity", "error", "--verbose", "--output", "json", "-")
					require.Equal(t, tt.code, code, "stdout=%s stderr=%s", out, errOut)

					var report struct {
						Files []struct {
							Status      string           `json:"status"`
							Diagnostics diag.Diagnostics `json:"diagnostics"`
						} `json:"files"`
					}

					require.NoError(t, json.Unmarshal([]byte(out), &report))
					require.Len(t, report.Files, 1)

					if tt.code == 0 {
						assert.Equal(t, "valid", report.Files[0].Status)
						assert.Empty(t, report.Files[0].Diagnostics)

						return
					}

					assert.Equal(t, "invalid", report.Files[0].Status)
					require.Len(t, report.Files[0].Diagnostics, 1, "avoid duplicate errors for required nulls")
					d := report.Files[0].Diagnostics[0]
					assert.Equal(t, tt.rule, d.Rule)
					assert.Equal(t, tt.path, d.Path)
					assert.Equal(t, diag.Error, d.Severity)
					assert.Positive(t, d.Position.Line)
				})
			}
		})
	}
}

func TestMemoryLimiterRequiredInterval(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name     string
		interval string
		code     int
	}{
		{name: "missing interval is required", interval: "", code: 1},
		{name: "runtime interval stays unknown", interval: "    check_interval: ${env:INTERVAL}\n", code: 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			code, out, errOut := lint(t, limiterWith(tt.interval+"    limit_mib: 512"),
				"--no-config", "--collector-version", "v0.157.0", "--min-severity", "error", "--output", "json", "-")
			require.Equal(t, tt.code, code, "stdout=%s stderr=%s", out, errOut)

			found := findings(t, out)["stdin"]
			if tt.code == 0 {
				assert.Empty(t, found)

				return
			}

			require.Len(t, found, 1)
			assert.Equal(t, "required-field", found[0].Rule)
		})
	}
}
