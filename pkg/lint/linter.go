// Package lint runs the rule set over collector config files.
package lint

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/spf13/afero"
	"gopkg.in/yaml.v3"

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
	// Error means an execution failure prevented checking the file.
	Error Status = "error"
	// Skipped means the file was not a config the linter handles.
	Skipped Status = "skipped"
)

// Result is the outcome of linting one file.
type Result struct {
	Path        string           `json:"filename"`
	Status      Status           `json:"status"`
	Diagnostics diag.Diagnostics `json:"diagnostics,omitempty"`
	// Coverage describes the checks that could run, independently of Status.
	Coverage *Coverage `json:"coverage,omitempty"`
	// Err explains Error results and may also accompany Invalid YAML syntax.
	// Inspect Status to distinguish invalid input from an execution failure.
	Err error `json:"-"`
}

// Message renders the error text for a failed file.
func (r Result) Message() string {
	if r.Err != nil {
		return r.Err.Error()
	}

	return ""
}

// Options configures a Linter. New copies Severities and the Rules slice;
// schemas, rule instances and callbacks remain shared and must be safe for
// concurrent use without configuration changes during linting.
type Options struct {
	// Schema describes the collector release to check against. A nil schema or
	// one with no components runs structural checks only; schema-dependent
	// rules stay silent.
	Schema *schema.Schema
	// Fs is the filesystem LintFile reads from. A nil Fs means the real one.
	Fs afero.Fs
	// Availability lets diagnostics mention other releases. May be nil.
	Availability Availability
	// Distributions lets diagnostics mention other distributions. May be nil.
	Distributions Distributions
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
	// Embedded checks collector configs in Kubernetes ConfigMap data blocks.
	Embedded bool
}

// Availability reports which releases ship a component type. It is what
// rule.Availability is bound from, and it takes a context because answering
// can mean walking a remote registry: that fetch belongs to the run asking the
// question, not to whoever built the index. VersionIndex implements it.
type Availability interface {
	Versions(ctx context.Context, k config.Kind, typ string) []string
}

// Distributions reports which distributions of the targeted release ship a
// component type, on the same terms as Availability. DistributionIndex
// implements it.
type Distributions interface {
	Distributions(ctx context.Context, k config.Kind, typ string) []string
}

// Linter checks config files against a rule set. It may be shared by concurrent
// callers when the dependencies described in Options are safe to share.
type Linter struct {
	opts  Options
	rules []rule.Rule
}

// New builds a Linter from resolved dependencies, defaulting to all built-in
// rules. It performs no I/O or policy validation. Use Prepare to load a schema
// and validate built-in rule selection and severities.
func New(opts Options) *Linter {
	opts.Severities = maps.Clone(opts.Severities)
	opts.Rules = slices.Clone(opts.Rules)

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

// Rules returns a copy of the rule slice in execution order. Rule instances
// remain shared and their configuration must not be mutated during linting.
func (l *Linter) Rules() []rule.Rule { return slices.Clone(l.rules) }

// SeverityFor returns the level a rule will report at.
func (l *Linter) SeverityFor(r rule.Rule) diag.Severity {
	if s, ok := l.opts.Severities[r.Name()]; ok {
		return s
	}

	return r.Severity()
}

// LintFile reads and checks a single config file.
func (l *Linter) LintFile(ctx context.Context, path string) Result {
	err := ctx.Err()
	if err != nil {
		return Result{Path: path, Status: Error, Err: err}
	}

	src, err := afero.ReadFile(l.fs(), path)
	if err != nil {
		return Result{Path: path, Status: Error, Err: err}
	}

	return l.Lint(ctx, path, src)
}

// LintReader checks config read from r, reporting it under name.
func (l *Linter) LintReader(ctx context.Context, name string, r io.Reader) Result {
	err := ctx.Err()
	if err != nil {
		return Result{Path: name, Status: Error, Err: err}
	}

	src, err := io.ReadAll(r)
	if err != nil {
		return Result{Path: name, Status: Error, Err: err}
	}

	return l.Lint(ctx, name, src)
}

// Lint checks config source that was read from path. Cancellation is checked
// between parsing and rules; schema lookups also run under this context.
func (l *Linter) Lint(ctx context.Context, path string, src []byte) Result {
	err := ctx.Err()
	if err != nil {
		return Result{Path: path, Status: Error, Err: err}
	}

	if l.opts.Embedded {
		return l.lintEmbedded(ctx, path, src)
	}

	return l.lintConfig(ctx, path, src)
}

// LintAll checks paths concurrently and waits for every worker to finish.
// On success it returns one result per input, in input order, including duplicate
// paths. File errors stay in Result. Cancellation stops subsequent work, discards
// all results and returns an error wrapping ctx.Err(). Empty input returns nil,
// nil unless canceled.
// Worker counts below one use one worker; counts above len(paths) are capped.
func (l *Linter) LintAll(ctx context.Context, paths []string, n int) ([]Result, error) {
	err := ctx.Err()
	if err != nil {
		return nil, fmt.Errorf("lint cancelled: %w", err)
	}

	if len(paths) == 0 {
		return nil, nil
	}

	n = min(max(n, 1), len(paths))
	results := make([]Result, len(paths))
	in := make(chan int)

	var workers sync.WaitGroup

	for range n {
		workers.Go(func() {
			for i := range in {
				if ctx.Err() != nil {
					return
				}

				results[i] = l.LintFile(ctx, paths[i])
			}
		})
	}

dispatch:
	for i := range paths {
		if ctx.Err() != nil {
			break
		}

		select {
		case <-ctx.Done():
			break dispatch
		case in <- i:
		}
	}

	close(in)
	workers.Wait()

	err = ctx.Err()
	if err != nil {
		return nil, fmt.Errorf("lint cancelled: %w", err)
	}

	return results, nil
}

func (l *Linter) lintConfig(ctx context.Context, path string, src []byte) Result {
	f, err := config.Parse(path, src)

	if ctx.Err() != nil {
		return Result{Path: path, Status: Error, Err: ctx.Err()}
	}

	if err != nil {
		var syn *config.SyntaxError
		if ok := asSyntaxError(err, &syn); ok {
			return Result{Path: path, Status: Invalid, Diagnostics: syn.Diagnostics(), Err: err}
		}

		return Result{Path: path, Status: Error, Err: err}
	}

	ruleCtx := rule.Context{
		File:   f,
		Schema: l.opts.Schema,
		Index:  rule.NewIndex(f, l.opts.Schema),
		Strict: l.opts.Strict,
		Env:    l.environment(path),
	}

	// The context is bound here rather than held by whatever answers. An index
	// is a cache with no run of its own, so the run a lookup belongs to is the
	// one asking, and the rule asks without knowing a context was involved.
	if l.opts.Availability != nil {
		ruleCtx.Avail = rule.AvailabilityFunc(func(k config.Kind, typ string) []string {
			return l.opts.Availability.Versions(ctx, k, typ)
		})
	}

	if l.opts.Distributions != nil {
		ruleCtx.Dists = rule.DistributionsFunc(func(k config.Kind, typ string) []string {
			return l.opts.Distributions.Distributions(ctx, k, typ)
		})
	}

	res := Result{Path: path, Status: Valid, Coverage: l.coverage(&ruleCtx)}

	for _, r := range l.rules {
		if ctx.Err() != nil {
			break
		}

		for _, d := range rule.Run(r, ruleCtx, l.SeverityFor(r)) {
			if d.Severity.AtLeast(l.opts.FailOn) {
				res.Status = Invalid
			}

			if d.Severity.AtLeast(l.opts.MinSeverity) {
				res.Diagnostics = append(res.Diagnostics, d)
			}
		}
	}

	if ctx.Err() != nil {
		return Result{Path: path, Status: Error, Err: ctx.Err()}
	}

	res.Diagnostics.Sort()

	return res
}

// lintEmbedded keeps one result per input file, including files with several
// ConfigMaps. The inner parser and rules remain the same as for a plain config.
func (l *Linter) lintEmbedded(ctx context.Context, path string, src []byte) Result {
	res := Result{Path: path, Status: Skipped}
	lines := strings.Split(string(src), "\n")
	dec := yaml.NewDecoder(strings.NewReader(string(src)))

	for {
		err := ctx.Err()
		if err != nil {
			return Result{Path: path, Status: Error, Err: err}
		}

		var doc yaml.Node

		err = dec.Decode(&doc)

		if ctx.Err() != nil {
			return Result{Path: path, Status: Error, Err: ctx.Err()}
		}

		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return Result{Path: path, Status: Error, Err: err}
		}

		if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
			continue
		}

		root := doc.Content[0]
		kind := mappingValue(root, "kind")

		if kind == nil || kind.Value != "ConfigMap" {
			continue
		}

		data := mappingValue(root, "data")

		if data == nil || data.Kind != yaml.MappingNode {
			continue
		}

		l.lintConfigMap(ctx, path, lines, root, data, &res)
	}

	res.Diagnostics.Sort()

	return res
}

func (l *Linter) lintConfigMap(ctx context.Context, path string, lines []string, root, data *yaml.Node, res *Result) {
	name := "<unnamed>"

	if metadata := mappingValue(root, "metadata"); metadata != nil {
		if n := mappingValue(metadata, "name"); n != nil && n.Value != "" {
			name = n.Value
		}
	}

	for i := 0; i+1 < len(data.Content); i += 2 {
		if ctx.Err() != nil {
			return
		}

		key, block := data.Content[i], data.Content[i+1]

		if block.Kind != yaml.ScalarNode || block.Style&yaml.LiteralStyle == 0 || !collectorBlock(block.Value) {
			continue
		}

		baseline := blockIndent(lines, block)
		checked := l.lintConfig(ctx, path, []byte(block.Value))

		if res.Status == Skipped || checked.Status == Invalid {
			res.Status = checked.Status
		}

		addCoverage(res, checked.Coverage, name+"/"+key.Value)

		for _, d := range checked.Diagnostics {
			if d.Position.Line > 0 {
				d.Position.Line += block.Line
				if d.Position.Column > 0 {
					d.Position.Column += baseline
				}
			}

			d.Message = "(" + name + "/" + key.Value + ") " + d.Message
			res.Diagnostics = append(res.Diagnostics, d)
		}
	}
}

func collectorBlock(src string) bool {
	sections := []string{"receivers", "exporters", "processors", "connectors", "extensions"}

	var doc yaml.Node

	err := yaml.Unmarshal([]byte(src), &doc)

	if err == nil && len(doc.Content) > 0 {
		root := doc.Content[0]

		if root.Kind != yaml.MappingNode {
			return false
		}

		service := mappingValue(root, "service")

		for _, key := range sections {
			if mappingValue(root, key) != nil {
				return true
			}
		}

		return mappingValue(service, "pipelines") != nil
	}

	// A broken collector config still needs its syntax finding. These key
	// prefixes identify it without pretending the invalid YAML has a node tree.
	for line := range strings.SplitSeq(src, "\n") {
		for _, key := range sections {
			if strings.HasPrefix(line, key+":") {
				return true
			}
		}
	}

	return false
}

func mappingValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}

	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}

	return nil
}

func blockIndent(lines []string, block *yaml.Node) int {
	for i, line := range strings.Split(block.Value, "\n") {
		if strings.TrimSpace(line) == "" || block.Line+i >= len(lines) {
			continue
		}

		if offset := strings.Index(lines[block.Line+i], line); offset >= 0 {
			return offset
		}
	}

	return 0
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
	Valid      int `json:"valid"`
	Invalid    int `json:"invalid"`
	Errors     int `json:"errors"`
	Skipped    int `json:"skipped"`
	Warnings   int `json:"warnings,omitempty"`
	Infos      int `json:"infos,omitempty"`
	Incomplete int `json:"incomplete,omitempty"`
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

	if r.Coverage != nil && r.Coverage.Status != coverageComplete {
		s.Incomplete++
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
