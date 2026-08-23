package run

import (
	"fmt"
	"strings"

	"github.com/minuk-dev/otelcol-config-lint/pkg/lint"
	"github.com/minuk-dev/otelcol-config-lint/pkg/quantity"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// kubernetesFlag is the flag that says the configs run in a pod. It is named
// because resolving the environment has to ask whether it was given, and a
// flag looked up by a misspelled name is a question that answers no.
const kubernetesFlag = "kubernetes"

// environmentPolicy builds the per-path environment from the flags and the
// settings file. The flags are the single-file convenience, and the file is
// what a repository of configs commits.
func (o *options) environmentPolicy() (lint.EnvironmentPolicy, error) {
	var none lint.EnvironmentPolicy

	fallback, err := environmentOf(o.kubernetesEnabled, o.memoryRequest, o.memoryLimit)
	if err != nil {
		return none, fmt.Errorf("kubernetes: %w", err)
	}

	policy := lint.EnvironmentPolicy{Default: fallback, Overrides: nil}

	for i, over := range o.kubernetesOverrides {
		where := fmt.Sprintf("kubernetes.overrides[%d]", i)

		if len(over.Paths) == 0 {
			return none, fmt.Errorf("%s: %w", where, ErrNoOverridePaths)
		}

		env, envErr := environmentOf(over.Enabled, over.MemoryRequest, over.MemoryLimit)
		if envErr != nil {
			return none, fmt.Errorf("%s: %w", where, envErr)
		}

		policy.Overrides = append(policy.Overrides, lint.EnvironmentOverride{Paths: over.Paths, Env: env})
	}

	err = policy.Validate()
	if err != nil {
		return none, fmt.Errorf("kubernetes.overrides: %w", err)
	}

	return policy, nil
}

// environmentOf turns one set of written values into an environment. An
// unstated "enabled" is read from the memory numbers: a block that says how
// much memory the container has is a block about a container.
func environmentOf(enabled *bool, request, limit string) (rule.Environment, error) {
	req, err := parseSize("memoryRequest", request)
	if err != nil {
		return rule.Environment{}, err
	}

	lim, err := parseSize("memoryLimit", limit)
	if err != nil {
		return rule.Environment{}, err
	}

	on := request != "" || limit != ""
	if enabled != nil {
		on = *enabled
	}

	return rule.Environment{Kubernetes: on, MemoryRequest: req, MemoryLimit: lim}, nil
}

// parseSize reads a Kubernetes quantity, treating an unwritten value as
// unknown rather than as zero bytes.
func parseSize(name, text string) (int64, error) {
	if text == "" {
		return 0, nil
	}

	size, err := quantity.Parse(text)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}

	return size, nil
}

// describeEnvironment renders what a file resolved to, so --verbose can answer
// "why was this file not checked" without anyone re-reading the glob list.
func describeEnvironment(env rule.Environment) string {
	if !env.Kubernetes {
		return "no deployment environment"
	}

	parts := []string{"kubernetes"}
	if env.MemoryRequest > 0 {
		parts = append(parts, "memory request "+quantity.Format(env.MemoryRequest))
	}

	if env.MemoryLimit > 0 {
		parts = append(parts, "memory limit "+quantity.Format(env.MemoryLimit))
	}

	return strings.Join(parts, ", ")
}
