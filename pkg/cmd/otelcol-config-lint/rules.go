package otelcolconfiglint

import (
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
	"github.com/minuk-dev/otelcol-config-lint/pkg/ruleset"
	"github.com/minuk-dev/otelcol-config-lint/pkg/sets"
)

// Errors reported for a rule selection that cannot be resolved.
var (
	// ErrUnknownRule names a rule that does not exist.
	ErrUnknownRule = errors.New("unknown rule")
	// ErrBadSeverityPair reports a --severity argument that is not rule=level.
	ErrBadSeverityPair = errors.New("not in rule=level form")
	// ErrUnknownDefault reports a rules.default outside the sets on offer.
	ErrUnknownDefault = errors.New("unknown rule set")
	// ErrEnabledAndDisabled reports a rule named on both sides at once.
	ErrEnabledAndDisabled = errors.New("is both enabled and disabled")
)

// The sets rules.default can name. They mirror golangci-lint's, minus the
// curated middle: every rule here is one a collector config should pass, so
// there is no subset to start from that is not simply all of them.
const (
	// defaultAll runs every registered rule, which is what a run that says
	// nothing does.
	defaultAll = "all"
	// defaultNone runs only what enable names.
	defaultNone = "none"
)

// rulePolicy is which rules run and at what level. It is resolved in the order
// the fields are written: the default set, then enable, then disable, then the
// explicit levels -- so a rule given a severity runs at it whatever came
// before, which is the only reading under which writing one is never a no-op.
type rulePolicy struct {
	// set is the set to start from: defaultAll, defaultNone, or "" for the
	// former.
	set string
	// enable and disable name rules. A rule in both is an error rather than a
	// silent win for one of them.
	enable  []string
	disable []string
	// severity holds rule=level pairs, later ones winning.
	severity []string
	// settings holds each rule's own block, keyed by rule name.
	settings map[string]yaml.Node
}

// resolve turns the policy into the severity map the linter takes, where a
// level of diag.Off is a rule that does not run.
func (p rulePolicy) resolve() (map[string]diag.Severity, error) {
	out := map[string]diag.Severity{}

	switch p.set {
	case "", defaultAll:
	case defaultNone:
		for _, r := range ruleset.All() {
			out[r.Name()] = diag.Off
		}
	default:
		return nil, fmt.Errorf("rules.default: %w %q (want %s or %s)",
			ErrUnknownDefault, p.set, defaultAll, defaultNone)
	}

	both := sets.New(p.enable...).Intersection(sets.New(p.disable...))
	if both.Len() > 0 {
		return nil, fmt.Errorf("%q %w", sets.List(both)[0], ErrEnabledAndDisabled)
	}

	for _, name := range p.enable {
		r, ok := ruleset.Lookup(name)
		if !ok {
			return nil, fmt.Errorf("enable: %w %q", ErrUnknownRule, name)
		}

		out[name] = r.Severity()
	}

	for _, name := range p.disable {
		if _, ok := ruleset.Lookup(name); !ok {
			return nil, fmt.Errorf("disable: %w %q", ErrUnknownRule, name)
		}

		out[name] = diag.Off
	}

	for _, pair := range p.severity {
		name, level, found := strings.Cut(pair, "=")
		if !found {
			return nil, fmt.Errorf("severity: %q is %w", pair, ErrBadSeverityPair)
		}

		if _, ok := ruleset.Lookup(name); !ok {
			return nil, fmt.Errorf("severity: %w %q", ErrUnknownRule, name)
		}

		sev, err := diag.ParseSeverity(level)
		if err != nil {
			return nil, fmt.Errorf("severity %s: %w", name, err)
		}

		out[name] = sev
	}

	return out, nil
}

// rules returns the rule set with each rule's own settings block applied. A
// block written for a rule that does not exist is reported here, because
// rule.Configure cannot tell a missing rule from a misspelled one.
func (p rulePolicy) rules() ([]rule.Rule, error) {
	for _, name := range sets.List(sets.KeySet(p.settings)) {
		if _, ok := ruleset.Lookup(name); !ok {
			return nil, fmt.Errorf("rules.settings: %w %q", ErrUnknownRule, name)
		}
	}

	configured, err := rule.Configure(ruleset.All(), p.settings)
	if err != nil {
		return nil, fmt.Errorf("rules.settings: %w", err)
	}

	return configured, nil
}

// rulePolicy builds the policy from the flags and the settings file. The rule
// lists merge rather than replace: the file states the project policy and a
// flag adds to it for a single run, which is how -E and -D read next to a
// committed config.
func (o *Options) rulePolicy(s *settings) rulePolicy {
	// File pairs are listed first so a flag that names the same rule wins.
	severity := make([]string, 0, len(s.Rules.Severity)+len(o.severity))
	for _, name := range sets.List(sets.KeySet(s.Rules.Severity)) {
		severity = append(severity, name+"="+s.Rules.Severity[name])
	}

	set := o.ruleDefault
	if set == "" {
		set = s.Rules.Default
	}

	return rulePolicy{
		set:      set,
		enable:   append(trimAll(s.Rules.Enable), trimAll(o.enable)...),
		disable:  append(trimAll(s.Rules.Disable), trimAll(o.disable)...),
		severity: append(severity, trimAll(o.severity)...),
		settings: s.Rules.Settings,
	}
}

// trimAll drops the blank entries a comma-separated flag or a hand-written list
// can carry, so "a,,b" and an empty flag both mean what they look like.
func trimAll(in []string) []string {
	var out []string

	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}

	return out
}
