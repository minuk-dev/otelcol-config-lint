package rule

import (
	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// ExporterFields returns the targeted release's field schema for an exporter
// type, or nil when there is none to consult.
func ExporterFields(ctx *Context, typ string) *schema.Field {
	if !ctx.SchemaReady() {
		return nil
	}

	comp, ok := ctx.Schema.Lookup(config.KindExporter, typ)
	if !ok {
		return nil
	}

	return comp.Fields
}

// DescribedExporter reports whether the field schema describes the exporter
// type's settings at all. Where it does, a queue batch nobody wrote is a queue
// batch that does not run, and a queue the type has no field for is a queue it
// does not have. Where it does not, neither claim holds, and that covers four
// shapes: no schema was resolved at all, the type is not in it, the type is in
// it with its settings left open, and the type is in it with no fields -- which
// reads as "nothing could be resolved for this component" rather than "this
// component has no settings", since the datadog exporter, which has a queue and
// a great many other settings, sits in that bucket next to nop.
func DescribedExporter(ctx *Context, typ string) bool {
	fields := ExporterFields(ctx, typ)

	return fields != nil && !fields.Open && len(fields.Children) > 0
}

// AcceptsQueue reports whether the exporter type has a sending queue at all.
// Plenty do not -- debug and nop among them -- and telling them they lose a
// queue they never had is worse than saying nothing.
//
// The field schema is the authority wherever it describes the type's settings,
// which is what keeps debug quiet. Where it does not -- see DescribedExporter
// for the shapes that covers -- a queue the config writes is the only evidence
// there is. A queue written under an exporter that really has none is then
// reported here as well as by unknown-field, which is the price of not staying
// silent about datadog.
func AcceptsQueue(ctx *Context, typ string, written bool) bool {
	if !DescribedExporter(ctx, typ) {
		return written
	}

	_, held := ExporterFields(ctx, typ).Children[SendingQueueKey]

	return held
}

// HasProcessorType reports whether a pipeline lists a processor of the given
// type, whatever the instance is named.
func HasProcessorType(p config.Pipeline, typ string) bool {
	for _, ref := range p.Processors {
		if ref.ID.Type == typ {
			return true
		}
	}

	return false
}
