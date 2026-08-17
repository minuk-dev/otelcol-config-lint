// Package missingmemorylimiter reports a pipeline with nothing bounding the
// collector's memory use.
package missingmemorylimiter

import (
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// New builds the rule.
func New() rule.Rule {
	return missingMemoryLimiter{rule.NewBase("missing-memory-limiter",
		"a pipeline without memory_limiter can be OOM-killed under load", diag.Info)}
}

type missingMemoryLimiter struct{ rule.Base }

func (r missingMemoryLimiter) Check(ctx *rule.Context) {
	for _, p := range ctx.File.Service.Pipelines {
		if rule.HasProcessorType(p, rule.MemoryLimiterType) || len(p.Receivers) == 0 {
			continue
		}

		ctx.Report(rule.Finding{
			Node: rule.NodeOr(p.ProcessorsNode, p.KeyNode),
			Path: "service.pipelines." + p.Key + ".processors",

			Message: "pipeline " + rule.Quote(p.Key) + " has no memory_limiter processor",
			Hint:    "add memory_limiter as the first processor to bound the collector's memory use",
			Docs:    rule.MemoryLimiterDocs,
		})
	}
}
