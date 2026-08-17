// Package signalsupport reports a component used for a signal it does not
// handle.
package signalsupport

import (
	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// New builds the rule.
func New() rule.Rule {
	return signalSupport{rule.NewBase("signal-support",
		"a component used in a pipeline whose signal it does not support", diag.Error)}
}

type signalSupport struct{ rule.Base }

func (r signalSupport) Check(ctx *rule.Context) {
	if !ctx.SchemaReady() {
		return
	}

	for _, p := range ctx.File.Service.Pipelines {
		if !rule.IsSignal(p.Signal) {
			continue // invalid-pipeline-key reports this
		}

		for _, slot := range []config.Kind{config.KindReceiver, config.KindProcessor, config.KindExporter} {
			for _, ref := range p.Refs(slot) {
				decl, declared := ctx.Index.Resolve(slot, ref.ID)
				if !declared {
					continue // undefined-reference reports this
				}

				comp, known := ctx.Schema.Lookup(decl.Kind, ref.ID.Type)
				if !known {
					continue // unknown-component reports this
				}

				if supports(comp, decl.Kind, slot, p.Signal) {
					continue
				}

				ctx.Report(rule.Finding{
					Node: ref.Node, Path: ref.Path,
					Message: string(decl.Kind) + " " + rule.Quote(ref.ID.Type) + " does not support " +
						string(p.Signal) + " in pipeline " + rule.Quote(p.Key),
					Hint: "it supports: " + comp.SignalList(),
				})
			}
		}
	}
}

// supports reports whether a component can sit in a pipeline slot for a signal.
// Connectors are directional: the exporter side consumes the pipeline's signal,
// the receiver side produces it.
func supports(comp *schema.Component, declKind, slot config.Kind, s config.Signal) bool {
	if declKind != config.KindConnector {
		return comp.Supports(s)
	}

	switch slot {
	case config.KindReceiver:
		return comp.SupportsAsReceiver(s)
	case config.KindExporter:
		return comp.SupportsAsExporter(s)
	default:
		return comp.Supports(s)
	}
}
