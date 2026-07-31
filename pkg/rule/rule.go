// Package rule defines the linter's checks and the registry that holds them.
package rule

import (
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/catalog"
	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
)

// Rule is a single check run against a parsed config.
type Rule interface {
	// Name is the stable identifier used to enable, disable or re-level the
	// rule from the command line or a config file.
	Name() string
	// Description is a one-line explanation shown by "otelcol-config-lint rules".
	Description() string
	// Severity is the level the rule reports at unless overridden.
	Severity() diag.Severity
	// Check inspects the config and reports findings through ctx.
	Check(ctx *Context)
}

// Context carries the config under test and collects findings for one rule.
type Context struct {
	// File is the parsed config being checked.
	File *config.File
	// Catalog describes the collector release being targeted. It is never nil,
	// but may be empty when no catalog could be resolved.
	Catalog *catalog.Catalog
	// Index resolves references between the service block and declarations.
	Index *Index
	// Avail reports which other releases ship a component. It may be nil.
	Avail Availability
	// Strict makes lenient checks report at their strict severity, mirroring
	// kubeconform's -strict flag.
	Strict bool

	rule     Rule
	severity diag.Severity
	out      *diag.Diagnostics
}

// Finding is a single problem a rule wants to report.
type Finding struct {
	// Node anchors the finding to a position; nil reports against the file.
	Node *yaml.Node
	// Path is the dotted YAML path, e.g. "service.pipelines.traces".
	Path string
	// Message states the problem. It should read as a sentence fragment
	// starting lowercase, without a trailing period.
	Message string
	// Hint optionally suggests a fix.
	Hint string
	// Severity overrides the rule's default for this one finding.
	Severity diag.Severity
}

// Report records a finding.
func (c *Context) Report(f Finding) {
	sev := c.severity
	if f.Severity != "" && c.severity != diag.Off {
		sev = f.Severity
	}

	*c.out = append(*c.out, diag.Diagnostic{
		Rule:     c.rule.Name(),
		Severity: sev,
		Message:  f.Message,
		Position: c.File.Pos(f.Node),
		Path:     f.Path,
		Hint:     f.Hint,
	})
}

// Reportf records a finding with a formatted message.
func (c *Context) Reportf(n *yaml.Node, path, format string, args ...any) {
	c.Report(Finding{Node: n, Path: path, Message: fmt.Sprintf(format, args...)})
}

// Run executes a rule against a config and returns what it found. Severity is
// the level the rule reports at; passing diag.Off skips the rule.
func Run(r Rule, ctx Context, severity diag.Severity) diag.Diagnostics {
	if severity == diag.Off {
		return nil
	}

	var out diag.Diagnostics

	ctx.rule, ctx.severity, ctx.out = r, severity, &out
	r.Check(&ctx)

	return out
}

// All returns every built-in rule, sorted by name. The set is built on each
// call rather than registered into package state, so nothing depends on
// initialisation order and callers can safely keep or filter the result.
func All() []Rule {
	rules := slices.Concat(
		structureRules(),
		referenceRules(),
		componentRules(),
		fieldRules(),
		practiceRules(),
	)
	slices.SortFunc(rules, func(a, b Rule) int { return strings.Compare(a.Name(), b.Name()) })

	return rules
}

// Lookup returns a built-in rule by name.
func Lookup(name string) (Rule, bool) {
	for _, r := range All() {
		if r.Name() == name {
			return r, true
		}
	}

	return nil, false
}

// base is the common implementation detail of every built-in rule.
type base struct {
	name string
	desc string
	sev  diag.Severity
}

func (b base) Name() string            { return b.name }
func (b base) Description() string     { return b.desc }
func (b base) Severity() diag.Severity { return b.sev }
