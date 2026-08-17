package memorylimiterconfig_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/memorylimiterconfig"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/ruletest"
)

// check runs the rule over src, which every test in this package starts from.
func check(t *testing.T, src string) diag.Diagnostics {
	t.Helper()

	found, err := ruletest.Run(memorylimiterconfig.New(), src)
	require.NoError(t, err, "parse")

	return found
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

			found := check(t, limiter("memory_limiter", tt.settings))
			if !ruletest.Reports(found, tt.want) {
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
	if found := check(t, limiter("memory_limiter", settings)); len(found) > 0 {
		t.Errorf("a valid memory_limiter should be quiet, got %+v", found)
	}

	// A percentage limiter is just as valid as a fixed one.
	settings = "    check_interval: 1s\n    limit_percentage: 80\n    spike_limit_percentage: 25"
	if found := check(t, limiter("memory_limiter", settings)); len(found) > 0 {
		t.Errorf("a percentage memory_limiter should be quiet, got %+v", found)
	}
}

func TestMemoryLimiterConfigMatchesOnType(t *testing.T) {
	t.Parallel()

	found := check(t, limiter("memory_limiter/aggressive", ""))
	if !ruletest.Reports(found, "memory_limiter/aggressive") {
		t.Errorf("a named instance should be checked and named, got %+v", found)
	}
}

func TestMemoryLimiterConfigLeavesExpansionsAlone(t *testing.T) {
	t.Parallel()

	settings := "    check_interval: ${env:INTERVAL}\n    limit_mib: ${env:LIMIT}\n    spike_limit_mib: ${env:SPIKE}"
	if found := check(t, limiter("memory_limiter", settings)); len(found) > 0 {
		t.Errorf("values resolved at runtime cannot be checked, got %+v", found)
	}
}

func TestMemoryLimiterConfigRemarksOnAFarOffInterval(t *testing.T) {
	t.Parallel()

	settings := "    check_interval: 30s\n    limit_mib: 512\n    spike_limit_mib: 128"

	found := check(t, limiter("memory_limiter", settings))
	if len(found) != 1 || found[0].Severity != diag.Info {
		t.Fatalf("want one info about the interval, got %+v", found)
	}

	settings = "    check_interval: 2s\n    limit_mib: 512\n    spike_limit_mib: 128"
	if found := check(t, limiter("memory_limiter", settings)); len(found) > 0 {
		t.Errorf("an interval near the recommended one is a choice, not a finding: %+v", found)
	}
}
