package lint

import (
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

const (
	coverageComplete   = "complete"
	coveragePartial    = "partial"
	coverageNotChecked = "not_checked"
)

// Coverage describes component field-schema checks and unresolved runtime values.
// It is independent of validity and never guarantees Collector startup.
type Coverage struct {
	// Status is complete, partial, or not_checked (no schema or field rules).
	// Complete is relative to FieldRules, not every possible Collector check.
	Status           string         `json:"status"`
	CollectorVersion string         `json:"collectorVersion"`
	Distribution     string         `json:"distribution"`
	FieldRules       []string       `json:"fieldRules"`
	Skipped          []CoverageSkip `json:"skipped,omitempty"`
}

// CoverageSkip names a component or field whose validation was unavailable.
// A component or container path covers its descendants too. Values are omitted.
type CoverageSkip struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
	// Block identifies the ConfigMap name/data key in embedded mode.
	Block string `json:"block,omitempty"`
}

func (l *Linter) coverage(ctx *rule.Context) *Coverage {
	coverage := &Coverage{
		Status: coverageComplete, CollectorVersion: ctx.Schema.CollectorVersion,
		Distribution: ctx.Schema.Distribution, FieldRules: []string{}, Skipped: nil,
	}
	if coverage.Distribution == "" && ctx.Schema.Count() > 0 {
		coverage.Distribution = schema.DefaultDistribution
	}

	for _, r := range l.rules {
		if slices.Contains([]string{"unknown-field", "required-field", "invalid-value", "deprecated-field"}, r.Name()) &&
			l.SeverityFor(r) != diag.Off {
			coverage.FieldRules = append(coverage.FieldRules, r.Name())
		}
	}

	if ctx.SchemaReady() {
		rule.FieldWalker{
			Ctx: ctx, OnUnknown: nil, OnRequired: nil, OnInvalid: nil, OnDeprecated: nil,
			OnSkipped: func(path, reason string) {
				coverage.Skipped = append(coverage.Skipped, CoverageSkip{Path: path, Reason: reason, Block: ""})
			},
		}.WalkComponents()
	} else {
		coverage.Skipped = append(coverage.Skipped, CoverageSkip{Path: "", Reason: "missing_schema", Block: ""})
	}

	coverage.runtimeValues(ctx.File.Root, "", 0)

	if len(coverage.Skipped) > 0 {
		coverage.Status = coveragePartial
	}

	if !ctx.SchemaReady() || len(coverage.FieldRules) == 0 {
		coverage.Status = coverageNotChecked
	}

	return coverage
}

func (c *Coverage) runtimeValues(node *yaml.Node, path string, depth int) {
	node = rule.ResolveAlias(node)
	if node == nil {
		return
	}

	// shortcut: cap runtime scanning at MaxSettingsDepth; raise it when real configs need deeper coverage.
	if depth > rule.MaxSettingsDepth || node.Kind == yaml.AliasNode {
		c.Skipped = append(c.Skipped, CoverageSkip{Path: path, Reason: "unresolved_structure", Block: ""})

		return
	}

	switch node.Kind {
	case yaml.ScalarNode:
		if rule.HasExpansion(node.Value) {
			c.Skipped = append(c.Skipped, CoverageSkip{Path: path, Reason: "runtime_value", Block: ""})
		}
	case yaml.MappingNode:
		for _, entry := range rule.MapEntries(node, path) {
			c.runtimeValues(entry.Node, entry.Path, depth+1)
		}
	case yaml.SequenceNode:
		for i, item := range node.Content {
			c.runtimeValues(item, rule.IndexPath(path, i), depth+1)
		}
	default:
		// Other node kinds carry no settings.
	}
}

func (c *Coverage) description() string {
	counts := make(map[string]int)
	for _, skipped := range c.Skipped {
		counts[skipped.Reason]++
	}

	parts := []string{}

	for _, reason := range []string{"missing_schema", "open_schema", "runtime_value", "unresolved_structure"} {
		if counts[reason] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[reason], strings.ReplaceAll(reason, "_", " ")+"(s)"))
		}
	}

	if len(c.FieldRules) == 0 {
		parts = append(parts, "no field rules enabled")
	}

	target := "no schema target"
	if c.CollectorVersion != "" {
		target = fmt.Sprintf("Collector %s, distribution %s", c.CollectorVersion, c.Distribution)
	}

	return fmt.Sprintf("validation coverage %s (%s): %s; see --output json for paths",
		c.Status, target, strings.Join(parts, ", "))
}

func addCoverage(r *Result, coverage *Coverage, block string) {
	if coverage == nil {
		return
	}

	for i := range coverage.Skipped {
		coverage.Skipped[i].Block = block
	}

	if r.Coverage == nil {
		r.Coverage = coverage

		return
	}

	r.Coverage.Skipped = append(r.Coverage.Skipped, coverage.Skipped...)
	if coverage.Status != coverageComplete {
		r.Coverage.Status = coverage.Status
	}
}
