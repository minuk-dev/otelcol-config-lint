package lint_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/minuk-dev/otelcol-config-lint/pkg/lint"
	"github.com/minuk-dev/otelcol-config-lint/pkg/quantity"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

func testPolicy() lint.EnvironmentPolicy {
	return lint.EnvironmentPolicy{
		Default: rule.Environment{Kubernetes: true, MemoryRequest: 512 * quantity.Mi, MemoryLimit: 512 * quantity.Mi},
		Overrides: []lint.EnvironmentOverride{
			{
				Paths: []string{"configs/agent-*.yaml"},
				Env: rule.Environment{
					Kubernetes: true, MemoryRequest: 256 * quantity.Mi, MemoryLimit: 256 * quantity.Mi,
				},
			},
			{
				Paths: []string{"configs/gateway/*.yaml", "gw.yaml"},
				Env:   rule.Environment{Kubernetes: true, MemoryRequest: 4 * quantity.Gi, MemoryLimit: 4 * quantity.Gi},
			},
			{
				Paths: []string{"configs/legacy/*.yaml"},
				Env:   rule.Environment{Kubernetes: false, MemoryRequest: 0, MemoryLimit: 0},
			},
		},
	}
}

func TestEnvironmentPolicyResolvesPerPath(t *testing.T) {
	t.Parallel()

	tests := map[string]int64{
		"configs/agent-node.yaml":     256 * quantity.Mi,
		"configs/gateway/main.yaml":   4 * quantity.Gi,
		"gw.yaml":                     4 * quantity.Gi,
		"configs/collector.yaml":      512 * quantity.Mi,
		"somewhere/else/config.yaml":  512 * quantity.Mi,
		"stdin":                       512 * quantity.Mi,
		filepath.Clean("./gw.yaml"):   4 * quantity.Gi,
		"configs/legacy/ancient.yaml": 0,
	}

	policy := testPolicy()
	for path, want := range tests {
		if got := policy.Resolve(path).MemoryLimit; got != want {
			t.Errorf("Resolve(%q) limit = %d, want %d", path, got, want)
		}
	}

	if policy.Resolve("configs/legacy/ancient.yaml").Known() {
		t.Error("an override that turns Kubernetes off should leave the environment unknown")
	}
}

func TestEnvironmentPolicyTakesTheFirstMatch(t *testing.T) {
	t.Parallel()

	policy := lint.EnvironmentPolicy{
		Default: rule.Environment{},
		Overrides: []lint.EnvironmentOverride{
			{Paths: []string{"*.yaml"}, Env: rule.Environment{Kubernetes: true, MemoryLimit: quantity.Gi}},
			{Paths: []string{"agent.yaml"}, Env: rule.Environment{Kubernetes: true, MemoryLimit: 2 * quantity.Gi}},
		},
	}

	if got := policy.Resolve("agent.yaml").MemoryLimit; got != quantity.Gi {
		t.Errorf("the first matching override should win, got %d", got)
	}
}

func TestZeroEnvironmentPolicyKnowsNothing(t *testing.T) {
	t.Parallel()

	var policy lint.EnvironmentPolicy

	if policy.Configured() {
		t.Error("a policy nobody configured should not claim to be")
	}

	if policy.Resolve("anything.yaml").Known() {
		t.Error("a policy nobody configured should resolve to an unknown environment")
	}
}

func TestEnvironmentPolicyRejectsAPatternThatCannotMatch(t *testing.T) {
	t.Parallel()

	policy := lint.EnvironmentPolicy{
		Default: rule.Environment{},
		Overrides: []lint.EnvironmentOverride{
			{Paths: []string{"good-*.yaml"}, Env: rule.Environment{}},
			{Paths: []string{"[bad"}, Env: rule.Environment{}},
		},
	}

	err := policy.Validate()
	if err == nil {
		t.Fatal("a malformed glob should be reported")
	}

	if !strings.Contains(err.Error(), "override 2") {
		t.Errorf("the error should say which override is wrong: %v", err)
	}
}

func TestLinterResolvesTheEnvironmentOfEveryFile(t *testing.T) {
	t.Parallel()

	// Enough memory for the gateway, far too little for the agent.
	const src = `
receivers: {otlp: }
processors:
  memory_limiter:
    check_interval: 1s
    limit_mib: 512
    spike_limit_mib: 128
exporters: {debug: }
service:
  pipelines:
    traces: {receivers: [otlp], processors: [memory_limiter], exporters: [debug]}
`

	policy := lint.EnvironmentPolicy{
		Default: rule.Environment{Kubernetes: true, MemoryRequest: 4 * quantity.Gi, MemoryLimit: 4 * quantity.Gi},
		Overrides: []lint.EnvironmentOverride{
			{
				Paths: []string{"agent-*.yaml"},
				Env: rule.Environment{
					Kubernetes: true, MemoryRequest: 256 * quantity.Mi, MemoryLimit: 256 * quantity.Mi,
				},
			},
		},
	}

	l := newLinter(t, lint.Options{Environment: policy.Resolve})

	if r := l.Lint(t.Context(), "agent-node.yaml", []byte(src)); !fires(r, "memory-limiter-sizing") {
		t.Errorf("the agent's limiter does not fit its container: %+v", r.Diagnostics)
	}

	if r := l.Lint(t.Context(), "gateway.yaml", []byte(src)); fires(r, "memory-limiter-sizing") {
		t.Errorf("the gateway has room for the same limiter: %+v", r.Diagnostics)
	}
}

func TestLinterWithoutAResolverKnowsNoEnvironment(t *testing.T) {
	t.Parallel()

	src := strings.Replace(good, "limit_mib: 512", "limit_mib: 4096", 1)
	if r := newLinter(t, lint.Options{}).Lint(t.Context(), "x.yaml", []byte(src)); fires(r, "memory-limiter-sizing") {
		t.Errorf("sizing needs an environment nobody gave: %+v", r.Diagnostics)
	}
}

func fires(r lint.Result, name string) bool {
	for _, d := range r.Diagnostics {
		if d.Rule == name {
			return true
		}
	}

	return false
}
