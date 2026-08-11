package rule_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/quantity"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// checkRule runs one rule over src in a given deployment environment.
func checkRule(t *testing.T, name, src string, env rule.Environment) diag.Diagnostics {
	t.Helper()

	f, err := config.Parse("test.yaml", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	r, ok := rule.Lookup(name)
	if !ok {
		t.Fatalf("rule %q is not registered", name)
	}

	return rule.Run(r, rule.Context{File: f, Schema: testSchema(), Index: rule.NewIndex(f), Env: env}, r.Severity())
}

// reports whether any finding mentions substr.
func reports(found diag.Diagnostics, substr string) bool {
	for _, d := range found {
		if strings.Contains(d.Message, substr) {
			return true
		}
	}

	return false
}

// limiter wraps memory_limiter settings in a config that is otherwise clean.
// The settings are written already indented by four spaces.
func limiter(name, settings string) string {
	return `
receivers: {otlp: }
processors:
  ` + name + `:
` + settings + `
exporters: {debug: }
service:
  pipelines:
    traces: {receivers: [otlp], processors: [` + name + `], exporters: [debug]}
`
}

func TestMemoryLimiterConfig(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		settings string
		want     string // a phrase the finding has to carry
	}{
		"nothing set at all": {
			settings: "",
			want:     "'check_interval' must be greater than zero",
		},
		"no limit": {
			settings: "    check_interval: 1s",
			want:     "'limit_mib' or 'limit_percentage' must be greater than zero",
		},
		"check_interval is zero": {
			settings: "    check_interval: 0s\n    limit_mib: 512",
			want:     "'check_interval' must be greater than zero",
		},
		"check_interval written as a bare zero": {
			settings: "    check_interval: 0\n    limit_mib: 512",
			want:     "'check_interval' must be greater than zero",
		},
		"limit set to zero": {
			settings: "    check_interval: 1s\n    limit_mib: 0",
			want:     "'limit_mib' or 'limit_percentage' must be greater than zero",
		},
		"spike at the limit": {
			settings: "    check_interval: 1s\n    limit_mib: 512\n    spike_limit_mib: 512",
			want:     "'spike_limit_mib' must be smaller than 'limit_mib'",
		},
		"spike above the limit": {
			settings: "    check_interval: 1s\n    limit_mib: 512\n    spike_limit_mib: 600",
			want:     "'spike_limit_mib' must be smaller than 'limit_mib'",
		},
		"spike percentage at the limit": {
			settings: "    check_interval: 1s\n    limit_percentage: 50\n    spike_limit_percentage: 50",
			want:     "'spike_limit_percentage' must be smaller than 'limit_percentage'",
		},
		"a percentage above a hundred": {
			settings: "    check_interval: 1s\n    limit_percentage: 120",
			want:     "less than or equal to hundred",
		},
		"a spike percentage above a hundred": {
			settings: "    check_interval: 1s\n    limit_percentage: 50\n    spike_limit_percentage: 120",
			want:     "less than or equal to hundred",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			found := checkRule(t, "memory-limiter-config", limiter("memory_limiter", tt.settings), rule.Environment{})
			if !reports(found, tt.want) {
				t.Errorf("no finding mentioned %q; got %+v", tt.want, found)
			}

			for _, d := range found {
				if d.Severity != diag.Error {
					continue
				}

				if !strings.Contains(d.Message, "memory_limiter") {
					t.Errorf("a finding should name the instance: %q", d.Message)
				}
			}
		})
	}
}

func TestMemoryLimiterConfigAcceptsAWorkingLimiter(t *testing.T) {
	t.Parallel()

	settings := "    check_interval: 1s\n    limit_mib: 512\n    spike_limit_mib: 128"
	if found := checkRule(t, "memory-limiter-config", limiter("memory_limiter", settings),
		rule.Environment{}); len(found) > 0 {
		t.Errorf("a valid memory_limiter should be quiet, got %+v", found)
	}

	// A percentage limiter is just as valid as a fixed one.
	settings = "    check_interval: 1s\n    limit_percentage: 80\n    spike_limit_percentage: 25"
	if found := checkRule(t, "memory-limiter-config", limiter("memory_limiter", settings),
		rule.Environment{}); len(found) > 0 {
		t.Errorf("a percentage memory_limiter should be quiet, got %+v", found)
	}
}

func TestMemoryLimiterConfigMatchesOnType(t *testing.T) {
	t.Parallel()

	found := checkRule(t, "memory-limiter-config", limiter("memory_limiter/aggressive", ""), rule.Environment{})
	if !reports(found, "memory_limiter/aggressive") {
		t.Errorf("a named instance should be checked and named, got %+v", found)
	}
}

func TestMemoryLimiterConfigLeavesExpansionsAlone(t *testing.T) {
	t.Parallel()

	settings := "    check_interval: ${env:INTERVAL}\n    limit_mib: ${env:LIMIT}\n    spike_limit_mib: ${env:SPIKE}"
	if found := checkRule(t, "memory-limiter-config", limiter("memory_limiter", settings),
		rule.Environment{}); len(found) > 0 {
		t.Errorf("values resolved at runtime cannot be checked, got %+v", found)
	}
}

func TestMemoryLimiterConfigRemarksOnAFarOffInterval(t *testing.T) {
	t.Parallel()

	settings := "    check_interval: 30s\n    limit_mib: 512\n    spike_limit_mib: 128"

	found := checkRule(t, "memory-limiter-config", limiter("memory_limiter", settings), rule.Environment{})
	if len(found) != 1 || found[0].Severity != diag.Info {
		t.Fatalf("want one info about the interval, got %+v", found)
	}

	settings = "    check_interval: 2s\n    limit_mib: 512\n    spike_limit_mib: 128"
	if found := checkRule(t, "memory-limiter-config", limiter("memory_limiter", settings),
		rule.Environment{}); len(found) > 0 {
		t.Errorf("an interval near the recommended one is a choice, not a finding: %+v", found)
	}
}

// sized is a memory_limiter with a fixed limit, for the sizing tests.
func sized(limitMiB string) string {
	return limiter("memory_limiter", "    check_interval: 1s\n    limit_mib: "+limitMiB+"\n    spike_limit_mib: 64")
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
			src:  limiter("memory_limiter", "    check_interval: 1s\n    limit_percentage: 80"),
			env:  rule.Environment{Kubernetes: true, MemoryRequest: 512 * quantity.Mi, MemoryLimit: 0},
			want: diag.Warning, says: "no memory limit",
		},
		"a percentage above the ceiling": {
			src:  limiter("memory_limiter", "    check_interval: 1s\n    limit_percentage: 90"),
			env:  container,
			want: diag.Warning, says: "above 80%",
		},
		"more than the pod asked for": {
			src:  sized("400"),
			env:  rule.Environment{Kubernetes: true, MemoryRequest: 256 * quantity.Mi, MemoryLimit: 512 * quantity.Mi},
			want: diag.Info, says: "memory request",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			found := checkRule(t, "memory-limiter-sizing", tt.src, tt.env)
			if !reports(found, tt.says) {
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

func TestMemoryLimiterSizingIsSilentWithoutAnEnvironment(t *testing.T) {
	t.Parallel()

	for name, env := range map[string]rule.Environment{
		"nothing configured":         {},
		"kubernetes with no numbers": {Kubernetes: true},
		"numbers but not kubernetes": {MemoryLimit: 512 * quantity.Mi},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if found := checkRule(t, "memory-limiter-sizing", sized("4096"), env); len(found) > 0 {
				t.Errorf("nothing should be reported without an environment, got %+v", found)
			}
		})
	}
}

func TestMemoryLimiterSizingFitsTheContainer(t *testing.T) {
	t.Parallel()

	env := rule.Environment{Kubernetes: true, MemoryRequest: 512 * quantity.Mi, MemoryLimit: 512 * quantity.Mi}
	if found := checkRule(t, "memory-limiter-sizing", sized("400"), env); len(found) > 0 {
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

	found := checkRule(t, "memory-limiter-sizing", src, env)
	if !reports(found, "\"memory_limiter\"") || !reports(found, "\"memory_limiter/aggressive\"") {
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
	assert.Empty(t, checkRule(t, "memory-limiter-sizing", sized("17592186044928"), env),
		"a limit that does not fit in a byte count cannot be sized")

	// The same for a percentage large enough to overflow the multiplication.
	src := limiter("memory_limiter", "    check_interval: 1s\n    limit_percentage: 99999999999999")
	assert.Empty(t, checkRule(t, "memory-limiter-sizing", src, env),
		"a percentage that does not fit cannot be sized")
}

func TestMemoryLimiterSizingLeavesExpansionsAlone(t *testing.T) {
	t.Parallel()

	env := rule.Environment{Kubernetes: true, MemoryRequest: 256 * quantity.Mi, MemoryLimit: 256 * quantity.Mi}
	src := limiter("memory_limiter", "    check_interval: 1s\n    limit_mib: ${env:LIMIT}")

	if found := checkRule(t, "memory-limiter-sizing", src, env); len(found) > 0 {
		t.Errorf("a limit resolved at runtime cannot be sized, got %+v", found)
	}
}

// TestFindingsCiteUpstream pins that a rule reporting what the collector
// requires says where upstream says so, rather than asking to be believed.
func TestFindingsCiteUpstream(t *testing.T) {
	t.Parallel()

	const memoryLimiterDocs = "https://github.com/open-telemetry/opentelemetry-collector" +
		"/blob/main/processor/memorylimiterprocessor/README.md"

	found := checkRule(t, "memory-limiter-config", limiter("memory_limiter", ""), rule.Environment{})
	if len(found) == 0 {
		t.Fatal("an empty memory_limiter should be reported")
	}

	for _, d := range found {
		if d.Docs != memoryLimiterDocs {
			t.Errorf("finding %q links to %q, want the processor's README", d.Message, d.Docs)
		}
	}

	env := rule.Environment{Kubernetes: true, MemoryRequest: 128 * quantity.Mi, MemoryLimit: 256 * quantity.Mi}
	for _, d := range checkRule(t, "memory-limiter-sizing", sized("512"), env) {
		if d.Docs == "" {
			t.Errorf("finding %q cites nothing", d.Message)
		}
	}
}
