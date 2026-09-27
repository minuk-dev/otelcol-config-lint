package signalsupport_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/ruletest"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/signalsupport"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// The config every test below checks: ordinary, and valid against any schema
// that describes what its components carry.
const pipeline = `
receivers:
  otlp:
exporters:
  otlp:
    endpoint: backend:4317
service:
  pipelines:
    traces:
      receivers: [otlp]
      exporters: [otlp]
`

func TestReportsAComponentWiredIntoASignalItDoesNotCarry(t *testing.T) {
	t.Parallel()

	found, err := ruletest.Run(signalsupport.New(), `
receivers:
  jaeger:
exporters:
  otlp:
    endpoint: backend:4317
service:
  pipelines:
    metrics:
      receivers: [jaeger]
      exporters: [otlp]
`)

	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Contains(t, found[0].Message, `receiver "jaeger" does not support metrics`)
	assert.Contains(t, found[0].Hint, "it supports: traces")
}

// Signals come from the metadata.yaml upstream ships beside a component, and
// components carried none until upstream added them: v0.70.0 says of all 284
// of its components that they support nothing. Read as an answer, that makes a
// config that runs an error on every release that far back.
func TestSaysNothingWhenTheSchemaStatesNoSignals(t *testing.T) {
	t.Parallel()

	found, err := ruletest.RunWith(signalsupport.New(), pipeline, ruletest.Options{
		Schema: &schema.Schema{
			CollectorVersion: "v0.70.0",
			Components: map[config.Kind]map[string]*schema.Component{
				config.KindReceiver: {"otlp": {Type: "otlp"}},
				config.KindExporter: {"otlp": {Type: "otlp"}},
			},
		},
	})

	require.NoError(t, err)
	assert.Empty(t, found, "a component the schema says nothing about was reported")
}

// A schema that describes one component and not another still answers for the
// one it describes.
func TestStillReportsTheComponentsTheSchemaDoesDescribe(t *testing.T) {
	t.Parallel()

	found, err := ruletest.RunWith(signalsupport.New(), pipeline, ruletest.Options{
		Schema: &schema.Schema{
			CollectorVersion: "v0.70.0",
			Components: map[config.Kind]map[string]*schema.Component{
				config.KindReceiver: {"otlp": {Type: "otlp", Signals: []config.Signal{"logs"}}},
				config.KindExporter: {"otlp": {Type: "otlp"}},
			},
		},
	})

	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Contains(t, found[0].Message, `receiver "otlp" does not support traces`)
}

// A connector states what it converts as pairs, so one carrying none is what
// says nothing -- signals alone cannot answer which end of the pipeline it
// sits at.
func TestSaysNothingWhenAConnectorStatesNoPairs(t *testing.T) {
	t.Parallel()

	found, err := ruletest.RunWith(signalsupport.New(), `
receivers:
  otlp:
exporters:
  otlp:
    endpoint: backend:4317
connectors:
  count:
service:
  pipelines:
    traces:
      receivers: [otlp]
      exporters: [count]
    metrics:
      receivers: [count]
      exporters: [otlp]
`, ruletest.Options{
		Schema: &schema.Schema{
			CollectorVersion: "v0.70.0",
			Components: map[config.Kind]map[string]*schema.Component{
				config.KindReceiver:  {"otlp": {Type: "otlp", Signals: []config.Signal{"traces"}}},
				config.KindExporter:  {"otlp": {Type: "otlp", Signals: []config.Signal{"metrics"}}},
				config.KindConnector: {"count": {Type: "count", Signals: []config.Signal{"traces", "metrics"}}},
			},
		},
	})

	require.NoError(t, err)
	assert.Empty(t, found, "a connector that states no pairs was reported")
}
