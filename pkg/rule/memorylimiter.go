package rule

import (
	"math"

	"github.com/samber/mo"
	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/quantity"
)

// SpikeDefaultPercent is what spike_limit_mib defaults to, as a share of
// limit_mib.
const SpikeDefaultPercent = 20

// WholePercent is 100%, the divisor for every percentage the limiter takes.
const WholePercent = 100

// mib is the unit limit_mib and spike_limit_mib are counted in.
const mib = quantity.Mi

// MemoryLimiter is one declared memory_limiter instance and the settings the
// collector validates before it starts. Two rules read it: one for the values
// the collector rejects outright, one for the values that do not fit the
// container the collector runs in.
type MemoryLimiter struct {
	// ID is the declared instance, e.g. "memory_limiter/aggressive".
	ID config.ID
	// Node anchors findings about the instance as a whole.
	Node *yaml.Node
	// Path is the dotted path of the instance, e.g. "processors.memory_limiter".
	Path string

	CheckInterval Setting
	LimitMiB      Setting
	LimitPercent  Setting
	SpikeMiB      Setting
	SpikePercent  Setting
}

// MemoryLimiters returns every declared memory_limiter. Instances are matched
// on their type, so "memory_limiter/aggressive" is covered too.
func MemoryLimiters(f *config.File) []MemoryLimiter {
	return ProcessorsOfType(f, MemoryLimiterType, readMemoryLimiter)
}

// Name renders the instance for a message, so two findings about the same
// container limit can be told apart.
func (m MemoryLimiter) Name() string { return "processor " + Quote(m.ID.String()) }

// At returns the value node of one of the instance's settings, falling back to
// the instance itself when the key is absent.
func (m MemoryLimiter) At(s Setting) *yaml.Node { return NodeOr(s.Node, m.Node) }

// HardLimit returns the number of bytes the limiter will actually enforce, or
// nothing when it cannot be worked out at all. limit_mib wins over
// limit_percentage, as it does upstream, and a percentage is only a number when
// the container's memory limit is known.
//
// A value resolved at runtime leaves the whole figure unknown: a partly known
// hard limit is worse than none, since every finding about it would be
// confident about a number nobody has yet.
func (m MemoryLimiter) HardLimit(env Environment) mo.Option[int64] {
	if m.LimitMiB.Unknown() || m.LimitPercent.Unknown() {
		return mo.None[int64]()
	}

	// A figure that does not fit in a byte count is left unknown rather than
	// wrapped: a wrapped product is a plausible-looking number that appears
	// nowhere in the config, and a finding quoting it would be worse than none.
	if m.LimitMiB.Positive() {
		if m.LimitMiB.Num > math.MaxInt64/mib {
			return mo.None[int64]()
		}

		return mo.Some(m.LimitMiB.Num * mib)
	}

	if m.LimitPercent.Positive() && env.MemoryLimit > 0 {
		if env.MemoryLimit > math.MaxInt64/m.LimitPercent.Num {
			return mo.None[int64]()
		}

		return mo.Some(env.MemoryLimit * m.LimitPercent.Num / WholePercent)
	}

	return mo.None[int64]()
}

func readMemoryLimiter(c config.Component) MemoryLimiter {
	lim := MemoryLimiter{
		ID:            c.ID,
		Node:          NodeOr(c.KeyNode, c.ValueNode),
		Path:          config.KindProcessor.Section() + "." + c.ID.String(),
		CheckInterval: Absent(),
		LimitMiB:      Absent(),
		LimitPercent:  Absent(),
		SpikeMiB:      Absent(),
		SpikePercent:  Absent(),
	}

	if c.ValueNode == nil || c.ValueNode.Kind != yaml.MappingNode {
		return lim
	}

	for _, e := range MapEntries(c.ValueNode, lim.Path) {
		switch e.Key {
		case "check_interval":
			lim.CheckInterval = ReadDuration(e.Node)
		case "limit_mib":
			lim.LimitMiB = ReadInt(e.Node)
		case "limit_percentage":
			lim.LimitPercent = ReadInt(e.Node)
		case "spike_limit_mib":
			lim.SpikeMiB = ReadInt(e.Node)
		case "spike_limit_percentage":
			lim.SpikePercent = ReadInt(e.Node)
		default:
			// Every other setting is the field schema's business.
		}
	}

	return lim
}
