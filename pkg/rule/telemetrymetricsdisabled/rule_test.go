package telemetrymetricsdisabled_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/ruletest"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/telemetrymetricsdisabled"
)

// telemetry wraps a service.telemetry block in a config that is otherwise
// clean. The block is written already indented by four spaces.
func telemetry(block string) string {
	return `
receivers: {otlp: }
exporters: {debug: }
service:
  telemetry:
` + block + `
  pipelines:
    traces: {receivers: [otlp], exporters: [debug]}
`
}

func check(t *testing.T, block string) diag.Diagnostics {
	t.Helper()

	found, err := ruletest.Run(telemetrymetricsdisabled.New(), telemetry(block))
	require.NoError(t, err, "parse")

	return found
}

func TestMetricsTurnedOffAreReported(t *testing.T) {
	t.Parallel()

	found := check(t, "    metrics:\n      level: none")
	require.Len(t, found, 1)

	assert.Equal(t, diag.Info, found[0].Severity, "there are reasons to do it, so it is a note")
	assert.Contains(t, found[0].Message, "no metrics about itself")
	assert.Equal(t, "service.telemetry.metrics.level", found[0].Path)
}

// TestTheLevelIsFoldedBeforeItIsRead pins that the rule reads the value the way
// configtelemetry does, which folds it.
func TestTheLevelIsFoldedBeforeItIsRead(t *testing.T) {
	t.Parallel()

	assert.Len(t, check(t, "    metrics:\n      level: None"), 1)
	assert.Len(t, check(t, "    metrics:\n      level: NONE"), 1)
}

func TestMetricsLeftOnAreQuiet(t *testing.T) {
	t.Parallel()

	for name, block := range map[string]string{
		"a level that reports":                   "    metrics:\n      level: basic",
		"the detailed level":                     "    metrics:\n      level: detailed",
		"no level, so the default":               "    metrics:\n      readers: []",
		"no telemetry block at all":              "",
		"none under logs, not here":              "    logs:\n      level: none",
		"a level only the collector can resolve": "    metrics:\n      level: ${env:OTEL_METRICS_LEVEL}",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Empty(t, check(t, block))
		})
	}
}
