package lint_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/lint"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/hardcodedsecret"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/servicerequired"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

const good = `
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

const bad = `
receivers:
  otlp:
service:
  pipelines:
    traces:
      receivers: [otlp]
`

// repoSchemas is the committed schema fixture. The binary reads the published
// registry over HTTP, so tests read this instead of the network.
const repoSchemas = "../../testdata/schemas"

func repoStore() schema.Store {
	return schema.Store{Locations: []string{repoSchemas}}
}

func newLinter(t *testing.T, opts lint.Options) *lint.Linter {
	t.Helper()

	if opts.Schema == nil {
		cat, err := repoStore().Load(t.Context(), schema.Latest)
		if err != nil {
			t.Fatal(err)
		}

		opts.Schema = cat
	}

	return lint.New(opts)
}

func TestStatuses(t *testing.T) {
	t.Parallel()

	l := newLinter(t, lint.Options{MinSeverity: diag.Error})

	if r := l.Lint(t.Context(), "good.yaml", []byte(good)); r.Status != lint.Valid {
		t.Errorf("want valid, got %s: %+v", r.Status, r.Diagnostics)
	}

	if r := l.Lint(t.Context(), "bad.yaml", []byte(bad)); r.Status != lint.Invalid {
		t.Errorf("want invalid, got %s", r.Status)
	}

	if r := l.LintFile(t.Context(), filepath.Join(t.TempDir(), "nope.yaml")); r.Status != lint.Error {
		t.Errorf("a missing file should be an error, got %s", r.Status)
	}
}

func TestEmptySchemaKeepsStructuralChecks(t *testing.T) {
	t.Parallel()

	const src = `receivers:
  imaginaryreceiver: {}
service:
  pipelines:
    traces:
      receivers: [imaginaryreceiver]
`

	for name, input := range map[string]string{
		"nil schema":      "",
		"empty object":    `{}`,
		"null":            `null`,
		"empty inventory": `{"components":{"receiver":{},"exporter":{}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var cat *schema.Schema

			if input != "" {
				var err error

				cat, err = schema.Read(strings.NewReader(input))
				require.NoError(t, err)
				require.NotNil(t, cat)
				assert.Zero(t, cat.Count())
			}

			result := lint.New(lint.Options{Schema: cat, MinSeverity: diag.Error}).Lint(t.Context(), "config.yaml", []byte(src))
			require.Equal(t, lint.Invalid, result.Status)
			require.Len(t, result.Diagnostics, 1)
			assert.Equal(t, "empty-pipeline", result.Diagnostics[0].Rule)
		})
	}
}

func TestSyntaxErrorIsADiagnosticNotAFailure(t *testing.T) {
	t.Parallel()

	l := newLinter(t, lint.Options{})

	r := l.Lint(t.Context(), "broken.yaml", []byte("receivers:\n  otlp: [1, 2\n"))
	if r.Status != lint.Invalid {
		t.Fatalf("want invalid, got %s", r.Status)
	}

	if len(r.Diagnostics) != 1 || r.Diagnostics[0].Rule != "yaml-syntax" {
		t.Errorf("want a yaml-syntax diagnostic, got %+v", r.Diagnostics)
	}
}

func TestCyclicAliasesAreInvalid(t *testing.T) {
	t.Parallel()

	const childEnv = "OTELCOL_TEST_CYCLIC_ALIAS"
	if os.Getenv(childEnv) != "1" {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()

		//nolint:gosec // re-executes this test binary with a fixed test filter
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCyclicAliasesAreInvalid$")

		cmd.Env = append(os.Environ(), childEnv+"=1")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", out)

		return
	}

	// Bound a regression's stack overflow to this subprocess.
	debug.SetMaxStack(256 * 1024)

	l := newLinter(t, lint.Options{})

	for _, value := range []string{"nested: *loop", "nested: [*loop]", "<<: *loop"} {
		result := l.Lint(t.Context(), "cycle.yaml", []byte("exporters:\n  otlp: &loop\n    "+value+"\n"))
		require.Equal(t, lint.Invalid, result.Status)
		require.Len(t, result.Diagnostics, 1)
		assert.Equal(t, "yaml-syntax", result.Diagnostics[0].Rule)
		assert.Contains(t, result.Diagnostics[0].Message, "cyclic YAML alias")
		assert.Equal(t, 3, result.Diagnostics[0].Position.Line)
	}
}

func TestAliasAmplificationIsInvalid(t *testing.T) {
	t.Parallel()

	const childEnv = "OTELCOL_TEST_ALIAS_AMPLIFICATION"
	if os.Getenv(childEnv) != "1" {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		t.Cleanup(cancel)

		//nolint:gosec // re-executes this test binary with a fixed test filter
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAliasAmplificationIsInvalid$")

		cmd.Env = append(os.Environ(), childEnv+"=1")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", out)

		return
	}

	src, err := os.ReadFile("../../testdata/aliases/amplification.yaml")
	require.NoError(t, err)

	start := bytes.Index(src, []byte("    nest6:"))
	end := bytes.Index(src, []byte("\nextensions:"))

	require.Positive(t, start)
	require.Greater(t, end, start)
	bounded := strings.Replace(string(src[:start])+string(src[end:]), "value: x", "token: abc123def456", 1)

	for name, rules := range map[string][]rule.Rule{
		"all rules":        nil,
		"hardcoded-secret": {hardcodedsecret.New()},
		"no rules":         {},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			l := newLinter(t, lint.Options{Rules: rules})
			result := l.Lint(t.Context(), "amplification.yaml", src)
			require.Equal(t, lint.Invalid, result.Status)
			require.Len(t, result.Diagnostics, 1)
			assert.Equal(t, "yaml-syntax", result.Diagnostics[0].Rule)
			assert.Contains(t, result.Diagnostics[0].Message, "YAML expansion limit of 100000 nodes")
			assert.Positive(t, result.Diagnostics[0].Position.Line)
			assert.Positive(t, result.Diagnostics[0].Position.Column)

			// A smaller shared graph must still reach the index and rule walkers.
			result = l.Lint(t.Context(), "bounded.yaml", []byte(bounded))

			secrets := 0

			for _, d := range result.Diagnostics {
				assert.NotEqual(t, "yaml-syntax", d.Rule)

				if d.Rule == "hardcoded-secret" {
					secrets++
				}
			}

			if name == "no rules" {
				assert.Equal(t, lint.Valid, result.Status)
				assert.Zero(t, secrets)
			} else {
				assert.Equal(t, 1, secrets)
			}
		})
	}
}

func TestEmbeddedConfigMapsReportOuterPositions(t *testing.T) {
	t.Parallel()

	src := `apiVersion: apps/v1
kind: Deployment
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: agent
data:
  notes: |
    service: documentation
  collector.yaml: |
    receivers:
      otlp:
    service:
      pipelines:
        traces:
          receivers: [missing]
          exporters: [debug]
---
kind: ConfigMap
metadata:
  name: gateway
data:
  config: |2
      exporters:
        debug:
      service:
        pipelines:
          traces:
            receivers: [missing]
            exporters: [debug]
`
	l := newLinter(t, lint.Options{Embedded: true, MinSeverity: diag.Error})
	r := l.Lint(t.Context(), "manifest.yaml", []byte(src))
	require.Equal(t, lint.Invalid, r.Status)

	var positions []diag.Position

	for _, d := range r.Diagnostics {
		if d.Rule == "undefined-reference" && strings.Contains(d.Message, "receiver \"missing\"") {
			positions = append(positions, d.Position)
			if d.Position.Line == 17 {
				assert.Contains(t, d.Message, "agent/collector.yaml")
			} else {
				assert.Contains(t, d.Message, "gateway/config")
			}
		}
	}

	assert.Equal(t, []diag.Position{
		{File: "manifest.yaml", Line: 17, Column: 23},
		{File: "manifest.yaml", Line: 30, Column: 25},
	}, positions)

	var out bytes.Buffer

	f, err := lint.NewFormatter("github", &out, lint.FormatterOptions{})
	require.NoError(t, err)
	require.NoError(t, f.Result(r))
	require.NoError(t, f.Finish(lint.Summary{}))
	assert.Contains(t, out.String(), "file=manifest.yaml,line=17,col=23")
	assert.Contains(t, out.String(), "agent/collector.yaml")
}

func TestEmbeddedConfigMapReportsInnerSyntaxError(t *testing.T) {
	t.Parallel()

	src := "kind: ConfigMap\nmetadata:\n  name: agent\ndata:\n  config: |\n    receivers:\n      otlp: [\n"
	r := newLinter(t, lint.Options{Embedded: true}).Lint(t.Context(), "manifest.yaml", []byte(src))
	require.Equal(t, lint.Invalid, r.Status)
	require.NotEmpty(t, r.Diagnostics)
	assert.Equal(t, "yaml-syntax", r.Diagnostics[0].Rule)
	assert.Equal(t, "manifest.yaml", r.Diagnostics[0].Position.File)
	assert.GreaterOrEqual(t, r.Diagnostics[0].Position.Line, 6)
	assert.Contains(t, r.Diagnostics[0].Message, "agent/config")
}

func TestEmbeddedConfigMapReportsMissingService(t *testing.T) {
	t.Parallel()

	src := "kind: ConfigMap\ndata:\n  config: |\n    receivers:\n      otlp:\n"
	r := newLinter(t, lint.Options{Embedded: true}).Lint(t.Context(), "manifest.yaml", []byte(src))
	require.Equal(t, lint.Invalid, r.Status)

	for _, d := range r.Diagnostics {
		if d.Rule == "service-required" {
			assert.Equal(t, diag.Position{File: "manifest.yaml", Line: 4, Column: 5}, d.Position)

			return
		}
	}

	t.Fatal("missing service-required finding")
}

func TestEmbeddedConfigMapReportsEmptyPipeline(t *testing.T) {
	t.Parallel()

	src := "kind: ConfigMap\ndata:\n  config: |\n    service:\n      pipelines:\n        traces: {}\n"
	r := newLinter(t, lint.Options{Embedded: true}).Lint(t.Context(), "manifest.yaml", []byte(src))
	require.Equal(t, lint.Invalid, r.Status)

	assert.Condition(t, func() bool {
		for _, d := range r.Diagnostics {
			if d.Rule == "empty-pipeline" && d.Position.File == "manifest.yaml" {
				return true
			}
		}

		return false
	})
}

func TestMinSeverityFilters(t *testing.T) {
	t.Parallel()

	all := newLinter(t, lint.Options{MinSeverity: diag.Info}).Lint(t.Context(), "x.yaml", []byte(bad))

	errsOnly := newLinter(t, lint.Options{MinSeverity: diag.Error}).Lint(t.Context(), "x.yaml", []byte(bad))
	if len(errsOnly.Diagnostics) >= len(all.Diagnostics) {
		t.Errorf("filtering did nothing: %d vs %d", len(errsOnly.Diagnostics), len(all.Diagnostics))
	}

	for _, d := range errsOnly.Diagnostics {
		if d.Severity != diag.Error {
			t.Errorf("unexpected severity %q survived the filter", d.Severity)
		}
	}
}

func TestFailOnRaisesTheGate(t *testing.T) {
	t.Parallel()

	// A config whose only problem is an unused component: warnings only.
	src := good + "extensions:\n  zpages:\n"
	if r := newLinter(t, lint.Options{}).Lint(t.Context(), "x.yaml", []byte(src)); r.Status != lint.Valid {
		t.Errorf("warnings should not fail by default: %+v", r.Diagnostics)
	}

	strictly := newLinter(t, lint.Options{FailOn: diag.Warning})
	if r := strictly.Lint(t.Context(), "x.yaml", []byte(src)); r.Status != lint.Invalid {
		t.Error("-fail-on warning should fail")
	}

	hidden := newLinter(t, lint.Options{FailOn: diag.Warning, MinSeverity: diag.Error}).Lint(
		t.Context(), "x.yaml", []byte(src))
	if hidden.Status != lint.Invalid || len(hidden.Diagnostics) != 0 {
		t.Errorf("hidden warnings must still fail the gate: %+v", hidden)
	}
}

func TestDisabledRule(t *testing.T) {
	t.Parallel()

	l := newLinter(t, lint.Options{Severities: map[string]diag.Severity{"empty-pipeline": diag.Off}})
	for _, d := range l.Lint(t.Context(), "x.yaml", []byte(bad)).Diagnostics {
		if d.Rule == "empty-pipeline" {
			t.Fatal("a disabled rule reported anyway")
		}
	}
}

func TestIgnoreMissingSchemasSilencesUnknownComponents(t *testing.T) {
	t.Parallel()

	src := strings.Replace(good, "  otlp:\n    protocols:\n      grpc:", "  mycorp_custom:", 1)
	src = strings.Replace(src, "receivers: [otlp]", "receivers: [mycorp_custom]", 1)

	if r := newLinter(t, lint.Options{}).Lint(t.Context(), "x.yaml", []byte(src)); r.Status != lint.Invalid {
		t.Error("an unknown component should fail by default")
	}

	r := newLinter(t, lint.Options{IgnoreMissingSchemas: true}).Lint(t.Context(), "x.yaml", []byte(src))
	if r.Status != lint.Valid {
		t.Errorf("want valid, got %s: %+v", r.Status, r.Diagnostics)
	}
}

func TestLintAllPreservesInputOrder(t *testing.T) {
	t.Parallel()

	for _, workers := range []int{-1, 0, 1, 3, 100} {
		t.Run(strconv.Itoa(workers), func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				fsys := afero.NewMemMapFs()
				require.NoError(t, afero.WriteFile(fsys, "slow.yaml", []byte(good), 0o600))
				require.NoError(t, afero.WriteFile(fsys, "bad.yaml", []byte(bad), 0o600))
				require.NoError(t, afero.WriteFile(fsys, "syntax.yaml", []byte("receivers: ["), 0o600))
				l := newLinter(t, lint.Options{Fs: fsys, Environment: func(path string) rule.Environment {
					if path == "slow.yaml" {
						time.Sleep(time.Second)
					}

					return rule.Environment{}
				}})
				paths := []string{"slow.yaml", "missing.yaml", "bad.yaml", "slow.yaml", "syntax.yaml"}
				want := []lint.Status{lint.Valid, lint.Error, lint.Invalid, lint.Valid, lint.Invalid}

				results, err := l.LintAll(t.Context(), paths, workers)
				require.NoError(t, err)
				require.Len(t, results, len(paths))

				for i, result := range results {
					assert.Equal(t, paths[i], result.Path)
					assert.Equal(t, want[i], result.Status)
				}

				require.ErrorIs(t, results[1].Err, os.ErrNotExist)
			})
		})
	}
}

func TestLintAllEmptyInput(t *testing.T) {
	t.Parallel()

	l := lint.New(lint.Options{})

	for _, workers := range []int{-1, 0, 1, 100} {
		t.Run(strconv.Itoa(workers), func(t *testing.T) {
			t.Parallel()

			results, err := l.LintAll(t.Context(), nil, workers)
			require.NoError(t, err)
			assert.Nil(t, results)
		})
	}
}

type callbackRule struct {
	rule.Base

	check func()
}

func (r callbackRule) Check(_ *rule.Context) { r.check() }

type watchedFS struct {
	afero.Fs

	opens atomic.Int64
}

func (f *watchedFS) Open(name string) (afero.File, error) {
	f.opens.Add(1)

	return f.Fs.Open(name)
}

func TestPreCancelledLintDoesNoWork(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	fsys := &watchedFS{Fs: afero.NewMemMapFs(), opens: atomic.Int64{}}
	l := lint.New(lint.Options{Fs: fsys})

	reader := strings.NewReader(good)
	for name, result := range map[string]lint.Result{
		"file":     l.LintFile(ctx, "agent.yaml"),
		"reader":   l.LintReader(ctx, "stdin", reader),
		"source":   l.Lint(ctx, "agent.yaml", []byte(good)),
		"embedded": lint.New(lint.Options{Embedded: true}).Lint(ctx, "manifest.yaml", nil),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, lint.Error, result.Status)
			require.ErrorIs(t, result.Err, context.Canceled)
		})
	}

	assert.Zero(t, fsys.opens.Load())
	assert.Equal(t, len(good), reader.Len())

	for name, paths := range map[string][]string{"files": {"a.yaml", "b.yaml"}, "empty": nil} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			results, err := l.LintAll(ctx, paths, 2)
			require.ErrorIs(t, err, context.Canceled)
			assert.Nil(t, results)
			assert.Zero(t, fsys.opens.Load())
		})
	}

	assert.Zero(t, fsys.opens.Load())
}

func TestLintAllCancellationStopsWorkers(t *testing.T) {
	t.Parallel()

	for _, workers := range []int{1, 3} {
		t.Run(strconv.Itoa(workers), func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()

				entered := make(chan struct{}, workers)
				release := make(chan struct{})
				fsys := &watchedFS{Fs: afero.NewMemMapFs(), opens: atomic.Int64{}}
				require.NoError(t, afero.WriteFile(fsys, "agent.yaml", []byte(good), 0o600))
				l := lint.New(lint.Options{Fs: fsys, Rules: []rule.Rule{
					callbackRule{Base: rule.NewBase("wait", "", diag.Error), check: func() {
						entered <- struct{}{}

						<-release
					}},
					callbackRule{Base: rule.NewBase("later", "", diag.Error), check: func() {
						t.Error("a later rule ran after cancellation")
					}},
				}})

				paths := make([]string, workers+10)
				for i := range paths {
					paths[i] = "agent.yaml"
				}

				done := make(chan struct{})

				go func() {
					defer close(done)

					results, err := l.LintAll(ctx, paths, workers)
					assert.ErrorIs(t, err, context.Canceled)
					assert.Nil(t, results)
				}()

				for range workers {
					<-entered
				}

				cancel()
				synctest.Wait()

				select {
				case <-done:
					t.Fatal("LintAll returned before its workers finished")
				default:
				}

				close(release)
				<-done

				assert.EqualValues(t, workers, fsys.opens.Load())
			})
		})
	}
}

func TestLintAllCancellationInLastRuleCannotPass(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	fsys := afero.NewMemMapFs()
	require.NoError(t, afero.WriteFile(fsys, "agent.yaml", []byte(good), 0o600))

	calls := 0
	l := lint.New(lint.Options{Fs: fsys, Rules: []rule.Rule{
		callbackRule{Base: rule.NewBase("cancel", "", diag.Error), check: func() {
			calls++

			if calls == 2 {
				cancel()
			}
		}},
	}})

	results, err := l.LintAll(ctx, []string{"agent.yaml", "agent.yaml"}, 1)
	require.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, results)
	assert.Equal(t, 2, calls)
}

func TestCancellationInLastRuleCannotPass(t *testing.T) {
	t.Parallel()

	for _, embedded := range []bool{false, true} {
		t.Run(strconv.FormatBool(embedded), func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			calls := 0
			l := lint.New(lint.Options{Embedded: embedded, Rules: []rule.Rule{
				callbackRule{Base: rule.NewBase("cancel", "", diag.Error), check: func() {
					calls++

					cancel()
				}},
			}})

			src := "service: {}"
			if embedded {
				src = "kind: ConfigMap\ndata:\n  first: |\n    service:\n      pipelines: {}\n" +
					"  second: |\n    service:\n      pipelines: {}\n---\nkind: ConfigMap\ndata:\n" +
					"  third: |\n    service:\n      pipelines: {}\n"
			}

			result := l.Lint(ctx, "agent.yaml", []byte(src))
			assert.Equal(t, lint.Error, result.Status)
			require.ErrorIs(t, result.Err, context.Canceled)
			assert.Equal(t, 1, calls)
		})
	}
}

// TestLintFileReadsTheGivenFs pins that LintFile honours Options.Fs, so a
// caller can lint a config that was never written to disk.
func TestLintFileReadsTheGivenFs(t *testing.T) {
	t.Parallel()

	fsys := afero.NewMemMapFs()

	err := afero.WriteFile(fsys, "/configs/agent.yaml", []byte(good), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	l := newLinter(t, lint.Options{Fs: fsys, MinSeverity: diag.Error})

	if r := l.LintFile(t.Context(), "/configs/agent.yaml"); r.Status != lint.Valid {
		t.Errorf("want valid, got %s: %v %+v", r.Status, r.Err, r.Diagnostics)
	}

	if r := l.LintFile(t.Context(), "/configs/absent.yaml"); r.Status != lint.Error {
		t.Errorf("a missing file should be an error, got %s", r.Status)
	}
}

func TestSummary(t *testing.T) {
	t.Parallel()

	l := newLinter(t, lint.Options{})

	var s lint.Summary
	s.Add(l.Lint(t.Context(), "good.yaml", []byte(good)))
	s.Add(l.Lint(t.Context(), "bad.yaml", []byte(bad)))

	if s.Valid != 1 || s.Invalid != 1 {
		t.Errorf("unexpected summary: %+v", s)
	}

	if !s.Failed() {
		t.Error("a summary with an invalid file should fail")
	}
}

func TestFormatters(t *testing.T) {
	t.Parallel()

	l := newLinter(t, lint.Options{})
	result := l.Lint(t.Context(), "bad.yaml", []byte(bad))

	for _, name := range []string{"text", "json", "junit", "tap", "github"} {
		var buf bytes.Buffer

		f, err := lint.NewFormatter(name, &buf, lint.FormatterOptions{Summary: true})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		err = f.Result(result)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		var s lint.Summary
		s.Add(result)

		err = f.Finish(s)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		if !strings.Contains(buf.String(), "bad.yaml") {
			t.Errorf("%s output does not mention the file:\n%s", name, buf.String())
		}
	}

	_, unknownErr := lint.NewFormatter("xml", &bytes.Buffer{}, lint.FormatterOptions{})
	if unknownErr == nil {
		t.Error("an unknown format should be rejected")
	}
}

func TestTextFormatterQuietOnSuccess(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	f, _ := lint.NewFormatter("text", &buf, lint.FormatterOptions{})

	err := f.Result(lint.Result{Path: "ok.yaml", Status: lint.Valid})
	if err != nil {
		t.Fatal(err)
	}

	if buf.Len() != 0 {
		t.Errorf("a passing file should print nothing, got %q", buf.String())
	}

	buf.Reset()

	f, _ = lint.NewFormatter("text", &buf, lint.FormatterOptions{Verbose: true})

	err = f.Result(lint.Result{Path: "ok.yaml", Status: lint.Valid})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(buf.String(), "valid") {
		t.Errorf("-verbose should report passing files, got %q", buf.String())
	}
}

// runKey marks a context as belonging to one run, which is what the recorder
// below reads back out of the question it is asked.
type runKey struct{}

// askedWith records which run's context a rule's schema question arrived on.
type askedWith struct {
	asked bool
	run   string
}

func (a *askedWith) Versions(ctx context.Context, _ config.Kind, _ string) []string {
	a.asked = true
	a.run, _ = ctx.Value(runKey{}).(string)

	return nil
}

// TestTheRunsContextReachesARulesSchemaQuestion pins where the context lives.
// The index behind Availability is a cache with no run of its own, so the
// context comes from the call being served, not from the index: linting under
// one is what decides which run a lookup belongs to, and cancelling that run is
// what ends a fetch it started.
func TestTheRunsContextReachesARulesSchemaQuestion(t *testing.T) {
	t.Parallel()

	src := strings.Replace(good, "  otlp:\n    protocols:\n      grpc:", "  mycorp_custom:", 1)
	src = strings.Replace(src, "receivers: [otlp]", "receivers: [mycorp_custom]", 1)

	avail := &askedWith{asked: false, run: ""}
	ctx := context.WithValue(t.Context(), runKey{}, "this run")

	newLinter(t, lint.Options{Availability: avail}).Lint(ctx, "x.yaml", []byte(src))

	require.True(t, avail.asked, "an unknown component should have asked which releases ship it")
	assert.Equal(t, "this run", avail.run, "the question should arrive on the linting call's context")
}

func TestVersionIndexFindsRemovedComponents(t *testing.T) {
	t.Parallel()

	idx := lint.NewVersionIndex(repoStore())

	versions := idx.Versions(t.Context(), "exporter", "logging")
	if len(versions) == 0 {
		t.Fatal("the logging exporter should exist in some published release")
	}

	for i := 1; i < len(versions); i++ {
		if schema.Compare(versions[i-1], versions[i]) >= 0 {
			t.Fatalf("versions should read oldest first: %v", versions)
		}
	}

	if len(idx.Versions(t.Context(), "exporter", "definitely_not_a_component")) != 0 {
		t.Error("an unknown component should have no versions")
	}
}

// TestAMissingCheckIntervalIsReportedOnce pins that memory-limiter-config
// stands down where the field schema already marks check_interval required:
// one missing key, one finding, not two about the same line.
func TestAMissingCheckIntervalIsReportedOnce(t *testing.T) {
	t.Parallel()

	src := strings.Replace(good, "    check_interval: 1s\n", "", 1)

	var about []string

	for _, d := range newLinter(t, lint.Options{}).Lint(t.Context(), "x.yaml", []byte(src)).Diagnostics {
		if strings.Contains(d.Message, "check_interval") {
			about = append(about, d.Rule)
		}
	}

	if len(about) != 1 {
		t.Errorf("want one finding about check_interval, got %v", about)
	}
}

// TestFormattersCarryTheDocumentationLink pins that a finding's citation
// survives into the output; a link nobody can see is not a citation.
func TestFormattersCarryTheDocumentationLink(t *testing.T) {
	t.Parallel()

	// A memory_limiter that is present and empty: the collector refuses to
	// start, and upstream's README is what says so.
	src := strings.Replace(good, "    check_interval: 1s\n    limit_mib: 512\n    spike_limit_mib: 128\n", "", 1)
	result := newLinter(t, lint.Options{}).Lint(t.Context(), "x.yaml", []byte(src))

	const docs = "processor/memorylimiterprocessor/README.md"

	cited := false

	for _, d := range result.Diagnostics {
		if strings.Contains(d.Docs, docs) {
			cited = true
		}
	}

	if !cited {
		t.Fatalf("no finding cited the processor's README: %+v", result.Diagnostics)
	}

	for _, name := range []string{"text", "json", "github"} {
		var buf bytes.Buffer

		f, err := lint.NewFormatter(name, &buf, lint.FormatterOptions{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		err = f.Result(result)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		err = f.Finish(lint.Summary{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		if !strings.Contains(buf.String(), docs) {
			t.Errorf("%s output drops the link:\n%s", name, buf.String())
		}
	}
}

func TestNewOwnsRuleSliceAndSeverities(t *testing.T) {
	t.Parallel()

	rules := []rule.Rule{servicerequired.New()}
	severities := map[string]diag.Severity{"service-required": diag.Warning}
	linter := lint.New(lint.Options{
		Rules: rules, Severities: severities, IgnoreMissingSchemas: true,
	})
	assert.NotContains(t, severities, "unknown-component", "New must not modify caller policy")

	rules[0] = nil
	severities["service-required"] = diag.Off
	returned := linter.Rules()
	returned[0] = nil

	result := linter.Lint(t.Context(), "request.yaml", []byte("{}"))
	require.Len(t, result.Diagnostics, 1)
	assert.Equal(t, "service-required", result.Diagnostics[0].Rule)
	assert.Equal(t, diag.Warning, result.Diagnostics[0].Severity)
}
