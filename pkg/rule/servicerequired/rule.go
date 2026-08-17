// Package servicerequired reports a config with nothing wired up to run.
package servicerequired

import (
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// New builds the rule.
func New() rule.Rule {
	return serviceRequired{rule.NewBase("service-required",
		"a config must declare a service block with at least one pipeline", diag.Error)}
}

type serviceRequired struct{ rule.Base }

func (r serviceRequired) Check(ctx *rule.Context) {
	svc := ctx.File.Service
	if svc.Node == nil {
		ctx.Report(rule.Finding{
			Node: ctx.File.Root, Path: "service",
			Message: "config has no service block, so nothing will run",
			Hint:    "add a service.pipelines section wiring the declared components together",
		})

		return
	}

	if svc.PipelinesNode == nil {
		ctx.Report(rule.Finding{
			Node: svc.KeyNode, Path: "service",
			Message: "service has no pipelines, so no telemetry will be processed",
		})

		return
	}

	if len(svc.Pipelines) == 0 {
		ctx.Report(rule.Finding{
			Node: svc.PipelinesNode, Path: "service.pipelines",
			Message: "service.pipelines is empty, so no telemetry will be processed",
		})
	}
}
