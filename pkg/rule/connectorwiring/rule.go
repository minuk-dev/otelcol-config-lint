// Package connectorwiring reports a connector wired to only one side, or to
// both sides of the same pipeline.
package connectorwiring

import (
	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// New builds the rule.
func New() rule.Rule {
	return connectorWiring{rule.NewBase("connector-wiring",
		"a connector must be an exporter in one pipeline and a receiver in another", diag.Error)}
}

type connectorWiring struct{ rule.Base }

func (r connectorWiring) Check(ctx *rule.Context) {
	sec := ctx.File.Sections[config.KindConnector]
	if sec == nil || ctx.File.Service.Node == nil {
		return
	}

	for _, c := range sec.Components {
		asReceiver, asExporter := ctx.Index.ConnectorPipelines(c.ID)
		if len(asReceiver) == 0 && len(asExporter) == 0 {
			continue // unused-component covers this
		}

		r.checkSides(ctx, c, asReceiver, asExporter)
		r.checkLoop(ctx, c, asReceiver, asExporter)
	}
}

// checkSides reports a connector wired to one side only, which leaves it with
// no input or with its output dropped.
func (r connectorWiring) checkSides(
	ctx *rule.Context, c config.Component, asReceiver, asExporter []config.Pipeline,
) {
	path := "connectors." + c.ID.String()

	switch {
	case len(asExporter) == 0:
		ctx.Report(rule.Finding{
			Node: c.KeyNode, Path: path,
			Message: "connector " + rule.Quote(c.ID.String()) +
				" is used as a receiver but never as an exporter, so it gets no input",
			Hint: "list it under exporters in the pipeline that should feed it",
		})
	case len(asReceiver) == 0:
		ctx.Report(rule.Finding{
			Node: c.KeyNode, Path: path,
			Message: "connector " + rule.Quote(c.ID.String()) +
				" is used as an exporter but never as a receiver, so its output is dropped",
			Hint: "list it under receivers in the pipeline that should consume it",
		})
	}
}

// checkLoop reports a connector standing on both sides of one pipeline, which
// feeds its own output back into itself.
func (r connectorWiring) checkLoop(
	ctx *rule.Context, c config.Component, asReceiver, asExporter []config.Pipeline,
) {
	for _, p := range asExporter {
		for _, q := range asReceiver {
			if p.Key == q.Key {
				ctx.Report(rule.Finding{
					Node: c.KeyNode, Path: "connectors." + c.ID.String(),
					Message: "connector " + rule.Quote(c.ID.String()) +
						" is both a receiver and an exporter of pipeline " +
						rule.Quote(p.Key) + ", which forms a loop",
				})
			}
		}
	}
}
