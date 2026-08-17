package rule

import (
	"errors"
	"fmt"
	"slices"

	"gopkg.in/yaml.v3"
)

// ErrNoSettings reports a settings block written for a rule that reads none.
var ErrNoSettings = errors.New("rule takes no settings")

// Configurable is implemented by rules that read a block of their own from the
// settings file, the way golangci-lint's linters take a settings block under
// their name. A rule that does not implement it takes no settings, and writing
// a block for it is reported rather than quietly ignored: a knob that does
// nothing is worse than a knob that is missing.
type Configurable interface {
	Rule

	// WithSettings returns the rule the given block describes. It must not
	// modify the receiver: All builds the set fresh on every call, and a rule
	// that configured itself in place would leak one run's policy into the
	// next. The node is the mapping written under the rule's name.
	WithSettings(node *yaml.Node) (Rule, error)
}

// Configure applies the per-rule settings blocks to a rule set and returns the
// configured set, leaving the one it was given untouched.
//
// Blocks are keyed by rule name. A name the set does not hold is skipped:
// whether it is a rule that was disabled or a rule that was misspelled is a
// question the caller can answer and this package cannot.
func Configure(rules []Rule, settings map[string]yaml.Node) ([]Rule, error) {
	if len(settings) == 0 {
		return rules, nil
	}

	out := slices.Clone(rules)

	for i, r := range out {
		node, ok := settings[r.Name()]
		if !ok {
			continue
		}

		conf, ok := r.(Configurable)
		if !ok {
			return nil, fmt.Errorf("%s: %w", r.Name(), ErrNoSettings)
		}

		configured, err := conf.WithSettings(&node)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", r.Name(), err)
		}

		out[i] = configured
	}

	return out, nil
}
