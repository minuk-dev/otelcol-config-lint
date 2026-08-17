// Package unknowntoplevelkey reports a top-level key the collector rejects.
package unknowntoplevelkey

import (
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// topLevelKeys are the only keys a config may write at the root.
//
// It returns a fresh slice so callers cannot alter what everyone else sees.
func topLevelKeys() []string {
	return []string{"connectors", "exporters", "extensions", "processors", "receivers", "service"}
}

// New builds the rule.
func New() rule.Rule {
	return unknownTopLevelKey{rule.NewBase("unknown-top-level-key",
		"top-level keys other than the component sections and service are rejected by the collector",
		diag.Error)}
}

type unknownTopLevelKey struct{ rule.Base }

func (r unknownTopLevelKey) Check(ctx *rule.Context) {
	for _, e := range ctx.File.Unknown {
		ctx.Report(rule.Finding{
			Node: e.KeyNode, Path: e.Path,
			Message: "unknown top-level key " + rule.Quote(e.Key),
			Hint: "valid top-level keys: " + rule.List(topLevelKeys()) +
				rule.Suggest(e.Key, topLevelKeys()),
		})
	}
}
