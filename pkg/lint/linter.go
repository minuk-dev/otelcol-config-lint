// Package lint runs the rule set over collector config files.
package lint

import (
	"errors"
	"io"
	"sync"

	"github.com/spf13/afero"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
	"github.com/minuk-dev/otelcol-config-lint/pkg/ruleset"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// Status is the outcome of linting one file.
type Status string

// The outcomes a file can have, mirroring kubeconform's vocabulary.
const (
	// Valid means nothing at or above the failure threshold was found.
	Valid Status = "valid"
	// Invalid means the file was checked and found wanting.
	Invalid Status = "invalid"
	// Error means the file could not be read or parsed at all.
	Error Status = "error"
	// Skipped means the file was not a config the linter handles.
	Skipped Status = "skipped"
)

// Result is the outcome of linting one file.
type Result struct {
	Path        string           `json:"filename"`
	Status      Status           `json:"status"`
	Diagnostics diag.Diagnostics `json:"diagnostics,omitempty"`
	// Err is set when Status is Error.
	Err error `json:"-"`
}

// Message renders the error text for a failed file.
func (r Result) Message() string {
	if r.Err != nil {
		return r.Err.Error()
	}

	return ""
}

// Options configures a Linter.
type Options struct {
	// Schema describes the collector release to check against.
	Schema *schema.Schema
	// Fs is the filesystem LintFile reads from. A nil Fs means the real one.
	Fs afero.Fs
	// Availability lets diagnostics mention other releases. May be nil.
	Availability rule.Availability
	// Distributions lets diagnostics mention other distributions. May be nil.
	Distributions rule.Distributions
	// Rules is the set to run. A nil Rules means every registered rule, which
	// is what a caller with no per-rule settings to apply wants; a caller that
	// has some passes what rule.Configure returned.
	Rules []rule.Rule
	// Severities overrides the level individual rules report at. A value of
	// diag.Off disables the rule.
	Severities map[string]diag.Severity
	// Strict makes lenient rules report as errors, like kubeconform -strict.
	Strict bool
	// IgnoreMissingSchemas keeps components that are absent from the schema
	// from failing the run, for configs using a custom distribution.
	IgnoreMissingSchemas bool
	// Environment resolves the deployment environment of one config file, for
	// the rules that cannot judge a config on its own. It is called from every
	// worker LintAll starts, so it must be safe for concurrent use and must
	// not change once linting has begun. A nil resolver leaves every file's
	// environment unknown, which keeps those rules silent.
	Environment func(path string) rule.Environment
	// MinSeverity drops diagnostics less serious than this level.
	MinSeverity diag.Severity
	// FailOn is the severity at which a file counts as invalid.
	FailOn diag.Severity
}

// Linter checks config files against a rule set.
type Linter struct {
	opts  Options
	rules []rule.Rule
}

// New builds a Linter over every registered rule.
func New(opts Options) *Linter {
	if opts.MinSeverity == "" {
		opts.MinSeverity = diag.Info
	}

	if opts.FailOn == "" {
		opts.FailOn = diag.Error
	}

	if opts.Schema == nil {
		opts.Schema = &schema.Schema{}
	}

	if opts.IgnoreMissingSchemas {
		if _, set := opts.Severities["unknown-component"]; !set {
			if opts.Severities == nil {
				opts.Severities = map[string]diag.Severity{}
			}

			opts.Severities["unknown-component"] = diag.Off
		}
	}

	if opts.Rules == nil {
		opts.Rules = ruleset.All()
	}

	return &Linter{opts: opts, rules: opts.Rules}
}

// Rules returns the rules the linter will run, in name order.
func (l *Linter) Rules() []rule.Rule { return l.rules }

// SeverityFor returns the level a rule will report at.
func (l *Linter) SeverityFor(r rule.Rule) diag.Severity {
	if s, ok := l.opts.Severities[r.Name()]; ok {
		return s
	}

	return r.Severity()
}

// LintFile reads and checks a single config file.
func (l *Linter) LintFile(path string) Result {
	src, err := afero.ReadFile(l.fs(), path)
	if err != nil {
		return Result{Path: path, Status: Error, Err: err}
	}

	return l.Lint(path, src)
}

// LintReader checks config read from r, reporting it under name.
func (l *Linter) LintReader(name string, r io.Reader) Result {
	src, err := io.ReadAll(r)
	if err != nil {
		return Result{Path: name, Status: Error, Err: err}
	}

	return l.Lint(name, src)
}

// Lint checks config source that was read from path.
func (l *Linter) Lint(path string, src []byte) Result {
	f, err := config.Parse(path, src)
	if err != nil {
		var syn *config.SyntaxError
		if ok := asSyntaxError(err, &syn); ok {
			return Result{
				Path: path, Status: Invalid,
				Diagnostics: diag.Diagnostics{syn.Diagnostic()},
				Err:         err,
			}
		}

		return Result{Path: path, Status: Error, Err: err}
	}

	ctx := rule.Context{
		File:   f,
		Schema: l.opts.Schema,
		Index:  rule.NewIndex(f),
		Avail:  l.opts.Availability,
		Dists:  l.opts.Distributions,
		Strict: l.opts.Strict,
		Env:    l.environment(path),
	}

	var found diag.Diagnostics

	for _, r := range l.rules {
		for _, d := range rule.Run(r, ctx, l.SeverityFor(r)) {
			if d.Severity.AtLeast(l.opts.MinSeverity) {
				found = append(found, d)
			}
		}
	}

	found.Sort()

	res := Result{Path: path, Status: Valid, Diagnostics: found}
	for _, d := range found {
		if d.Severity.AtLeast(l.opts.FailOn) {
			res.Status = Invalid

			break
		}
	}

	return res
}

// LintAll checks paths concurrently with up to n workers, sending results in
// completion order. It closes the returned channel when every path is done.
func (l *Linter) LintAll(paths []string, n int) <-chan Result {
	if n < 1 {
		n = 1
	}

	out := make(chan Result, len(paths))
	in := make(chan string)

	var workers sync.WaitGroup

	for range n {
		workers.Go(func() {
			for p := range in {
				out <- l.LintFile(p)
			}
		})
	}

	go func() {
		for _, p := range paths {
			in <- p
		}

		close(in)
		workers.Wait()
		close(out)
	}()

	return out
}

// fs returns the filesystem to read, which is the real one unless the caller
// named another.
func (l *Linter) fs() afero.Fs {
	if l.opts.Fs == nil {
		return afero.NewOsFs()
	}

	return l.opts.Fs
}

// environment resolves where a file is deployed, which is unknown unless the
// caller supplied a resolver.
func (l *Linter) environment(path string) rule.Environment {
	if l.opts.Environment == nil {
		return rule.Environment{}
	}

	return l.opts.Environment(path)
}

// Summary counts results by status and diagnostics by severity.
type Summary struct {
	Valid    int `json:"valid"`
	Invalid  int `json:"invalid"`
	Errors   int `json:"errors"`
	Skipped  int `json:"skipped"`
	Warnings int `json:"warnings,omitempty"`
	Infos    int `json:"infos,omitempty"`
}

// Add folds one result into the summary.
func (s *Summary) Add(r Result) {
	switch r.Status {
	case Valid:
		s.Valid++
	case Invalid:
		s.Invalid++
	case Error:
		s.Errors++
	case Skipped:
		s.Skipped++
	}

	s.Warnings += r.Diagnostics.Count(diag.Warning)
	s.Infos += r.Diagnostics.Count(diag.Info)
}

// Failed reports whether any file was invalid or could not be processed.
func (s *Summary) Failed() bool { return s.Invalid > 0 || s.Errors > 0 }

// asSyntaxError is errors.As specialised to avoid importing errors in the hot
// path signature; it keeps Lint readable.
func asSyntaxError(err error, target **config.SyntaxError) bool {
	se := &config.SyntaxError{}
	if errors.As(err, &se) {
		*target = se

		return true
	}

	return false
}
