package rule

import (
	"strconv"
	"time"

	"github.com/samber/lo"
	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
)

// Setting is one value read out of a component's settings block, for the rules
// that compare one setting against another rather than against a schema.
type Setting struct {
	// Node is the value node, or nil when the key was not written.
	Node *yaml.Node
	// Present reports that the key was written at all.
	Present bool
	// Known reports that Num holds the value. A confmap expansion such as
	// ${env:LIMIT} is present but never known, and neither is a value of the
	// wrong type, which invalid-value reports on its own.
	Known bool
	// Num is the value: a count for the integer settings, and nanoseconds for
	// a duration.
	Num int64
}

// Absent is the setting a key nobody wrote reads as.
func Absent() Setting { return Setting{Node: nil, Present: false, Known: false, Num: 0} }

// ReadInt reads an integer setting, in the base the collector's own decoder
// reads it: confmap unmarshals through yaml.v3, which resolves 0x400 as 1024,
// 8_192 as 8192, and a leading zero as octal, so 0100 is 64 and not a hundred.
// Reading base 10 here would compare, and quote back, a number the collector
// never sees.
//
// A value nothing can know before the collector starts -- an expansion, or text
// of the wrong type -- is present but not known, which stops every check that
// would need the number.
func ReadInt(node *yaml.Node) Setting {
	out := Setting{Node: node, Present: true, Known: false, Num: 0}
	if node == nil || node.Kind != yaml.ScalarNode || HasExpansion(node.Value) {
		return out
	}

	num, err := strconv.ParseInt(node.Value, 0, 64)
	if err != nil {
		return out
	}

	out.Known, out.Num = true, num

	return out
}

// ReadDuration reads a duration setting, holding it in nanoseconds.
func ReadDuration(node *yaml.Node) Setting {
	out := Setting{Node: node, Present: true, Known: false, Num: 0}
	if node == nil || node.Kind != yaml.ScalarNode || HasExpansion(node.Value) {
		return out
	}

	dur, err := time.ParseDuration(node.Value)
	if err != nil {
		return out
	}

	out.Known, out.Num = true, int64(dur)

	return out
}

// Positive reports a value that is known and above zero.
func (s Setting) Positive() bool { return s.Known && s.Num > 0 }

// Unknown reports a setting that was written but whose value cannot be read
// here, so no check that needs the number can run.
func (s Setting) Unknown() bool { return s.Present && !s.Known }

// ProcessorsOfType returns every declared processor of one type, read into
// whatever the caller needs. Matching on the type rather than the whole id is
// what covers named instances such as "batch/traces".
func ProcessorsOfType[T any](f *config.File, typ string, read func(config.Component) T) []T {
	sec := f.Sections[config.KindProcessor]
	if sec == nil {
		return nil
	}

	declared := lo.Filter(sec.Components, func(c config.Component, _ int) bool { return c.ID.Type == typ })

	return lo.Map(declared, func(c config.Component, _ int) T { return read(c) })
}
