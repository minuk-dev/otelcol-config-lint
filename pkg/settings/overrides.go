package settings

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/ruleset"
	"github.com/minuk-dev/otelcol-config-lint/pkg/scanner"
)

var errOverridePaths = errors.New("paths must contain nonempty glob patterns")

// RulePolicy validates file overrides and captures an immutable policy resolver.
// Global selection controls which rules can run; this policy only relevels them.
// Explicit CLI enables/disables bypass file exceptions for those names, and CLI
// severity wins over both file exceptions and the global settings severity.
func (s *File) RulePolicy(flags ruleset.Selection) (func(string) map[string]diag.Severity, error) {
	type entry struct {
		paths  []string
		levels map[string]diag.Severity
	}

	entries := make([]entry, 0, len(s.Rules.Overrides))
	for i, over := range s.Rules.Overrides {
		levels, err := overrideLevels(over)
		if err != nil {
			return nil, fmt.Errorf("rules.overrides[%d]: %w", i, err)
		}

		for _, name := range append(trimAll(flags.Enable), trimAll(flags.Disable)...) {
			delete(levels, name)
		}

		entries = append(entries, entry{paths: slices.Clone(over.Paths), levels: levels})
	}

	defaults, err := overrideLevels(RuleOverride{Paths: []string{"*"}, Disable: nil, Severity: s.Rules.Severity})
	if err != nil {
		return nil, err
	}

	cli, err := ruleset.Resolve(ruleset.Selection{Severity: trimAll(flags.Severity)})
	if err != nil {
		return nil, err
	}

	return func(path string) map[string]diag.Severity {
		levels := maps.Clone(defaults)

		if path != scanner.StdinMarker {
			for _, over := range entries {
				if slices.ContainsFunc(over.paths, func(pattern string) bool { return scanner.Match(pattern, path) }) {
					maps.Copy(levels, over.levels)

					break
				}
			}
		}

		maps.Copy(levels, cli.Severities)

		return levels
	}, nil
}

func overrideLevels(over RuleOverride) (map[string]diag.Severity, error) {
	if len(over.Paths) == 0 {
		return nil, errOverridePaths
	}

	for _, pattern := range over.Paths {
		if strings.TrimSpace(pattern) == "" {
			return nil, errOverridePaths
		}

		err := scanner.CheckPattern(pattern)
		if err != nil {
			return nil, err
		}
	}

	severity := make([]string, 0, len(over.Severity))
	for name, level := range over.Severity {
		severity = append(severity, name+"="+level)
	}

	slices.Sort(severity)

	resolved, err := ruleset.Resolve(ruleset.Selection{Disable: over.Disable, Severity: severity})
	if err != nil {
		return nil, err
	}

	return resolved.Severities, nil
}
