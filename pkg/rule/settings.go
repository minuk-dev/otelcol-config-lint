package rule

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/samber/lo"
	"github.com/samber/mo"
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
		batchSizeBounds{base{"batch-size-bounds",
			"a batch processor the collector will refuse to start", diag.Error}},
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

// hardLimit returns the number of bytes the limiter will actually enforce, or
// nothing when it cannot be worked out at all. limit_mib wins over
// limit_percentage, as it does upstream, and a percentage is only a number when
// the container's memory limit is known.
//
// A value resolved at runtime leaves the whole figure unknown: a partly known
// hard limit is worse than none, since every finding about it would be
// confident about a number nobody has yet.
func (m memoryLimiter) hardLimit(env Environment) mo.Option[int64] {
	if unknownValue(m.limitMiB) || unknownValue(m.limitPercent) {
		return mo.None[int64]()
	}

	// A figure that does not fit in a byte count is left unknown rather than
	// wrapped: a wrapped product is a plausible-looking number that appears
	// nowhere in the config, and a finding quoting it would be worse than none.
	if m.limitMiB.positive() {
		if m.limitMiB.num > math.MaxInt64/mib {
			return mo.None[int64]()
		}

		return mo.Some(m.limitMiB.num * mib)
	}

	if m.limitPercent.positive() && env.MemoryLimit > 0 {
		if env.MemoryLimit > math.MaxInt64/m.limitPercent.num {
			return mo.None[int64]()
		}

		return mo.Some(env.MemoryLimit * m.limitPercent.num / wholePercent)
	}

	return mo.None[int64]()
}

// memoryLimiters returns every declared memory_limiter. Instances are matched
// on their type, so "memory_limiter/aggressive" is covered too.
func memoryLimiters(f *config.File) []memoryLimiter {
	return processorsOfType(f, memoryLimiterType, readMemoryLimiter)
}

// processorsOfType returns every declared processor of one type, read into
// whatever the caller needs. Matching on the type rather than the whole id is
// what covers named instances such as "batch/traces".
func processorsOfType[T any](f *config.File, typ string, read func(config.Component) T) []T {
	sec := f.Sections[config.KindProcessor]
	if sec == nil {
		return nil
	}

	declared := lo.Filter(sec.Components, func(c config.Component, _ int) bool { return c.ID.Type == typ })

	return lo.Map(declared, func(c config.Component, _ int) T { return read(c) })
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

// readInt reads an integer setting, in the base the collector's own decoder
// reads it: confmap unmarshals through yaml.v3, which resolves 0x400 as 1024,
// 8_192 as 8192, and a leading zero as octal, so 0100 is 64 and not a hundred.
// Reading base 10 here would compare, and quote back, a number the collector
// never sees.
//
// A value nothing can know before the collector starts -- an expansion, or text
// of the wrong type -- is present but not known, which stops every check that
// would need the number.
func readInt(node *yaml.Node) setting {
	out := setting{node: node, present: true, known: false, num: 0}
	if node == nil || node.Kind != yaml.ScalarNode || hasExpansion(node.Value) {
		return out
	}

	num, err := strconv.ParseInt(node.Value, 0, 64)
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
			Docs: memoryLimiterDocs,
		})
	case lim.checkInterval.known && interval <= 0:
		ctx.Report(Finding{
			Node: lim.at(lim.checkInterval), Path: path,
			Message: lim.name() + " sets check_interval to " + interval.String() +
				": 'check_interval' must be greater than zero",
			Hint: "set check_interval: 1s, the value upstream recommends",
			Docs: memoryLimiterDocs,
		})
	case lim.checkInterval.known && (interval < intervalFloor || interval > intervalCeiling):
		ctx.Report(Finding{
			Node: lim.at(lim.checkInterval), Path: path, Severity: diag.Info,
			Message: lim.name() + " checks memory every " + interval.String() +
				"; upstream recommends " + recommendedInterval.String(),
			Hint: "a long interval lets memory run away between checks, a very short one costs CPU",
			Docs: memoryLimiterDocs,
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
		Docs: memoryLimiterDocs,
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
			Docs: memoryLimiterDocs,
		})
	}

	if lim.spikePercent.known && lim.limitPercent.positive() && lim.spikePercent.num >= lim.limitPercent.num {
		ctx.Report(Finding{
			Node: lim.at(lim.spikePercent), Path: joinPath(lim.path, "spike_limit_percentage"),
			Message: lim.name() + " sets spike_limit_percentage to " + itoa64(lim.spikePercent.num) +
				" and limit_percentage to " + itoa64(lim.limitPercent.num) +
				": 'spike_limit_percentage' must be smaller than 'limit_percentage'",
			Docs: memoryLimiterDocs,
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
				Docs: memoryLimiterDocs,
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

		hard, ok := lim.hardLimit(ctx.Env).Get()
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
		Docs: memoryLimiterDocs,
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
		Docs: memoryLimiterDocs,
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
		Docs: kubernetesResourceDocs,
	})
}

// The numbers upstream defaults to, in the batchprocessor's factory.
const (
	// defaultSendBatchSize is the number of items a batch is sent at when
	// send_batch_size is left out. It is the figure that makes the bounds
	// check worth having: a cap picked to look reasonable, say 1000, sits
	// below it without anyone writing 8192 anywhere.
	defaultSendBatchSize = 8192
	// defaultBatchTimeout is how long a batch waits before it is sent
	// regardless of size, when timeout is left out.
	defaultBatchTimeout = 200 * time.Millisecond
	// maxBatchSize is the largest value the two size fields can hold: upstream
	// types both as uint32. The published field schemas flatten that to "int",
	// so nothing else in the linter knows the range.
	maxBatchSize = math.MaxUint32
)

// batchProcessor is one declared batch instance and the settings the collector
// validates before it starts.
type batchProcessor struct {
	id config.ID
	// node anchors findings about the instance as a whole.
	node *yaml.Node
	// path is the dotted path of the instance, e.g. "processors.batch".
	path string

	sendBatchSize    setting
	sendBatchMaxSize setting
	timeout          setting
	// metadataKeys is the metadata_keys sequence, or nil when the key was not
	// written. The entries are read where the duplicates are reported, since
	// each finding names the entry it found.
	metadataKeys *yaml.Node
	// merged reports a YAML merge key, "<<", among the settings. The document
	// is read as written, so a key the merge supplies looks absent here, and a
	// check that would otherwise fill in a default has to say nothing instead.
	merged bool
}

// name renders the instance for a message, so a file with more than one batch
// processor says which is meant.
func (b batchProcessor) name() string { return "processor " + quote(b.id.String()) }

// at returns the value node of one of the instance's settings, falling back to
// the instance itself when the key is absent.
func (b batchProcessor) at(s setting) *yaml.Node { return nodeOr(s.node, b.node) }

// batchProcessors returns every declared batch processor, matched on type so
// "batch/traces" is covered too.
func batchProcessors(f *config.File) []batchProcessor {
	return processorsOfType(f, batchType, readBatch)
}

func readBatch(c config.Component) batchProcessor {
	proc := batchProcessor{
		id:               c.ID,
		node:             nodeOr(c.KeyNode, c.ValueNode),
		path:             config.KindProcessor.Section() + "." + c.ID.String(),
		sendBatchSize:    absent(),
		sendBatchMaxSize: absent(),
		timeout:          absent(),
		metadataKeys:     nil,
		merged:           false,
	}

	if c.ValueNode == nil || c.ValueNode.Kind != yaml.MappingNode {
		return proc
	}

	for _, e := range mapEntries(c.ValueNode, proc.path) {
		switch e.key {
		case "send_batch_size":
			proc.sendBatchSize = readInt(e.node)
		case "send_batch_max_size":
			proc.sendBatchMaxSize = readInt(e.node)
		case "timeout":
			proc.timeout = readDuration(e.node)
		case "metadata_keys":
			proc.metadataKeys = e.node
		case "<<":
			proc.merged = true
		default:
			// Every other setting is the field schema's business.
		}
	}

	return proc
}

type batchSizeBounds struct{ base }

func (r batchSizeBounds) Check(ctx *Context) {
	for _, proc := range batchProcessors(ctx.File) {
		// A size the field cannot hold stands on its own: it fails to decode
		// whatever the other field says, and it stops the comparison, which
		// the collector never reaches.
		if !r.checkRange(ctx, proc) {
			r.checkSizes(ctx, proc)
		}

		r.checkTimeout(ctx, proc)
		r.checkMetadataKeys(ctx, proc)
	}
}

// checkSizes compares the cap against the threshold that triggers a send.
// Neither number is wrong on its own, so a schema describing one field at a
// time sees nothing: send_batch_size is when a batch goes, send_batch_max_size
// is how large one may get, and a cap below the trigger is rejected.
func (r batchSizeBounds) checkSizes(ctx *Context, proc batchProcessor) {
	if unknownValue(proc.sendBatchSize) || unknownValue(proc.sendBatchMaxSize) {
		return
	}

	// A cap of zero, the default, means batches are not capped at all.
	if !proc.sendBatchMaxSize.positive() {
		return
	}

	size := int64(defaultSendBatchSize)

	switch {
	case proc.sendBatchSize.known:
		size = proc.sendBatchSize.num
	case proc.merged:
		// The merge key may be what sets send_batch_size, and the collector
		// resolves it before either number is read. Filling in the default
		// here would report a figure the config overrides.
		return
	}

	if proc.sendBatchMaxSize.num >= size {
		return
	}

	// The default is the whole point of the rule, and also the one number in
	// the message the reader will not find in their file, so it is named as a
	// default rather than quoted back at them as if they had written it.
	written := " and send_batch_size to " + itoa64(size)
	if !proc.sendBatchSize.present {
		written = ", below the default send_batch_size of " + itoa64(size)
	}

	ctx.Report(Finding{
		Node: proc.at(proc.sendBatchMaxSize), Path: joinPath(proc.path, "send_batch_max_size"),
		Message: proc.name() + " sets send_batch_max_size to " + itoa64(proc.sendBatchMaxSize.num) + written +
			": send_batch_max_size must be greater or equal to send_batch_size",
		Hint: "raise send_batch_max_size to " + itoa64(size) +
			" or more, or leave it out so batches are not split at all",
		Docs: batchDocs,
	})
}

// checkRange reports a size outside the uint32 the field holds, and reports
// whether it found one. Such a value fails to decode, so the collector never
// gets as far as the bounds check: quoting it back in a comparison would
// diagnose the wrong problem and hint at a fix that still does not load.
func (r batchSizeBounds) checkRange(ctx *Context, proc batchProcessor) bool {
	var found bool

	for _, size := range []struct {
		key string
		val setting
	}{
		{key: "send_batch_size", val: proc.sendBatchSize},
		{key: "send_batch_max_size", val: proc.sendBatchMaxSize},
	} {
		if !size.val.known || (size.val.num >= 0 && size.val.num <= maxBatchSize) {
			continue
		}

		found = true

		ctx.Report(Finding{
			Node: proc.at(size.val), Path: joinPath(proc.path, size.key),
			Message: proc.name() + " sets " + size.key + " to " + itoa64(size.val.num) +
				", which the field cannot hold: it counts items as a uint32, " +
				"so the collector fails to load the config before it validates it",
			Hint: "write a whole number between 0 and " + itoa64(maxBatchSize),
			Docs: batchDocs,
		})
	}

	return found
}

// checkTimeout reports the one value the collector rejects outright. Zero is
// allowed and means every batch is sent as soon as it is formed.
func (r batchSizeBounds) checkTimeout(ctx *Context, proc batchProcessor) {
	if !proc.timeout.known || proc.timeout.num >= 0 {
		return
	}

	ctx.Report(Finding{
		Node: proc.at(proc.timeout), Path: joinPath(proc.path, "timeout"),
		Message: proc.name() + " sets timeout to " + time.Duration(proc.timeout.num).String() +
			": timeout must be greater or equal to 0",
		Hint: "leave timeout out to take the default of " + defaultBatchTimeout.String() +
			", or set 0 to send each batch as soon as it is formed",
		Docs: batchDocs,
	})
}

// checkMetadataKeys reports an entry listed twice. The keys are folded to lower
// case before they are compared, so a duplicate need not look like one.
func (r batchSizeBounds) checkMetadataKeys(ctx *Context, proc batchProcessor) {
	if proc.metadataKeys == nil || proc.metadataKeys.Kind != yaml.SequenceNode {
		return
	}

	path := joinPath(proc.path, "metadata_keys")
	seen := map[string]bool{}

	for i, item := range proc.metadataKeys.Content {
		if item.Kind != yaml.ScalarNode || hasExpansion(item.Value) {
			continue
		}

		folded := strings.ToLower(item.Value)
		if !seen[folded] {
			seen[folded] = true

			continue
		}

		ctx.Report(Finding{
			Node: item, Path: indexPath(path, i),
			Message: proc.name() + " repeats " + quote(item.Value) + ": " +
				"duplicate entry in metadata_keys: " + quote(folded) + " (case-insensitive)",
			Hint: "the keys are compared case-insensitively, so " + quote(item.Value) +
				" is one already listed; remove it",
			Docs: batchDocs,
		})
	}
}

// itoa64 renders a setting's value for a message.
func itoa64(n int64) string { return strconv.FormatInt(n, 10) }
