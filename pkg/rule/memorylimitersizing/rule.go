// Package memorylimitersizing reports a memory_limiter whose limits do not fit
// the container it runs in.
package memorylimitersizing

import (
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/quantity"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

const (
	// processOverhead is how far the collector's total memory use runs above
	// the figure the limiter enforces, per the README.
	processOverhead = 50 * quantity.Mi
	// ceilingPercent is the share of the container's memory limit the hard
	// limit is documented to stay under.
	ceilingPercent = 80
)

// New builds the rule.
func New() rule.Rule {
	return memoryLimiterSizing{rule.NewBase("memory-limiter-sizing",
		"a memory_limiter whose limits do not fit the container it runs in", diag.Warning)}
}

type memoryLimiterSizing struct{ rule.Base }

func (r memoryLimiterSizing) Check(ctx *rule.Context) {
	if !ctx.Env.Known() {
		return
	}

	for _, lim := range rule.MemoryLimiters(ctx.File) {
		r.checkPercentage(ctx, lim)

		hard, ok := lim.HardLimit(ctx.Env).Get()
		if !ok {
			continue
		}

		r.checkLimit(ctx, lim, hard)
		r.checkRequest(ctx, lim, hard)
	}
}

// checkPercentage reports a percentage limit with nothing to be a percentage
// of. Outside a container with a memory limit it resolves against the whole
// node, which is not a limit on this collector at all.
func (r memoryLimiterSizing) checkPercentage(ctx *rule.Context, lim rule.MemoryLimiter) {
	if !lim.LimitPercent.Present || ctx.Env.MemoryLimit > 0 {
		return
	}

	ctx.Report(rule.Finding{
		Node: lim.At(lim.LimitPercent), Path: rule.JoinPath(lim.Path, "limit_percentage"),
		Message: lim.Name() + " limits memory by percentage, but the container has no memory limit",
		Hint: "the percentage resolves against the node's memory instead, so the limiter is effectively off; " +
			"set a container memory limit, or use limit_mib",
		Docs: rule.MemoryLimiterDocs,
	})
}

// checkLimit compares what the limiter will enforce against the limit the
// kernel enforces. Only the most serious of the three applies, so a limit that
// is over the container's is not also reported as being over 80% of it.
func (r memoryLimiterSizing) checkLimit(ctx *rule.Context, lim rule.MemoryLimiter, hard int64) {
	limit := ctx.Env.MemoryLimit
	if limit <= 0 {
		return
	}

	finding := rule.Finding{
		Node: lim.At(lim.LimitMiB), Path: lim.Path,
		Hint: "keep the hard limit near " + rule.Itoa(ceilingPercent) + "% of the container memory limit, " +
			"and set GOMEMLIMIT to about " + rule.Itoa(ceilingPercent) + "% of the hard limit so the collector " +
			"collects garbage before the limiter has to refuse data",
		Docs: rule.MemoryLimiterDocs,
	}

	switch {
	case hard >= limit:
		finding.Severity = diag.Error
		finding.Message = lim.Name() + " enforces " + quantity.Format(hard) +
			", at or above the container memory limit of " + quantity.Format(limit) +
			"; the kernel kills the collector before the limiter engages"
	case hard+processOverhead > limit:
		finding.Message = lim.Name() + " enforces " + quantity.Format(hard) + " of the container's " +
			quantity.Format(limit) + ", leaving " + quantity.Format(limit-hard) +
			"; the process runs about " + quantity.Format(processOverhead) + " above what the limiter counts"
	case hard > limit*ceilingPercent/rule.WholePercent:
		finding.Message = lim.Name() + " enforces " + quantity.Format(hard) + ", above " +
			rule.Itoa(ceilingPercent) + "% of the container memory limit of " + quantity.Format(limit)
	default:
		return
	}

	ctx.Report(finding)
}

// checkRequest reports a limiter that may use memory the scheduler never
// reserved.
func (r memoryLimiterSizing) checkRequest(ctx *rule.Context, lim rule.MemoryLimiter, hard int64) {
	request := ctx.Env.MemoryRequest
	if request <= 0 || request >= hard {
		return
	}

	ctx.Report(rule.Finding{
		Node: lim.At(lim.LimitMiB), Path: lim.Path, Severity: diag.Info,
		Message: lim.Name() + " may use " + quantity.Format(hard) +
			", above the container memory request of " + quantity.Format(request),
		Hint: "the pod is Burstable and is evicted first under node pressure; " +
			"collectors usually set the memory request equal to the limit",
		Docs: rule.KubernetesResourceDocs,
	})
}
