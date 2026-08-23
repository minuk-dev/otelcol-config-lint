package deprecatedtelemetrykey_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/deprecatedtelemetrykey"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/ruletest"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
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

// check runs the rule against the release the stand-in schema describes, which
// is newer than the one that stopped reading the key.
func check(t *testing.T, block string) diag.Diagnostics {
	t.Helper()

	found, err := ruletest.Run(deprecatedtelemetrykey.New(), telemetry(block))
	require.NoError(t, err, "parse")

	return found
}

func TestAnAddressTheCollectorNoLongerReadsIsReported(t *testing.T) {
	t.Parallel()

	found := check(t, "    metrics:\n      address: localhost:8888")
	require.Len(t, found, 1)

	assert.Equal(t, diag.Warning, found[0].Severity)
	assert.Contains(t, found[0].Message, "v0.123.0", "the message should say when it stopped working")
	assert.Contains(t, found[0].Hint, "readers", "the hint should show the replacement")
	assert.Equal(t, "service.telemetry.metrics.address", found[0].Path)
}

func TestTelemetryWithoutTheKeyIsQuiet(t *testing.T) {
	t.Parallel()

	for name, block := range map[string]string{
		"metrics served by a reader":                  "    metrics:\n      readers: []",
		"metrics at a level only":                     "    metrics:\n      level: basic",
		"an address under logs, which is not the key": "    logs:\n      address: localhost:8888",
		"no telemetry block at all":                   "",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Empty(t, check(t, block))
		})
	}
}

// TestTheKeyStillWorksOnAnOlderRelease pins the version gate: a config that
// targets a collector which still reads the key is right as written.
func TestTheKeyStillWorksOnAnOlderRelease(t *testing.T) {
	t.Parallel()

	const block = "    metrics:\n      address: localhost:8888"

	for name, tt := range map[string]struct {
		version string
		want    int
	}{
		"the release before it was dropped": {version: "v0.122.0", want: 0},
		"the release that dropped it":       {version: "v0.123.0", want: 1},
		"a release after that":              {version: "v0.157.0", want: 1},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			sch := ruletest.Schema()
			sch.CollectorVersion = tt.version

			found, err := ruletest.RunWith(deprecatedtelemetrykey.New(), telemetry(block),
				ruletest.Options{Schema: sch})
			require.NoError(t, err, "parse")
			assert.Len(t, found, tt.want)
		})
	}
}

// TestAResolvedSchemaIsWhatTheGateNeeds pins that a run with no schema says
// nothing: without a release there is nothing to say the key is too old for.
func TestAResolvedSchemaIsWhatTheGateNeeds(t *testing.T) {
	t.Parallel()

	found, err := ruletest.RunWith(deprecatedtelemetrykey.New(),
		telemetry("    metrics:\n      address: localhost:8888"),
		ruletest.Options{Schema: &schema.Schema{}})
	require.NoError(t, err, "parse")
	assert.Empty(t, found)
}
