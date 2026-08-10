package rule

import (
	"strconv"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/quantity"
)

// settingsRules check what a field schema cannot state: a value that must be
// written even though the field has a default, two fields that are a one-of,
// and comparisons between one field and another.
func settingsRules() []Rule {
	return []Rule{
		memoryLimiterConfig{base{"memory-limiter-config",
			"a memory_limiter the collector will refuse to start", diag.Error}},
		memoryLimiterSizing{base{"memory-limiter-sizing",
			"a memory_limiter whose limits do not fit the container it runs in", diag.Warning}},
	}
}

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

	// spikeDefaultPercent is what spike_limit_mib defaults to, as a share of
	// limit_mib.
	spikeDefaultPercent = 20
	// processOverhead is how far the collector's total memory use runs above
	// the figure the limiter enforces, per the README.
	processOverhead = 50 * quantity.Mi
	// ceilingPercent is the share of the container's memory limit the hard
	// limit is documented to stay under.
	ceilingPercent = 80
	// wholePercent is 100%, the divisor for every percentage above.
	wholePercent = 100
)

// mib is the unit limit_mib and spike_limit_mib are counted in.
const mib = quantity.Mi

// setting is one value read out of a memory_limiter's settings block.
type setting struct {
	// node is the value node, or nil when the key was not written.
	node *yaml.Node
	// present reports that the key was written at all.
	present bool
	// known reports that num holds the value. A confmap expansion such as
	// ${env:LIMIT} is present but never known, and neither is a value of the
	// wrong type, which invalid-value reports on its own.
	known bool
	// num is the value: a count for the integer settings, and nanoseconds for
	// check_interval.
	num int64
}

// positive reports a value that is known and above zero.
func (s setting) positive() bool { return s.known && s.num > 0 }

// memoryLimiter is one declared memory_limiter instance and the settings the
// collector validates before it starts.
type memoryLimiter struct {
	id config.ID
	// node anchors findings about the instance as a whole.
	node *yaml.Node
	// path is the dotted path of the instance, e.g. "processors.memory_limiter".
	path string

	checkInterval setting
	limitMiB      setting
	limitPercent  setting
	spikeMiB      setting
	spikePercent  setting
}

// name renders the instance for a message, so two findings about the same
// container limit can be told apart.
func (m memoryLimiter) name() string { return "processor " + quote(m.id.String()) }

// setting returns the value node of one of the instance's settings, falling
// back to the instance itself when the key is absent.
func (m memoryLimiter) at(s setting) *yaml.Node { return nodeOr(s.node, m.node) }

// hardLimit returns the number of bytes the limiter will actually enforce, and
// whether it could be worked out at all. limit_mib wins over limit_percentage,
// as it does upstream, and a percentage is only a number when the container's
// memory limit is known.
//
// A value resolved at runtime leaves the whole figure unknown: a partly known
// hard limit is worse than none, since every finding about it would be
// confident about a number nobody has yet.
func (m memoryLimiter) hardLimit(env Environment) (int64, bool) {
	if unknownValue(m.limitMiB) || unknownValue(m.limitPercent) {
		return 0, false
	}

	if m.limitMiB.positive() {
		return m.limitMiB.num * mib, true
	}

	if m.limitPercent.positive() && env.MemoryLimit > 0 {
		return env.MemoryLimit * m.limitPercent.num / wholePercent, true
	}

	return 0, false
}

// memoryLimiters returns every declared memory_limiter. Instances are matched
// on their type, so "memory_limiter/aggressive" is covered too.
func memoryLimiters(f *config.File) []memoryLimiter {
	sec := f.Sections[config.KindProcessor]
	if sec == nil {
		return nil
	}

	var out []memoryLimiter

	for _, c := range sec.Components {
		if c.ID.Type == memoryLimiterType {
			out = append(out, readMemoryLimiter(c))
		}
	}

	return out
}

func readMemoryLimiter(c config.Component) memoryLimiter {
	lim := memoryLimiter{
		id:            c.ID,
		node:          nodeOr(c.KeyNode, c.ValueNode),
		path:          config.KindProcessor.Section() + "." + c.ID.String(),
		checkInterval: absent(),
		limitMiB:      absent(),
		limitPercent:  absent(),
		spikeMiB:      absent(),
		spikePercent:  absent(),
	}

	if c.ValueNode == nil || c.ValueNode.Kind != yaml.MappingNode {
		return lim
	}

	for _, e := range mapEntries(c.ValueNode, lim.path) {
		switch e.key {
		case "check_interval":
			lim.checkInterval = readDuration(e.node)
		case "limit_mib":
			lim.limitMiB = readInt(e.node)
		case "limit_percentage":
			lim.limitPercent = readInt(e.node)
		case "spike_limit_mib":
			lim.spikeMiB = readInt(e.node)
		case "spike_limit_percentage":
			lim.spikePercent = readInt(e.node)
		default:
			// Every other setting is the field schema's business.
		}
	}

	return lim
}

func absent() setting { return setting{node: nil, present: false, known: false, num: 0} }

// readInt reads an integer setting. A value nothing can know before the
// collector starts -- an expansion, or text of the wrong type -- is present
// but not known, which stops every check that would need the number.
func readInt(node *yaml.Node) setting {
	out := setting{node: node, present: true, known: false, num: 0}
	if node == nil || node.Kind != yaml.ScalarNode || hasExpansion(node.Value) {
		return out
	}

	num, err := strconv.ParseInt(node.Value, 10, 64)
	if err != nil {
		return out
	}

	out.known, out.num = true, num

	return out
}

// readDuration reads a duration setting, holding it in nanoseconds.
func readDuration(node *yaml.Node) setting {
	out := setting{node: node, present: true, known: false, num: 0}
	if node == nil || node.Kind != yaml.ScalarNode || hasExpansion(node.Value) {
		return out
	}

	dur, err := time.ParseDuration(node.Value)
	if err != nil {
		return out
	}

	out.known, out.num = true, int64(dur)

	return out
}

type memoryLimiterConfig struct{ base }

func (r memoryLimiterConfig) Check(ctx *Context) {
	for _, lim := range memoryLimiters(ctx.File) {
		r.checkInterval(ctx, lim)
		r.checkLimits(ctx, lim)
		r.checkSpikes(ctx, lim)
	}
}

// checkInterval reports the one setting a memory_limiter cannot start without.
// Its default is 0s, so leaving it out is an error rather than a choice.
func (r memoryLimiterConfig) checkInterval(ctx *Context, lim memoryLimiter) {
	path := joinPath(lim.path, "check_interval")
	interval := time.Duration(lim.checkInterval.num)

	switch {
	case !lim.checkInterval.present:
		// The field schema marks check_interval required on every release that
		// describes the component, and required-field reports it there. Saying
		// it twice about one line helps nobody.
		if schemaRequires(ctx, memoryLimiterType, "check_interval") {
			return
		}

		ctx.Report(Finding{
			Node: lim.node, Path: path,
			Message: lim.name() + " has no check_interval, and its default of 0s is rejected: " +
				"'check_interval' must be greater than zero",
			Hint: "set check_interval: 1s, the value upstream recommends",
		})
	case lim.checkInterval.known && interval <= 0:
		ctx.Report(Finding{
			Node: lim.at(lim.checkInterval), Path: path,
			Message: lim.name() + " sets check_interval to " + interval.String() +
				": 'check_interval' must be greater than zero",
			Hint: "set check_interval: 1s, the value upstream recommends",
		})
	case lim.checkInterval.known && (interval < intervalFloor || interval > intervalCeiling):
		ctx.Report(Finding{
			Node: lim.at(lim.checkInterval), Path: path, Severity: diag.Info,
			Message: lim.name() + " checks memory every " + interval.String() +
				"; upstream recommends " + recommendedInterval.String(),
			Hint: "a long interval lets memory run away between checks, a very short one costs CPU",
		})
	}
}

// checkLimits reports the one-of that a flat list of required fields cannot
// express: either limit_mib or limit_percentage has to be set.
func (r memoryLimiterConfig) checkLimits(ctx *Context, lim memoryLimiter) {
	if lim.limitMiB.positive() || lim.limitPercent.positive() {
		return
	}

	// An expansion is a value, just not one that can be read here.
	if unknownValue(lim.limitMiB) || unknownValue(lim.limitPercent) {
		return
	}

	ctx.Report(Finding{
		Node: lim.node, Path: joinPath(lim.path, "limit_mib"),
		Message: lim.name() + " sets neither limit_mib nor limit_percentage: " +
			"'limit_mib' or 'limit_percentage' must be greater than zero",
		Hint: "set limit_mib to the memory the collector may use; spike_limit_mib then defaults to " +
			itoa(spikeDefaultPercent) + "% of it",
	})
}

// checkSpikes reports the comparisons between two fields, which a schema
// describing one field at a time cannot make.
func (r memoryLimiterConfig) checkSpikes(ctx *Context, lim memoryLimiter) {
	if lim.spikeMiB.known && lim.limitMiB.positive() && lim.spikeMiB.num >= lim.limitMiB.num {
		ctx.Report(Finding{
			Node: lim.at(lim.spikeMiB), Path: joinPath(lim.path, "spike_limit_mib"),
			Message: lim.name() + " sets spike_limit_mib to " + itoa64(lim.spikeMiB.num) +
				" and limit_mib to " + itoa64(lim.limitMiB.num) +
				": 'spike_limit_mib' must be smaller than 'limit_mib'",
			Hint: "leave spike_limit_mib out to take the default of " + itoa(spikeDefaultPercent) +
				"% of limit_mib, which is where upstream suggests starting",
		})
	}

	if lim.spikePercent.known && lim.limitPercent.positive() && lim.spikePercent.num >= lim.limitPercent.num {
		ctx.Report(Finding{
			Node: lim.at(lim.spikePercent), Path: joinPath(lim.path, "spike_limit_percentage"),
			Message: lim.name() + " sets spike_limit_percentage to " + itoa64(lim.spikePercent.num) +
				" and limit_percentage to " + itoa64(lim.limitPercent.num) +
				": 'spike_limit_percentage' must be smaller than 'limit_percentage'",
		})
	}

	for _, pct := range []struct {
		key string
		val setting
	}{
		{key: "limit_percentage", val: lim.limitPercent},
		{key: "spike_limit_percentage", val: lim.spikePercent},
	} {
		if pct.val.known && pct.val.num > wholePercent {
			ctx.Report(Finding{
				Node: lim.at(pct.val), Path: joinPath(lim.path, pct.key),
				Message: lim.name() + " sets " + pct.key + " to " + itoa64(pct.val.num) +
					": 'limit_percentage' and 'spike_limit_percentage' must be greater than zero " +
					"and less than or equal to hundred",
			})
		}
	}
}

// unknownValue reports a setting that was written but whose value cannot be
// read here, so no check that needs the number can run.
func unknownValue(s setting) bool { return s.present && !s.known }

// schemaRequires reports whether the targeted release's field schema already
// marks a processor setting required.
func schemaRequires(ctx *Context, typ, field string) bool {
	if !ctx.schemaReady() {
		return false
	}

	comp, ok := ctx.Schema.Lookup(config.KindProcessor, typ)
	if !ok || comp.Fields == nil {
		return false
	}

	return contains(comp.Fields.Required, field)
}

type memoryLimiterSizing struct{ base }

func (r memoryLimiterSizing) Check(ctx *Context) {
	if !ctx.Env.Known() {
		return
	}

	for _, lim := range memoryLimiters(ctx.File) {
		r.checkPercentage(ctx, lim)

		hard, ok := lim.hardLimit(ctx.Env)
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
func (r memoryLimiterSizing) checkPercentage(ctx *Context, lim memoryLimiter) {
	if !lim.limitPercent.present || ctx.Env.MemoryLimit > 0 {
		return
	}

	ctx.Report(Finding{
		Node: lim.at(lim.limitPercent), Path: joinPath(lim.path, "limit_percentage"),
		Message: lim.name() + " limits memory by percentage, but the container has no memory limit",
		Hint: "the percentage resolves against the node's memory instead, so the limiter is effectively off; " +
			"set a container memory limit, or use limit_mib",
	})
}

// checkLimit compares what the limiter will enforce against the limit the
// kernel enforces. Only the most serious of the three applies, so a limit that
// is over the container's is not also reported as being over 80% of it.
func (r memoryLimiterSizing) checkLimit(ctx *Context, lim memoryLimiter, hard int64) {
	limit := ctx.Env.MemoryLimit
	if limit <= 0 {
		return
	}

	finding := Finding{
		Node: lim.at(lim.limitMiB), Path: lim.path,
		Hint: "keep the hard limit near " + itoa(ceilingPercent) + "% of the container memory limit, " +
			"and set GOMEMLIMIT to about " + itoa(ceilingPercent) + "% of the hard limit so the collector " +
			"collects garbage before the limiter has to refuse data",
	}

	switch {
	case hard >= limit:
		finding.Severity = diag.Error
		finding.Message = lim.name() + " enforces " + quantity.Format(hard) +
			", at or above the container memory limit of " + quantity.Format(limit) +
			"; the kernel kills the collector before the limiter engages"
	case hard+processOverhead > limit:
		finding.Message = lim.name() + " enforces " + quantity.Format(hard) + " of the container's " +
			quantity.Format(limit) + ", leaving " + quantity.Format(limit-hard) +
			"; the process runs about " + quantity.Format(processOverhead) + " above what the limiter counts"
	case hard > limit*ceilingPercent/wholePercent:
		finding.Message = lim.name() + " enforces " + quantity.Format(hard) + ", above " +
			itoa(ceilingPercent) + "% of the container memory limit of " + quantity.Format(limit)
	default:
		return
	}

	ctx.Report(finding)
}

// checkRequest reports a limiter that may use memory the scheduler never
// reserved.
func (r memoryLimiterSizing) checkRequest(ctx *Context, lim memoryLimiter, hard int64) {
	request := ctx.Env.MemoryRequest
	if request <= 0 || request >= hard {
		return
	}

	ctx.Report(Finding{
		Node: lim.at(lim.limitMiB), Path: lim.path, Severity: diag.Info,
		Message: lim.name() + " may use " + quantity.Format(hard) + ", above the container memory request of " +
			quantity.Format(request),
		Hint: "the pod is Burstable and is evicted first under node pressure; " +
			"collectors usually set the memory request equal to the limit",
	})
}

// itoa64 renders a setting's value for a message.
func itoa64(n int64) string { return strconv.FormatInt(n, 10) }
