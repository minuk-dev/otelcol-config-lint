// Package unknownservicekey reports a key the service block does not accept.
package unknownservicekey

import (
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// serviceKeys are the only keys the service block accepts.
//
// It returns a fresh slice so callers cannot alter what everyone else sees.
func serviceKeys() []string { return []string{"extensions", "pipelines", "telemetry"} }

// New builds the rule.
func New() rule.Rule {
	return unknownServiceKey{rule.NewBase("unknown-service-key",
		"service accepts only extensions, pipelines and telemetry", diag.Error)}
}

type unknownServiceKey struct{ rule.Base }

func (r unknownServiceKey) Check(ctx *rule.Context) {
	for _, e := range ctx.File.Service.Unknown {
		ctx.Report(rule.Finding{
			Node: e.KeyNode, Path: e.Path,
			Message: "unknown key " + rule.Quote(e.Key) + " in service",
			Hint: "service accepts extensions, pipelines and telemetry" +
				rule.Suggest(e.Key, serviceKeys()),
		})
	}
}
