package otelcolconfiglint

import (
	"errors"
	"fmt"
	"strings"

	"github.com/minuk-dev/otelcol-config-lint/pkg/lint"
	"github.com/minuk-dev/otelcol-config-lint/pkg/quantity"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// ErrNoOverridePaths reports an environment override that matches nothing.
var ErrNoOverridePaths = errors.New("an override needs at least one path pattern")

// kubernetesSettings is the "kubernetes" block of a settings file: what the
// pods these configs run in look like, which is what the sizing rules check
// against.
//
// The block states the defaults, and overrides state the files that are a
// different workload -- an agent DaemonSet at 256Mi next to a gateway
// Deployment at 4Gi. There is deliberately no flag form of the overrides: a
// path-to-limits table belongs in a file.
type kubernetesSettings struct {
	// Enabled says the configs run in Kubernetes. When it is not set, writing
	// either memory number is taken to mean the same thing.
	Enabled *bool `yaml:"enabled"`
	// MemoryRequest and MemoryLimit are the container's resources, written as
	// a Kubernetes quantity such as "512Mi".
	MemoryRequest string `yaml:"memoryRequest"`
	MemoryLimit   string `yaml:"memoryLimit"`
	// Overrides are matched in order and the first match wins.
	Overrides []kubernetesOverride `yaml:"overrides"`
}

// kubernetesOverride is one path-matched entry of the kubernetes block. It
// replaces the defaults for the files it matches rather than merging with
// them, so what a file resolves to is stated in one place.
type kubernetesOverride struct {
	// Paths are glob patterns, matched against both the whole path and the
	// base name, exactly as the exclude patterns are.
	Paths         []string `yaml:"paths"`
	Enabled       *bool    `yaml:"enabled"`
	MemoryRequest string   `yaml:"memoryRequest"`
	MemoryLimit   string   `yaml:"memoryLimit"`
}

// environmentPolicy builds the per-path environment from the flags and the
// settings file. The flags set the defaults only; they are the single-file
// convenience, and the file is what a repository of configs commits.
func (o *Options) environmentPolicy() (lint.EnvironmentPolicy, error) {
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
