// Package unknownpipelinekey reports a key a pipeline does not accept.
package unknownpipelinekey

import (
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// pipelineKeys are the only keys a pipeline accepts.
//
// It returns a fresh slice so callers cannot alter what everyone else sees.
func pipelineKeys() []string { return []string{"receivers", "processors", "exporters"} }

// New builds the rule.
func New() rule.Rule {
	return unknownPipelineKey{rule.NewBase("unknown-pipeline-key",
		"pipelines accept only receivers, processors and exporters", diag.Error)}
}

type unknownPipelineKey struct{ rule.Base }

func (r unknownPipelineKey) Check(ctx *rule.Context) {
	for _, p := range ctx.File.Service.Pipelines {
		for _, e := range p.Unknown {
			ctx.Report(rule.Finding{
				Node: e.KeyNode, Path: e.Path,
				Message: "unknown key " + rule.Quote(e.Key) + " in pipeline " + rule.Quote(p.Key),
				Hint: "pipelines accept receivers, processors and exporters" +
					rule.Suggest(e.Key, pipelineKeys()),
			})
		}
	}
}
