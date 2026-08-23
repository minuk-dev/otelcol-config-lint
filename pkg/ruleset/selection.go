package ruleset

import (
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
	"github.com/minuk-dev/otelcol-config-lint/pkg/sets"
)

// Errors reported for a selection that cannot be resolved. Every one of them
// is a name that is not in this set, which is why they are reported here: this
// package is the only one that knows what the names are.
var (
	// ErrUnknownRule names a rule that does not exist.
	ErrUnknownRule = errors.New("unknown rule")
	// ErrBadSeverityPair reports a severity entry that is not rule=level.
	ErrBadSeverityPair = errors.New("not in rule=level form")
	// ErrUnknownDefault reports a starting set outside the ones on offer.
	ErrUnknownDefault = errors.New("unknown rule set")
	// ErrEnabledAndDisabled reports a rule named on both sides at once.
	ErrEnabledAndDisabled = errors.New("is both enabled and disabled")
)

// The sets a selection can start from. They mirror golangci-lint's, minus the
// curated middle: every rule here is one a collector config should pass, so
// there is no subset to start from that is not simply all of them.
const (
	// DefaultAll runs every registered rule, which is what a selection that
	// says nothing does.
	DefaultAll = "all"
	// DefaultNone runs only what Enable names.
	DefaultNone = "none"
)

// Selection is which rules to run, at what level, and with what settings of
// their own -- the answer a caller has arrived at, however it arrived at it.
// Resolve is what holds it to the rules that exist.
type Selection struct {
	// Default is the set to start from: DefaultAll, DefaultNone, or "" for the
	// former.
	Default string
	// Enable and Disable name rules. A rule in both is an error rather than a
	// silent win for one of them.
	Enable  []string
	Disable []string
	// Severity holds rule=level pairs, later ones winning.
	Severity []string
	// Settings holds each rule's own block, keyed by rule name.
	Settings map[string]yaml.Node
}

// Resolved is a selection that has been held to the rules that exist: the set
// to run, and the level each one runs at.
type Resolved struct {
	// Rules is the set with each rule's own settings block applied.
	Rules []rule.Rule
	// Severities is the level each named rule runs at, where diag.Off is a
	// rule that does not run. A rule absent from the map runs at its own.
	Severities map[string]diag.Severity
}

// Resolve turns a selection into the rules and the levels a run uses, reporting
// every name it does not recognise.
//
// It is applied in the order the fields are written: the default set, then
// Enable, then Disable, then the explicit levels -- so a rule given a severity
// runs at it whatever came before, which is the only reading under which
// writing one is never a no-op.
func Resolve(sel Selection) (Resolved, error) {
	var none Resolved

	severities, err := sel.severities()
	if err != nil {
		return none, err
	}

	rules, err := sel.rules()
	if err != nil {
		return none, err
	}

	return Resolved{Rules: rules, Severities: severities}, nil
}

// severities is the level map, where diag.Off is a rule that does not run.
func (s Selection) severities() (map[string]diag.Severity, error) {
	out := map[string]diag.Severity{}

	switch s.Default {
	case "", DefaultAll:
	case DefaultNone:
		for _, r := range All() {
			out[r.Name()] = diag.Off
		}
	default:
		return nil, fmt.Errorf("rules.default: %w %q (want %s or %s)",
			ErrUnknownDefault, s.Default, DefaultAll, DefaultNone)
	}

	both := sets.New(s.Enable...).Intersection(sets.New(s.Disable...))
	if both.Len() > 0 {
		return nil, fmt.Errorf("%q %w", sets.List(both)[0], ErrEnabledAndDisabled)
	}

	for _, name := range s.Enable {
		r, ok := Lookup(name)
		if !ok {
			return nil, fmt.Errorf("enable: %w %q", ErrUnknownRule, name)
		}

		out[name] = r.Severity()
	}

	for _, name := range s.Disable {
		if _, ok := Lookup(name); !ok {
			return nil, fmt.Errorf("disable: %w %q", ErrUnknownRule, name)
		}

		out[name] = diag.Off
	}

	err := s.applyLevels(out)
	if err != nil {
		return nil, err
	}

	return out, nil
}

// applyLevels folds the explicit rule=level pairs into the map, which is the
// last word on what a rule runs at.
func (s Selection) applyLevels(out map[string]diag.Severity) error {
	for _, pair := range s.Severity {
		name, level, found := strings.Cut(pair, "=")
		if !found {
			return fmt.Errorf("severity: %q is %w", pair, ErrBadSeverityPair)
		}

		if _, ok := Lookup(name); !ok {
			return fmt.Errorf("severity: %w %q", ErrUnknownRule, name)
		}

		sev, err := diag.ParseSeverity(level)
		if err != nil {
			return fmt.Errorf("severity %s: %w", name, err)
		}

		out[name] = sev
	}

	return nil
}

// rules returns the set with each rule's own settings block applied. A block
// written for a rule that does not exist is reported here, because
// rule.Configure cannot tell a missing rule from a misspelled one.
func (s Selection) rules() ([]rule.Rule, error) {
	for _, name := range sets.List(sets.KeySet(s.Settings)) {
		if _, ok := Lookup(name); !ok {
			return nil, fmt.Errorf("rules.settings: %w %q", ErrUnknownRule, name)
		}
	}

	configured, err := rule.Configure(All(), s.Settings)
	if err != nil {
		return nil, fmt.Errorf("rules.settings: %w", err)
	}

	return configured, nil
}
