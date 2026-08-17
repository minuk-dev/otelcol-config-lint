// Package memorylimiterconfig reports a memory_limiter the collector will
// refuse to start.
//
// It checks what a field schema cannot state: a value that must be written
// even though the field has a default, two fields that are a one-of, and
// comparisons between one field and another.
package memorylimiterconfig

import (
	"slices"
	"time"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// The numbers upstream documents, in the memorylimiterprocessor README.
const (
	// recommendedInterval is the check_interval the README tells people to
	// start from.
	recommendedInterval = time.Second
	// intervalFloor and intervalCeiling bound how far from the recommended
	// interval a value has to be before it is worth a remark. Anything in
	// between is a deliberate choice, not a mistake.
	intervalFloor   = 100 * time.Millisecond
	intervalCeiling = 5 * time.Second
)

// New builds the rule.
func New() rule.Rule {
	return memoryLimiterConfig{rule.NewBase("memory-limiter-config",
		"a memory_limiter the collector will refuse to start", diag.Error)}
}

type memoryLimiterConfig struct{ rule.Base }

func (r memoryLimiterConfig) Check(ctx *rule.Context) {
	for _, lim := range rule.MemoryLimiters(ctx.File) {
		r.checkInterval(ctx, lim)
		r.checkLimits(ctx, lim)
		r.checkSpikes(ctx, lim)
	}
}

// checkInterval reports the one setting a memory_limiter cannot start without.
// Its default is 0s, so leaving it out is an error rather than a choice.
func (r memoryLimiterConfig) checkInterval(ctx *rule.Context, lim rule.MemoryLimiter) {
	path := rule.JoinPath(lim.Path, "check_interval")
	interval := time.Duration(lim.CheckInterval.Num)

	switch {
	case !lim.CheckInterval.Present:
		// The field schema marks check_interval required on every release that
		// describes the component, and required-field reports it there. Saying
		// it twice about one line helps nobody.
		if schemaRequires(ctx, rule.MemoryLimiterType, "check_interval") {
			return
		}

		ctx.Report(rule.Finding{
			Node: lim.Node, Path: path,
			Message: lim.Name() + " has no check_interval, and its default of 0s is rejected: " +
				"'check_interval' must be greater than zero",
			Hint: "set check_interval: 1s, the value upstream recommends",
			Docs: rule.MemoryLimiterDocs,
		})
	case lim.CheckInterval.Known && interval <= 0:
		ctx.Report(rule.Finding{
			Node: lim.At(lim.CheckInterval), Path: path,
			Message: lim.Name() + " sets check_interval to " + interval.String() +
				": 'check_interval' must be greater than zero",
			Hint: "set check_interval: 1s, the value upstream recommends",
			Docs: rule.MemoryLimiterDocs,
		})
	case lim.CheckInterval.Known && (interval < intervalFloor || interval > intervalCeiling):
		ctx.Report(rule.Finding{
			Node: lim.At(lim.CheckInterval), Path: path, Severity: diag.Info,
			Message: lim.Name() + " checks memory every " + interval.String() +
				"; upstream recommends " + recommendedInterval.String(),
			Hint: "a long interval lets memory run away between checks, a very short one costs CPU",
			Docs: rule.MemoryLimiterDocs,
		})
	}
}

// checkLimits reports the one-of that a flat list of required fields cannot
// express: either limit_mib or limit_percentage has to be set.
func (r memoryLimiterConfig) checkLimits(ctx *rule.Context, lim rule.MemoryLimiter) {
	if lim.LimitMiB.Positive() || lim.LimitPercent.Positive() {
		return
	}

	// An expansion is a value, just not one that can be read here.
	if lim.LimitMiB.Unknown() || lim.LimitPercent.Unknown() {
		return
	}

	ctx.Report(rule.Finding{
		Node: lim.Node, Path: rule.JoinPath(lim.Path, "limit_mib"),
		Message: lim.Name() + " sets neither limit_mib nor limit_percentage: " +
			"'limit_mib' or 'limit_percentage' must be greater than zero",
		Hint: "set limit_mib to the memory the collector may use; spike_limit_mib then defaults to " +
			rule.Itoa(rule.SpikeDefaultPercent) + "% of it",
		Docs: rule.MemoryLimiterDocs,
	})
}

// checkSpikes reports the comparisons between two fields, which a schema
// describing one field at a time cannot make.
func (r memoryLimiterConfig) checkSpikes(ctx *rule.Context, lim rule.MemoryLimiter) {
	if lim.SpikeMiB.Known && lim.LimitMiB.Positive() && lim.SpikeMiB.Num >= lim.LimitMiB.Num {
		ctx.Report(rule.Finding{
			Node: lim.At(lim.SpikeMiB), Path: rule.JoinPath(lim.Path, "spike_limit_mib"),
			Message: lim.Name() + " sets spike_limit_mib to " + rule.Itoa64(lim.SpikeMiB.Num) +
				" and limit_mib to " + rule.Itoa64(lim.LimitMiB.Num) +
				": 'spike_limit_mib' must be smaller than 'limit_mib'",
			Hint: "leave spike_limit_mib out to take the default of " + rule.Itoa(rule.SpikeDefaultPercent) +
				"% of limit_mib, which is where upstream suggests starting",
			Docs: rule.MemoryLimiterDocs,
		})
	}

	if lim.SpikePercent.Known && lim.LimitPercent.Positive() && lim.SpikePercent.Num >= lim.LimitPercent.Num {
		ctx.Report(rule.Finding{
			Node: lim.At(lim.SpikePercent), Path: rule.JoinPath(lim.Path, "spike_limit_percentage"),
			Message: lim.Name() + " sets spike_limit_percentage to " + rule.Itoa64(lim.SpikePercent.Num) +
				" and limit_percentage to " + rule.Itoa64(lim.LimitPercent.Num) +
				": 'spike_limit_percentage' must be smaller than 'limit_percentage'",
			Docs: rule.MemoryLimiterDocs,
		})
	}

	r.checkPercentages(ctx, lim)
}

// checkPercentages reports a share of the container's memory above a hundred
// percent, which is not a share of anything.
func (r memoryLimiterConfig) checkPercentages(ctx *rule.Context, lim rule.MemoryLimiter) {
	for _, pct := range []struct {
		key string
		val rule.Setting
	}{
		{key: "limit_percentage", val: lim.LimitPercent},
		{key: "spike_limit_percentage", val: lim.SpikePercent},
	} {
		if pct.val.Known && pct.val.Num > rule.WholePercent {
			ctx.Report(rule.Finding{
				Node: lim.At(pct.val), Path: rule.JoinPath(lim.Path, pct.key),
				Message: lim.Name() + " sets " + pct.key + " to " + rule.Itoa64(pct.val.Num) +
					": 'limit_percentage' and 'spike_limit_percentage' must be greater than zero " +
					"and less than or equal to hundred",
				Docs: rule.MemoryLimiterDocs,
			})
		}
	}
}

// schemaRequires reports whether the targeted release's field schema already
// marks a processor setting required.
func schemaRequires(ctx *rule.Context, typ, field string) bool {
	if !ctx.SchemaReady() {
		return false
	}

	comp, ok := ctx.Schema.Lookup(config.KindProcessor, typ)
	if !ok || comp.Fields == nil {
		return false
	}

	return slices.Contains(comp.Fields.Required, field)
}
