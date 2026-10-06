package memorylimitersizing_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/quantity"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/memorylimitersizing"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/ruletest"
)

// checkIn runs the rule over src as deployed in the given environment.
func checkIn(t *testing.T, src string, env rule.Environment) diag.Diagnostics {
	t.Helper()

	found, err := ruletest.RunWith(memorylimitersizing.New(), src, ruletest.Options{Env: env})
	require.NoError(t, err, "parse")

	return found
}

// limiter wraps memory_limiter settings in a config that is otherwise clean.
// The settings are written already indented by four spaces.
func limiter(settings string) string {
	return `
receivers: {otlp: }
processors:
  memory_limiter:
` + settings + `
exporters: {debug: }
service:
  pipelines:
    traces: {receivers: [otlp], processors: [memory_limiter], exporters: [debug]}
`
}

// sized is a memory_limiter with a fixed limit, for the sizing tests.
func sized(limitMiB string) string {
	return limiter("    check_interval: 1s\n    limit_mib: " + limitMiB + "\n    spike_limit_mib: 64")
}

func TestMemoryLimiterSizing(t *testing.T) {
	t.Parallel()

	container := rule.Environment{Kubernetes: true, MemoryRequest: 512 * quantity.Mi, MemoryLimit: 512 * quantity.Mi}

	tests := map[string]struct {
		src  string
		env  rule.Environment
		want diag.Severity
		says string
	}{
		"a limit at the container's": {
			src: sized("512"), env: container,
			want: diag.Error, says: "before the limiter engages",
		},
		"a limit above the container's": {
			src: sized("1024"), env: container,
			want: diag.Error, says: "before the limiter engages",
		},
		"no headroom for the process itself": {
			src: sized("480"), env: container,
			want: diag.Warning, says: "leaving",
		},
		"above the documented ceiling": {
			src: sized("440"), env: container,
			want: diag.Warning, says: "above 80%",
		},
		"a percentage of a container with no limit": {
			src:  limiter("    check_interval: 1s\n    limit_percentage: 80"),
			env:  rule.Environment{Kubernetes: true, MemoryRequest: 512 * quantity.Mi, MemoryLimit: 0},
			want: diag.Warning, says: "no memory limit",
		},
		"a percentage above the ceiling": {
			src:  limiter("    check_interval: 1s\n    limit_percentage: 90"),
			env:  container,
			want: diag.Warning, says: "above 80%",
		},
		"a fixed limit overrides a percentage of the container": {
			src:  limiter("    check_interval: 1s\n    limit_mib: 512\n    limit_percentage: 50"),
			env:  container,
			want: diag.Error, says: "before the limiter engages",
		},
		"more than the pod asked for": {
			src:  sized("400"),
			env:  rule.Environment{Kubernetes: true, MemoryRequest: 256 * quantity.Mi, MemoryLimit: 512 * quantity.Mi},
			want: diag.Info, says: "memory request",
		},
		"a fixed limit with a percentage still exceeds the request": {
			src:  limiter("    check_interval: 1s\n    limit_mib: 400\n    limit_percentage: 80"),
			env:  rule.Environment{Kubernetes: true, MemoryRequest: 256 * quantity.Mi, MemoryLimit: 0},
			want: diag.Info, says: "memory request",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			found := checkIn(t, tt.src, tt.env)
			if !ruletest.Reports(found, tt.says) {
				t.Fatalf("no finding mentioned %q; got %+v", tt.says, found)
			}

			for _, d := range found {
				if strings.Contains(d.Message, tt.says) && d.Severity != tt.want {
					t.Errorf("want severity %q, got %q for %q", tt.want, d.Severity, d.Message)
				}
			}
		})
	}
}

func TestMemoryLimiterSizingPercentageWithoutContainerLimit(t *testing.T) {
	t.Parallel()

	env := rule.Environment{Kubernetes: true, MemoryRequest: 512 * quantity.Mi, MemoryLimit: 0}
	tests := []struct {
		name     string
		settings string
		warn     bool
	}{
		{name: "absent limits", settings: "", warn: false},
		{name: "positive percentage", settings: "    limit_percentage: 80", warn: true},
		{name: "whole percentage", settings: "    limit_percentage: 100", warn: true},
		{name: "zero fixed limit", settings: "    limit_mib: 0\n    limit_percentage: 80", warn: true},
		{name: "fixed limit with zero percentage", settings: "    limit_mib: 512\n    limit_percentage: 0", warn: false},
		{name: "fixed limit with positive percentage", settings: "    limit_mib: 512\n    limit_percentage: 80", warn: false},
		{name: "zero percentage", settings: "    limit_percentage: 0", warn: false},
		{name: "negative percentage", settings: "    limit_percentage: -1", warn: false},
		{name: "percentage above hundred", settings: "    limit_percentage: 101", warn: false},
		{name: "invalid percentage", settings: "    limit_percentage: invalid", warn: false},
		{name: "runtime percentage", settings: "    limit_percentage: ${env:PERCENT}", warn: false},
		{
			name:     "fixed limit with runtime percentage",
			settings: "    limit_mib: 512\n    limit_percentage: ${env:PERCENT}",
			warn:     false,
		},
		{
			name:     "runtime fixed limit with positive percentage",
			settings: "    limit_mib: ${env:LIMIT}\n    limit_percentage: 80",
			warn:     false,
		},
		{
			name:     "invalid fixed limit with positive percentage",
			settings: "    limit_mib: invalid\n    limit_percentage: 80",
			warn:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			found := checkIn(t, limiter("    check_interval: 1s\n"+tt.settings), env)
			if !tt.warn {
				assert.Empty(t, found)

				return
			}

			require.Len(t, found, 1)
			assert.Equal(t, diag.Warning, found[0].Severity)
			assert.Equal(t, "processors.memory_limiter.limit_percentage", found[0].Path)
			assert.Contains(t, found[0].Message, "no memory limit")
		})
	}
}

func TestMemoryLimiterSizingIsSilentWithoutAnEnvironment(t *testing.T) {
	t.Parallel()

	for name, env := range map[string]rule.Environment{
		"nothing configured":         {},
		"kubernetes with no numbers": {Kubernetes: true},
		"numbers but not kubernetes": {MemoryLimit: 512 * quantity.Mi},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if found := checkIn(t, sized("4096"), env); len(found) > 0 {
				t.Errorf("nothing should be reported without an environment, got %+v", found)
			}
		})
	}
}

func TestMemoryLimiterSizingFitsTheContainer(t *testing.T) {
	t.Parallel()

	env := rule.Environment{Kubernetes: true, MemoryRequest: 512 * quantity.Mi, MemoryLimit: 512 * quantity.Mi}
	if found := checkIn(t, sized("400"), env); len(found) > 0 {
		t.Errorf("a limiter that fits its container should be quiet, got %+v", found)
	}
}

func TestMemoryLimiterSizingNamesEveryInstance(t *testing.T) {
	t.Parallel()

	src := `
receivers: {otlp: }
processors:
  memory_limiter:
    check_interval: 1s
    limit_mib: 4096
  memory_limiter/aggressive:
    check_interval: 1s
    limit_mib: 8192
exporters: {debug: }
service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [memory_limiter, memory_limiter/aggressive]
      exporters: [debug]
`
	env := rule.Environment{Kubernetes: true, MemoryRequest: 0, MemoryLimit: 512 * quantity.Mi}

	found := checkIn(t, src, env)
	if !ruletest.Reports(found, "\"memory_limiter\"") || !ruletest.Reports(found, "\"memory_limiter/aggressive\"") {
		t.Errorf("both instances share the one container limit and both should be named: %+v", found)
	}
}

// TestMemoryLimiterSizingDoesNotInventANumber pins that a limit_mib too large
// to hold as a byte count leaves the hard limit unknown. Multiplying it out
// wraps, and a wrapped product lands back in a plausible range: the limiter
// below would otherwise be reported as enforcing exactly the container's 512Mi,
// a figure that appears nowhere in the config.
func TestMemoryLimiterSizingDoesNotInventANumber(t *testing.T) {
	t.Parallel()

	env := rule.Environment{Kubernetes: true, MemoryRequest: 0, MemoryLimit: 512 * quantity.Mi}

	// 2^44 MiB is 2^64 bytes plus the 512Mi that the wrap leaves behind.
	assert.Empty(t, checkIn(t, sized("17592186044928"), env),
		"a limit that does not fit in a byte count cannot be sized")

	// The same for a percentage large enough to overflow the multiplication.
	src := limiter("    check_interval: 1s\n    limit_percentage: 99999999999999")
	assert.Empty(t, checkIn(t, src, env),
		"a percentage that does not fit cannot be sized")
}

func TestMemoryLimiterSizingLeavesExpansionsAlone(t *testing.T) {
	t.Parallel()

	env := rule.Environment{Kubernetes: true, MemoryRequest: 256 * quantity.Mi, MemoryLimit: 256 * quantity.Mi}
	src := limiter("    check_interval: 1s\n    limit_mib: ${env:LIMIT}")

	if found := checkIn(t, src, env); len(found) > 0 {
		t.Errorf("a limit resolved at runtime cannot be sized, got %+v", found)
	}
}
