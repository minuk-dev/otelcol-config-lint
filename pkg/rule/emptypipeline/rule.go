// Package emptypipeline reports a pipeline missing an end.
package emptypipeline

import (
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// New builds the rule.
func New() rule.Rule {
	return emptyPipeline{rule.NewBase("empty-pipeline",
		"every pipeline needs at least one receiver and one exporter", diag.Error)}
}

type emptyPipeline struct{ rule.Base }

func (r emptyPipeline) Check(ctx *rule.Context) {
	for _, p := range ctx.File.Service.Pipelines {
		path := "service.pipelines." + p.Key
		if len(p.Receivers) == 0 {
			ctx.Report(rule.Finding{
				Node: rule.NodeOr(p.ReceiversNode, p.KeyNode), Path: path + ".receivers",
				Message: "pipeline " + rule.Quote(p.Key) + " has no receivers",
			})
		}

		if len(p.Exporters) == 0 {
			ctx.Report(rule.Finding{
				Node: rule.NodeOr(p.ExportersNode, p.KeyNode), Path: path + ".exporters",
				Message: "pipeline " + rule.Quote(p.Key) + " has no exporters",
			})
		}
	}
}
