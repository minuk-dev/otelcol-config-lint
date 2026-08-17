package debugexporterverbosity_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/debugexporterverbosity"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/ruletest"
)

// check runs the rule over src, which every test in this package starts from.
func check(t *testing.T, src string) diag.Diagnostics {
	t.Helper()

	found, err := ruletest.Run(debugexporterverbosity.New(), src)
	require.NoError(t, err, "parse")

	return found
}

// debugging wraps debug exporter settings in a config that is otherwise clean.
// The settings are written already indented by four spaces.
func debugging(settings string) string {
	return `
receivers: {otlp: }
exporters:
  debug:
` + settings + `
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [debug]}
`
}

func TestDebugExporterVerbosity(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		settings string
		want     diag.Severity
	}{
		"the verbosity that logs every record": {
			settings: "    verbosity: detailed",
			want:     diag.Warning,
		},
		// configtelemetry folds the value before it reads it, so this is the
		// same level to the collector.
		"the same verbosity in capitals": {
			settings: "    verbosity: Detailed",
			want:     diag.Warning,
		},
		"no settings at all, so the default": {
			settings: "",
			want:     diag.Info,
		},
		"a verbosity written out as the default": {
			settings: "    verbosity: basic",
			want:     diag.Info,
		},
		"the verbosity between the two": {
			settings: "    verbosity: normal",
			want:     diag.Info,
		},
		// Nothing here knows what the variable holds, and a warning quoting a
		// verbosity nobody wrote would be about a config the collector does not
		// run. The note below it is true either way.
		"a verbosity resolved at runtime": {
			settings: "    verbosity: ${env:VERBOSITY}",
			want:     diag.Info,
		},
		"a setting that is not the verbosity": {
			settings: "    sampling_initial: 2",
			want:     diag.Info,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			found := check(t, debugging(tt.settings))
			require.Len(t, found, 1)
			assert.Equal(t, tt.want, found[0].Severity)
		})
	}
}

// Matching on the type is what covers a named instance, which is how a debug
// exporter added for one pipeline is usually written.
func TestDebugExporterVerbosityMatchesOnType(t *testing.T) {
	t.Parallel()

	src := `
receivers: {otlp: }
exporters:
  debug/verbose:
    verbosity: detailed
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [debug/verbose]}
`

	found := check(t, src)
	require.Len(t, found, 1)
	assert.Equal(t, diag.Warning, found[0].Severity)
	assert.Contains(t, found[0].Message, `exporter "debug/verbose"`)
}

// Each clause has to name the pipeline the exporter is wired into, since that
// is where the reader takes it out of.
func TestDebugExporterVerbosityReadsAsASentence(t *testing.T) {
	t.Parallel()

	detailed := check(t, debugging("    verbosity: detailed"))
	require.Len(t, detailed, 1)

	assert.Equal(t, `exporter "debug" runs at verbosity: detailed in pipeline "traces", `+
		"logging every record it receives", detailed[0].Message)
	assert.Equal(t, "set verbosity: basic, or remove the exporter from service.pipelines.traces.exporters; "+
		"sampling_initial and sampling_thereafter bound the rate if it has to stay at detailed",
		detailed[0].Hint)
	assert.Equal(t, "service.pipelines.traces.exporters[0]", detailed[0].Path)
	assert.Contains(t, detailed[0].Docs, "exporter/debugexporter/README.md")

	quiet := check(t, debugging(""))
	require.Len(t, quiet, 1)

	assert.Equal(t, `exporter "debug" writes the telemetry of pipeline "traces" to the collector's log`,
		quiet[0].Message)
	assert.Equal(t, "the exporter is for diagnosing a pipeline rather than for running one; "+
		"remove it from service.pipelines.traces.exporters once the diagnosis is done, and do not parse "+
		"what it prints, whose format upstream does not keep stable", quiet[0].Hint)
}

// The exporter is reported where it is wired in, so a debug exporter shared by
// three pipelines reports in the three places it has to be removed from.
func TestDebugExporterVerbosityReportsPerPipeline(t *testing.T) {
	t.Parallel()

	src := `
receivers: {otlp: }
exporters:
  debug:
    verbosity: detailed
  otlphttp:
    endpoint: http://backend:4318
    sending_queue: {storage: file_storage}
extensions: {file_storage: }
service:
  extensions: [file_storage]
  pipelines:
    traces: {receivers: [otlp], exporters: [otlphttp, debug]}
    metrics: {receivers: [otlp], exporters: [debug]}
`

	found := check(t, src)
	require.Len(t, found, 2)
	assert.Equal(t, "service.pipelines.traces.exporters[1]", found[0].Path)
	assert.Equal(t, "service.pipelines.metrics.exporters[0]", found[1].Path)
}

func TestDebugExporterVerbosityStaysQuiet(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		// An exporter no pipeline reaches is never instantiated and prints
		// nothing; unused-component is what has something to say about it.
		"a debug exporter no pipeline references": `
receivers: {otlp: }
exporters:
  debug:
    verbosity: detailed
  otlphttp:
    endpoint: http://backend:4318
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [otlphttp]}
`,
		// Nothing declares it, so the collector does not start at all and
		// undefined-reference is the finding worth having.
		"a debug exporter nobody declared": `
receivers: {otlp: }
exporters:
  otlphttp:
    endpoint: http://backend:4318
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [otlphttp, debug]}
`,
		// The predecessor of the debug exporter is deprecated-component's, and
		// it is not this exporter type.
		"the logging exporter": `
receivers: {otlp: }
exporters: {logging: }
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [logging]}
`,
		"no service block": `
exporters:
  debug:
    verbosity: detailed
`,
	}

	for name, src := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Empty(t, check(t, src))
		})
	}
}
