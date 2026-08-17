// Package undefinedreference reports a service block naming a component the
// config never declares.
package undefinedreference

import (
	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// New builds the rule.
func New() rule.Rule {
	return undefinedReference{rule.NewBase("undefined-reference",
		"the service block may only reference components declared in the config", diag.Error)}
}

type undefinedReference struct{ rule.Base }

func (r undefinedReference) Check(ctx *rule.Context) {
	f := ctx.File

	for _, ref := range f.Service.Extensions {
		if _, ok := ctx.Index.Declared(config.KindExtension, ref.ID); ok {
			continue
		}

		ctx.Report(rule.Finding{
			Node: ref.Node, Path: ref.Path,
			Message: "service.extensions references " + rule.Quote(ref.ID.String()) +
				" which is not declared under extensions",
			Hint: rule.MisplacedHint(ctx, ref.ID, config.KindExtension),
		})
	}

	for _, p := range f.Service.Pipelines {
		for _, slot := range []config.Kind{config.KindReceiver, config.KindProcessor, config.KindExporter} {
			for _, ref := range p.Refs(slot) {
				if _, ok := ctx.Index.Resolve(slot, ref.ID); ok {
					continue
				}

				ctx.Report(rule.Finding{
					Node: ref.Node, Path: ref.Path,
					Message: "pipeline " + rule.Quote(p.Key) + " references " + string(slot) + " " +
						rule.Quote(ref.ID.String()) + " which is not declared under " + slot.Section(),
					Hint: rule.MisplacedHint(ctx, ref.ID, slot),
				})
			}
		}
	}
}
