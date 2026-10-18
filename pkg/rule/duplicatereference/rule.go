// Package duplicatereference reports a component listed twice in one pipeline
// slot.
package duplicatereference

import (
	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
	"github.com/minuk-dev/otelcol-config-lint/pkg/sets"
)

// New builds the rule.
func New() rule.Rule {
	return duplicateReference{rule.NewBase("duplicate-reference",
		"listing the same component twice in one pipeline slot", diag.Warning)}
}

type duplicateReference struct{ rule.Base }

func (r duplicateReference) Check(ctx *rule.Context) {
	for _, p := range ctx.File.Service.Pipelines {
		for _, slot := range []config.Kind{config.KindReceiver, config.KindProcessor, config.KindExporter} {
			seen := sets.New[config.ID]()

			for _, ref := range p.Refs(slot) {
				if rule.HasExpansion(ref.ID.String()) {
					continue
				}

				if seen.Has(ref.ID) {
					ctx.Report(rule.Finding{
						Node: ref.Node, Path: ref.Path,
						Message: rule.Quote(ref.ID.String()) + " is listed twice in " + p.Key + "." + slot.Section(),
					})
				}

				seen.Insert(ref.ID)
			}
		}
	}
}
