package rule

import (
	"strings"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
)

// referenceRules check how the service block wires components together.
func referenceRules() []Rule {
	return []Rule{
		undefinedReference{base{"undefined-reference",
			"the service block may only reference components declared in the config", diag.Error}},
		unusedComponent{base{"unused-component",
			"a declared component that no pipeline references is never instantiated", diag.Warning}},
		duplicateReference{base{"duplicate-reference",
			"listing the same component twice in one pipeline slot", diag.Warning}},
		connectorWiring{base{"connector-wiring",
			"a connector must be an exporter in one pipeline and a receiver in another", diag.Error}},
	}
}

type undefinedReference struct{ base }

func (r undefinedReference) Check(ctx *Context) {
	f := ctx.File

	for _, ref := range f.Service.Extensions {
		if _, ok := ctx.Index.Declared(config.KindExtension, ref.ID); ok {
			continue
		}

		ctx.Report(Finding{
			Node: ref.Node, Path: "service." + ref.Path,
			Message: "service.extensions references " + quote(ref.ID.String()) + " which is not declared under extensions",
			Hint:    misplacedHint(ctx, ref.ID, config.KindExtension),
		})
	}

	for _, p := range f.Service.Pipelines {
		for _, slot := range []config.Kind{config.KindReceiver, config.KindProcessor, config.KindExporter} {
			for _, ref := range p.Refs(slot) {
				if _, ok := ctx.Index.Resolve(slot, ref.ID); ok {
					continue
				}

				ctx.Report(Finding{
					Node: ref.Node, Path: "service." + ref.Path,
					Message: "pipeline " + quote(p.Key) + " references " + string(slot) + " " +
						quote(ref.ID.String()) + " which is not declared under " + slot.Section(),
					Hint: misplacedHint(ctx, ref.ID, slot),
				})
			}
		}
	}
}

// misplacedHint explains a reference that points at a component declared under
// a different section, which is the usual cause of an undefined reference.
func misplacedHint(ctx *Context, id config.ID, want config.Kind) string {
	if k, ok := ctx.Index.KindOf(id); ok && k != want {
		return quote(id.String()) + " is declared under " + k.Section() +
			"; it cannot be used as a " + string(want)
	}

	if declared := declaredIDs(ctx, want); len(declared) > 0 {
		return "declared " + want.Section() + ": " + list(declared) + suggest(id.String(), declared)
	}

	return "no " + want.Section() + " are declared in this config"
}

func declaredIDs(ctx *Context, k config.Kind) []string {
	sec := ctx.File.Sections[k]
	if sec == nil {
		return nil
	}

	out := make([]string, 0, len(sec.Components))
	for _, c := range sec.Components {
		out = append(out, c.ID.String())
	}

	return out
}

type unusedComponent struct{ base }

func (r unusedComponent) Check(ctx *Context) {
	// Without a service block every component is unused; serviceRequired
	// already says so, and repeating it per component is only noise.
	if ctx.File.Service.Node == nil {
		return
	}

	for _, kind := range config.Kinds() {
		sec := ctx.File.Sections[kind]
		if sec == nil {
			continue
		}

		for _, c := range sec.Components {
			if ctx.Index.Used(kind, c.ID) {
				continue
			}

			where := "no pipeline"
			if kind == config.KindExtension {
				where = "service.extensions"
			}

			ctx.Report(Finding{
				Node: c.KeyNode, Path: kind.Section() + "." + c.ID.String(),
				Message: string(kind) + " " + quote(c.ID.String()) + " is declared but referenced by " + where,
				Hint:    "remove it, or reference it so it actually runs",
			})
		}
	}
}

type duplicateReference struct{ base }

func (r duplicateReference) Check(ctx *Context) {
	for _, p := range ctx.File.Service.Pipelines {
		for _, slot := range []config.Kind{config.KindReceiver, config.KindProcessor, config.KindExporter} {
			seen := map[config.ID]bool{}
			for _, ref := range p.Refs(slot) {
				if seen[ref.ID] {
					ctx.Report(Finding{
						Node: ref.Node, Path: "service." + ref.Path,
						Message: quote(ref.ID.String()) + " is listed twice in " + p.Key + "." + slot.Section(),
					})
				}

				seen[ref.ID] = true
			}
		}
	}
}

type connectorWiring struct{ base }

func (r connectorWiring) Check(ctx *Context) {
	sec := ctx.File.Sections[config.KindConnector]
	if sec == nil || ctx.File.Service.Node == nil {
		return
	}

	for _, c := range sec.Components {
		asReceiver, asExporter := ctx.Index.ConnectorPipelines(c.ID)
		if len(asReceiver) == 0 && len(asExporter) == 0 {
			continue // unusedComponent covers this
		}

		path := "connectors." + c.ID.String()

		switch {
		case len(asExporter) == 0:
			ctx.Report(Finding{
				Node: c.KeyNode, Path: path,
				Message: "connector " + quote(c.ID.String()) +
					" is used as a receiver but never as an exporter, so it gets no input",
				Hint: "list it under exporters in the pipeline that should feed it",
			})
		case len(asReceiver) == 0:
			ctx.Report(Finding{
				Node: c.KeyNode, Path: path,
				Message: "connector " + quote(c.ID.String()) +
					" is used as an exporter but never as a receiver, so its output is dropped",
				Hint: "list it under receivers in the pipeline that should consume it",
			})
		}

		for _, p := range asExporter {
			for _, q := range asReceiver {
				if p.Key == q.Key {
					ctx.Report(Finding{
						Node: c.KeyNode, Path: path,
						Message: "connector " + quote(c.ID.String()) + " is both a receiver and an exporter of pipeline " +
							quote(p.Key) + ", which forms a loop",
					})
				}
			}
		}
	}
}

func list(items []string) string {
	const maxItems = 8

	if len(items) > maxItems {
		items = append(items[:maxItems:maxItems], "...")
	}

	return strings.Join(items, ", ")
}
