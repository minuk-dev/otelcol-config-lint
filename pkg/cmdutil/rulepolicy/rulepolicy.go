// Package rulepolicy is which rules run, at what level, and with what settings
// of their own, once a command's rule flags and the settings file have both had
// their say. "run" and "list rules" both resolve one: what the listing prints
// is the policy a run would use.
//
// The flags stay with the commands that declare them; what arrives here is a
// Selection, which is only what they said.
package rulepolicy

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/cmdutil"
	"github.com/minuk-dev/otelcol-config-lint/pkg/cmdutil/settings"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
	"github.com/minuk-dev/otelcol-config-lint/pkg/ruleset"
	"github.com/minuk-dev/otelcol-config-lint/pkg/sets"
)

// Errors reported for a rule selection that cannot be resolved.
var (
	// ErrUnknownRule names a rule that does not exist.
	ErrUnknownRule = cmdutil.NewUsageError("unknown rule")
	// ErrBadSeverityPair reports a --severity argument that is not rule=level.
	ErrBadSeverityPair = cmdutil.NewUsageError("not in rule=level form")
	// ErrUnknownDefault reports a rules.default outside the sets on offer.
	ErrUnknownDefault = cmdutil.NewUsageError("unknown rule set")
	// ErrEnabledAndDisabled reports a rule named on both sides at once.
	ErrEnabledAndDisabled = cmdutil.NewUsageError("is both enabled and disabled")
)

// Policy is which rules run and at what level. It is resolved in the order
// the fields are written: the default set, then enable, then disable, then the
// explicit levels -- so a rule given a severity runs at it whatever came
// before, which is the only reading under which writing one is never a no-op.
type Policy struct {
	// set is the set to start from: settings.DefaultAll, settings.DefaultNone, or "" for the
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

// Severities turns the policy into the severity map the linter takes, where a
// level of diag.Off is a rule that does not run.
func (p Policy) Severities() (map[string]diag.Severity, error) {
	out := map[string]diag.Severity{}

	switch p.set {
	case "", settings.DefaultAll:
	case settings.DefaultNone:
		for _, r := range ruleset.All() {
			out[r.Name()] = diag.Off
		}
	default:
		return nil, fmt.Errorf("rules.default: %w %q (want %s or %s)",
			ErrUnknownDefault, p.set, settings.DefaultAll, settings.DefaultNone)
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

// Rules returns the rule set with each rule's own settings block applied. A
// block written for a rule that does not exist is reported here, because
// rule.Configure cannot tell a missing rule from a misspelled one.
func (p Policy) Rules() ([]rule.Rule, error) {
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

// Selection is what a command's rule flags hold: the set to start from and the
// rules moved in and out of it. The flags themselves belong to the command that
// declares them; this is only what they said.
type Selection struct {
	// Default is the set to start from, "" meaning the file decides.
	Default string
	// Enable and Disable name rules to move in and out of that set.
	Enable  []string
	Disable []string
	// Severity holds rule=level pairs.
	Severity []string
}

// New builds the policy from a selection and the rules block. The rule lists
// merge rather than replace: the file states the project policy and a flag
// adds to it for a single run, which is how -E and -D read next to a committed
// config.
func New(s *settings.File, sel Selection) Policy {
	// File pairs are listed first so a flag that names the same rule wins.
	severity := make([]string, 0, len(s.Rules.Severity)+len(sel.Severity))
	for _, name := range sets.List(sets.KeySet(s.Rules.Severity)) {
		severity = append(severity, name+"="+s.Rules.Severity[name])
	}

	set := sel.Default
	if set == "" {
		set = s.Rules.Default
	}

	return Policy{
		set:      set,
		enable:   append(trimAll(s.Rules.Enable), trimAll(sel.Enable)...),
		disable:  append(trimAll(s.Rules.Disable), trimAll(sel.Disable)...),
		severity: append(severity, trimAll(sel.Severity)...),
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
