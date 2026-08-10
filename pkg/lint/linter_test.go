package lint_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/lint"
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
		cat, err := repoStore().Load(schema.Latest)
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

	if r := l.Lint("good.yaml", []byte(good)); r.Status != lint.Valid {
		t.Errorf("want valid, got %s: %+v", r.Status, r.Diagnostics)
	}

	if r := l.Lint("bad.yaml", []byte(bad)); r.Status != lint.Invalid {
		t.Errorf("want invalid, got %s", r.Status)
	}

	if r := l.LintFile(filepath.Join(t.TempDir(), "nope.yaml")); r.Status != lint.Error {
		t.Errorf("a missing file should be an error, got %s", r.Status)
	}
}

func TestSyntaxErrorIsADiagnosticNotAFailure(t *testing.T) {
	t.Parallel()

	l := newLinter(t, lint.Options{})

	r := l.Lint("broken.yaml", []byte("receivers:\n  otlp: [1, 2\n"))
	if r.Status != lint.Invalid {
		t.Fatalf("want invalid, got %s", r.Status)
	}

	if len(r.Diagnostics) != 1 || r.Diagnostics[0].Rule != "yaml-syntax" {
		t.Errorf("want a yaml-syntax diagnostic, got %+v", r.Diagnostics)
	}
}

func TestMinSeverityFilters(t *testing.T) {
	t.Parallel()

	all := newLinter(t, lint.Options{MinSeverity: diag.Info}).Lint("x.yaml", []byte(bad))

	errsOnly := newLinter(t, lint.Options{MinSeverity: diag.Error}).Lint("x.yaml", []byte(bad))
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
	if r := newLinter(t, lint.Options{}).Lint("x.yaml", []byte(src)); r.Status != lint.Valid {
		t.Errorf("warnings should not fail by default: %+v", r.Diagnostics)
	}

	if r := newLinter(t, lint.Options{FailOn: diag.Warning}).Lint("x.yaml", []byte(src)); r.Status != lint.Invalid {
		t.Error("-fail-on warning should fail")
	}
}

func TestDisabledRule(t *testing.T) {
	t.Parallel()

	l := newLinter(t, lint.Options{Severities: map[string]diag.Severity{"empty-pipeline": diag.Off}})
	for _, d := range l.Lint("x.yaml", []byte(bad)).Diagnostics {
		if d.Rule == "empty-pipeline" {
			t.Fatal("a disabled rule reported anyway")
		}
	}
}

func TestIgnoreMissingSchemasSilencesUnknownComponents(t *testing.T) {
	t.Parallel()

	src := strings.Replace(good, "  otlp:\n    protocols:\n      grpc:", "  mycorp_custom:", 1)
	src = strings.Replace(src, "receivers: [otlp]", "receivers: [mycorp_custom]", 1)

	if r := newLinter(t, lint.Options{}).Lint("x.yaml", []byte(src)); r.Status != lint.Invalid {
		t.Error("an unknown component should fail by default")
	}

	r := newLinter(t, lint.Options{IgnoreMissingSchemas: true}).Lint("x.yaml", []byte(src))
	if r.Status != lint.Valid {
		t.Errorf("want valid, got %s: %+v", r.Status, r.Diagnostics)
	}
}

func TestLintAllVisitsEveryFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	names := []string{"a.yaml", "b.yaml", "c.yaml", "d.yaml"}
	paths := make([]string, 0, len(names))

	for _, name := range names {
		p := filepath.Join(dir, name)

		err := os.WriteFile(p, []byte(good), 0o600)
		if err != nil {
			t.Fatal(err)
		}

		paths = append(paths, p)
	}

	seen := map[string]bool{}
	for r := range newLinter(t, lint.Options{}).LintAll(paths, 3) {
		seen[r.Path] = true
	}

	if len(seen) != len(paths) {
		t.Errorf("want %d results, got %d", len(paths), len(seen))
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

	if r := l.LintFile("/configs/agent.yaml"); r.Status != lint.Valid {
		t.Errorf("want valid, got %s: %v %+v", r.Status, r.Err, r.Diagnostics)
	}

	if r := l.LintFile("/configs/absent.yaml"); r.Status != lint.Error {
		t.Errorf("a missing file should be an error, got %s", r.Status)
	}
}

func TestSummary(t *testing.T) {
	t.Parallel()

	l := newLinter(t, lint.Options{})

	var s lint.Summary
	s.Add(l.Lint("good.yaml", []byte(good)))
	s.Add(l.Lint("bad.yaml", []byte(bad)))

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
	result := l.Lint("bad.yaml", []byte(bad))

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

func TestVersionIndexFindsRemovedComponents(t *testing.T) {
	t.Parallel()

	idx := lint.NewVersionIndex(repoStore())

	versions := idx.Versions("exporter", "logging")
	if len(versions) == 0 {
		t.Fatal("the logging exporter should exist in some published release")
	}

	for i := 1; i < len(versions); i++ {
		if schema.Compare(versions[i-1], versions[i]) >= 0 {
			t.Fatalf("versions should read oldest first: %v", versions)
		}
	}

	if len(idx.Versions("exporter", "definitely_not_a_component")) != 0 {
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

	for _, d := range newLinter(t, lint.Options{}).Lint("x.yaml", []byte(src)).Diagnostics {
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
	result := newLinter(t, lint.Options{}).Lint("x.yaml", []byte(src))

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
