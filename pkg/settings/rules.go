package settings

import (
	"strings"

	"github.com/minuk-dev/otelcol-config-lint/pkg/ruleset"
	"github.com/minuk-dev/otelcol-config-lint/pkg/sets"
)

// RuleSelection merges the rules block with what a command's rule flags said,
// which is the selection ruleset.Resolve is held to.
//
// The lists merge rather than replace: the file states the project policy and
// a flag adds to it for a single run, which is how -E and -D read next to a
// committed config. The flags themselves belong to the command that declares
// them; what arrives here is only what they hold.
func (s *File) RuleSelection(flags ruleset.Selection) ruleset.Selection {
	// File pairs are listed first so a flag that names the same rule wins.
	severity := make([]string, 0, len(s.Rules.Severity)+len(flags.Severity))
	for _, name := range sets.List(sets.KeySet(s.Rules.Severity)) {
		severity = append(severity, name+"="+s.Rules.Severity[name])
	}

	set := flags.Default
	if set == "" {
		set = s.Rules.Default
	}

	return ruleset.Selection{
		Default:  set,
		Enable:   append(trimAll(s.Rules.Enable), trimAll(flags.Enable)...),
		Disable:  append(trimAll(s.Rules.Disable), trimAll(flags.Disable)...),
		Severity: append(severity, trimAll(flags.Severity)...),
		// Per-rule settings are the file's alone: a block of options is not
		// something a flag can carry.
		Settings: s.Rules.Settings,
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
