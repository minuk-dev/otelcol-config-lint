package run

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/minuk-dev/otelcol-config-lint/pkg/cmdutil"
	"github.com/minuk-dev/otelcol-config-lint/pkg/cmdutil/settings"
	"github.com/minuk-dev/otelcol-config-lint/pkg/lint"
	"github.com/minuk-dev/otelcol-config-lint/pkg/quantity"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// kubernetesFlag is the flag that says the configs run in a pod, named here
// because the tri-state below has to ask whether it was given.
const kubernetesFlag = "kubernetes"

// ErrNoOverridePaths reports an environment override that matches nothing.
var ErrNoOverridePaths = cmdutil.NewUsageError("an override needs at least one path pattern")

// environmentFlags describe the pod the configs run in: --kubernetes and the
// two memory numbers. Only "run" takes them, because only a lint run judges a
// config against what it runs in.
type environmentFlags struct {
	// flags
	kubernetes    bool
	memoryRequest string
	memoryLimit   string

	// internal state
	// enabled is what the flag or the settings file said about running in
	// Kubernetes; nil when neither said anything, which leaves the answer to
	// be read from the memory numbers.
	enabled *bool
	// overrides are the per-path environments, which only the settings file
	// can state.
	overrides []settings.KubernetesOverride
}

// register declares the environment flags on cmd.
func (f *environmentFlags) register(cmd *cobra.Command) {
	flags := cmd.Flags()

	flags.BoolVar(&f.kubernetes, kubernetesFlag, false, "the config runs in a Kubernetes pod")
	flags.StringVar(&f.memoryRequest, "memory-request", "", "container memory request, e.g. 256Mi")
	flags.StringVar(&f.memoryLimit, "memory-limit", "", "container memory limit, e.g. 512Mi")
}

// applySettings folds the kubernetes block into the flags. The flags set the
// defaults only; the per-path overrides are the file's alone.
func (f *environmentFlags) applySettings(s *settings.File, fold settings.Fold) {
	fold.Str("memory-request", &f.memoryRequest, s.Run.Kubernetes.MemoryRequest)
	fold.Str("memory-limit", &f.memoryLimit, s.Run.Kubernetes.MemoryLimit)

	// The deployment environment is a tri-state: the flag wins, then the file,
	// and with neither the memory numbers speak for themselves.
	switch {
	case fold.Changed(kubernetesFlag):
		f.enabled = &f.kubernetes
	case s.Run.Kubernetes.Enabled != nil:
		f.enabled = s.Run.Kubernetes.Enabled
	}

	f.overrides = s.Run.Kubernetes.Overrides
}

// policy builds the per-path environment from the flags and the settings
// file. The flags are the single-file convenience, and the file is
// what a repository of configs commits.
func (f *environmentFlags) policy() (lint.EnvironmentPolicy, error) {
	var none lint.EnvironmentPolicy

	fallback, err := environmentOf(f.enabled, f.memoryRequest, f.memoryLimit)
	if err != nil {
		return none, fmt.Errorf("kubernetes: %w", err)
	}

	policy := lint.EnvironmentPolicy{Default: fallback, Overrides: nil}

	for i, over := range f.overrides {
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
