package lint

import (
	"fmt"

	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
	"github.com/minuk-dev/otelcol-config-lint/pkg/scanner"
)

// EnvironmentPolicy resolves the environment a config file is deployed into.
//
// A run is not one deployment: an agent DaemonSet and a gateway Deployment sit
// in the same directory and are checked in the same run, so the numbers the
// sizing rules need belong to a path, not to the run. A path that matches
// nothing gets the defaults, and defaults that were never set say nothing,
// which leaves those rules quiet.
//
// The zero value resolves every path to an unknown environment.
type EnvironmentPolicy struct {
	// Default applies to any path no override matches.
	Default rule.Environment
	// Overrides are consulted in order and the first match wins. A matching
	// override replaces the defaults; it does not merge with them.
	Overrides []EnvironmentOverride
}

// EnvironmentOverride is one path-matched entry of a policy.
type EnvironmentOverride struct {
	// Paths are glob patterns, matched by scanner.Match against both the whole
	// path and the base name, exactly as the exclude patterns are.
	Paths []string
	// Env is what a path matching any of the patterns resolves to.
	Env rule.Environment
}

// Validate reports the first pattern that cannot be used as a glob. It is
// meant to be called once, before linting: a pattern nothing can match would
// otherwise look like an override that simply did not apply.
func (p EnvironmentPolicy) Validate() error {
	for i, over := range p.Overrides {
		for _, pattern := range over.Paths {
			err := scanner.CheckPattern(pattern)
			if err != nil {
				return fmt.Errorf("override %d: %w", i+1, err)
			}
		}
	}

	return nil
}

// Resolve returns the environment for a config file path. It reads nothing but
// the policy, which is why LintAll's workers can all call it at once; a policy
// must therefore not be changed once linting has started.
//
// Config read from standard input is reported under a name no glob is meant to
// match, so it resolves to the defaults.
func (p EnvironmentPolicy) Resolve(path string) rule.Environment {
	for _, over := range p.Overrides {
		for _, pattern := range over.Paths {
			if scanner.Match(pattern, path) {
				return over.Env
			}
		}
	}

	return p.Default
}

// Configured reports whether the policy can resolve anything at all, so a
// caller can stay quiet about environments nobody asked for.
func (p EnvironmentPolicy) Configured() bool {
	return p.Default.Known() || len(p.Overrides) > 0
}
