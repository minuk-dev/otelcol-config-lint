// Package invalidpipelinekey reports a pipeline key that names no known signal.
package invalidpipelinekey

import (
	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// New builds the rule.
func New() rule.Rule {
	return invalidPipelineKey{rule.NewBase("invalid-pipeline-key",
		"pipeline keys must be <signal> or <signal>/<name>", diag.Error)}
}

type invalidPipelineKey struct{ rule.Base }

func (r invalidPipelineKey) Check(ctx *rule.Context) {
	valid := make([]string, 0, len(config.Signals()))
	for _, s := range config.Signals() {
		valid = append(valid, string(s))
	}

	for _, p := range ctx.File.Service.Pipelines {
		if rule.IsSignal(p.Signal) {
			continue
		}

		ctx.Report(rule.Finding{
			Node: p.KeyNode, Path: "service.pipelines." + p.Key,
			Message: "pipeline " + rule.Quote(p.Key) + " does not name a known signal",
			Hint: "pipeline keys look like traces, metrics/internal or logs/2" +
				rule.Suggest(string(p.Signal), valid),
		})
	}
}
