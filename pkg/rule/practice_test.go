package rule_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// exporting wraps exporter settings in a config that is otherwise clean, with
// a file_storage extension declared and enabled so a queue has something real
// to name. The settings are written already indented by four spaces.
func exporting(settings string) string {
	return `
receivers: {otlp: }
exporters:
  otlphttp:
    endpoint: http://backend:4318
` + settings + `
extensions: {file_storage: }
service:
  extensions: [file_storage]
  pipelines:
    traces: {receivers: [otlp], exporters: [otlphttp]}
`
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

// ordering wraps a processor list in a config that is otherwise clean, with
// every processor in the list declared so the pipeline references nothing that
// does not exist.
func ordering(processors ...string) string {
	var declared strings.Builder
	for _, p := range processors {
		declared.WriteString("  " + p + ":\n")
	}

	return `
receivers: {otlp: }
processors:
` + declared.String() + `exporters: {debug: }
service:
  pipelines:
    traces: {receivers: [otlp], processors: [` + strings.Join(processors, ", ") + `], exporters: [debug]}
`
}

// A sampler decides on what it can see, so an enrichment processor behind one
// is a policy matching attributes that are not there yet. None of these lists
// carry a memory_limiter or a batch, so what the rule reports is the middle of
// the chain and nothing else.
func TestProcessorOrderEnrichmentAfterSampling(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		processors []string
		want       int
	}{
		"k8s attributes added after the sampling decision": {
			processors: []string{"tail_sampling", "k8sattributes"},
			want:       1,
		},
		// The same two processors under the names upstream renamed them to,
		// which is what a config written against a recent release carries.
		"the k8s attributes processor under its new name": {
			processors: []string{"tail_sampling", "k8s_attributes"},
			want:       1,
		},
		"the resource detection processor under its new name": {
			processors: []string{"probabilistic_sampler", "resource_detection"},
			want:       1,
		},
		"the same two the way round that works": {
			processors: []string{"k8sattributes", "tail_sampling"},
			want:       0,
		},
		"cloud and host attributes added after the decision": {
			processors: []string{"probabilistic_sampler", "resourcedetection"},
			want:       1,
		},
		"attributes the config writes itself, added after the decision": {
			processors: []string{"tail_sampling", "resource"},
			want:       1,
		},
		// attributes is left out of the group on purpose: stripping fields
		// from what a sampler kept is the right order to do it in.
		"an attributes processor after the decision": {
			processors: []string{"tail_sampling", "attributes"},
			want:       0,
		},
		// filter belongs between enrichment and sampling in the documented
		// order, but a filter that drops early is a filter doing its job.
		"a filter after the decision": {
			processors: []string{"tail_sampling", "filter"},
			want:       0,
		},
		"two enrichment processors behind one sampler": {
			processors: []string{"tail_sampling", "k8sattributes", "resource"},
			want:       2,
		},
		"a pipeline with no sampler in it at all": {
			processors: []string{"resource", "k8sattributes"},
			want:       0,
		},
		// Matching on the type is what covers the named instances, which is
		// how a second sampling policy set is usually written.
		"named instances of both": {
			processors: []string{"tail_sampling/errors", "k8sattributes/pods"},
			want:       1,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			found := checkRule(t, "processor-order", ordering(tt.processors...), rule.Environment{})
			assert.Len(t, found, tt.want)

			for _, d := range found {
				assert.Equal(t, diag.Warning, d.Severity)
			}
		})
	}
}

// The finding has to name both processors, since neither one on its own says
// what is wrong, and it reports at the rule's own severity rather than at the
// batch clause's info.
func TestProcessorOrderEnrichmentReadsAsASentence(t *testing.T) {
	t.Parallel()

	found := checkRule(t, "processor-order",
		ordering("memory_limiter", "tail_sampling", "k8sattributes/pods"), rule.Environment{})
	require.Len(t, found, 1)

	assert.Equal(t, diag.Warning, found[0].Severity)
	assert.Equal(t, `"k8sattributes/pods" runs after "tail_sampling" in pipeline "traces", `+
		"so sampling policies cannot match the attributes it adds", found[0].Message)
	assert.Equal(t, `move "k8sattributes/pods" ahead of "tail_sampling" in `+
		"service.pipelines.traces.processors so the decision is made against enriched telemetry",
		found[0].Hint)
	assert.Equal(t, "service.pipelines.traces.processors[2]", found[0].Path)
	assert.Contains(t, found[0].Docs, "processor/tailsamplingprocessor/README.md")
}

// Getting ahead of the first sampler clears the ones behind it, so that is the
// one the finding names -- and the page it cites is the one that sampler's
// decision is documented on.
func TestProcessorOrderNamesTheFirstSampler(t *testing.T) {
	t.Parallel()

	found := checkRule(t, "processor-order",
		ordering("probabilistic_sampler", "tail_sampling", "resourcedetection"), rule.Environment{})
	require.Len(t, found, 1)

	assert.Contains(t, found[0].Message, `runs after "probabilistic_sampler"`)
	assert.Contains(t, found[0].Docs, "processor/probabilisticsamplerprocessor/README.md")
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

			found := checkRule(t, "debug-exporter-verbosity", debugging(tt.settings), rule.Environment{})
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

	found := checkRule(t, "debug-exporter-verbosity", src, rule.Environment{})
	require.Len(t, found, 1)
	assert.Equal(t, diag.Warning, found[0].Severity)
	assert.Contains(t, found[0].Message, `exporter "debug/verbose"`)
}

// Each clause has to name the pipeline the exporter is wired into, since that
// is where the reader takes it out of.
func TestDebugExporterVerbosityReadsAsASentence(t *testing.T) {
	t.Parallel()

	detailed := checkRule(t, "debug-exporter-verbosity",
		debugging("    verbosity: detailed"), rule.Environment{})
	require.Len(t, detailed, 1)

	assert.Equal(t, `exporter "debug" runs at verbosity: detailed in pipeline "traces", `+
		"logging every record it receives", detailed[0].Message)
	assert.Equal(t, "set verbosity: basic, or remove the exporter from service.pipelines.traces.exporters; "+
		"sampling_initial and sampling_thereafter bound the rate if it has to stay at detailed",
		detailed[0].Hint)
	assert.Equal(t, "service.pipelines.traces.exporters[0]", detailed[0].Path)
	assert.Contains(t, detailed[0].Docs, "exporter/debugexporter/README.md")

	quiet := checkRule(t, "debug-exporter-verbosity", debugging(""), rule.Environment{})
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

	found := checkRule(t, "debug-exporter-verbosity", src, rule.Environment{})
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

			assert.Empty(t, checkRule(t, "debug-exporter-verbosity", src, rule.Environment{}))
		})
	}
}

func TestMissingBatch(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		settings string
		want     bool // whether the pipeline should be reported
	}{
		"no queue settings at all": {
			settings: "",
			want:     true,
		},
		"a queue that does not batch": {
			settings: "    sending_queue:\n      queue_size: 5000",
			want:     true,
		},
		// The block is optional upstream, so writing the key is what turns the
		// batcher on and an empty one takes the defaults.
		"a queue that batches": {
			settings: "    sending_queue:\n      batch: {}",
			want:     false,
		},
		"a batch key with nothing under it": {
			settings: "    sending_queue:\n      batch:",
			want:     false,
		},
		"a batch with settings of its own": {
			settings: "    sending_queue:\n      batch:\n        flush_timeout: 200ms\n        min_size: 8192",
			want:     false,
		},
		// The queue is on by default and batching is not, so an exporter that
		// only turns the queue on still sends a span at a time.
		"a queue turned on and nothing else": {
			settings: "    sending_queue:\n      enabled: true",
			want:     true,
		},
		// Nothing here can see what the variable holds, and telling an exporter
		// that already batches to batch is the finding this rule must not make.
		"a queue built from an expansion": {
			settings: "    sending_queue: ${env:QUEUE}",
			want:     false,
		},
		"a queue built from a merge key": {
			settings: "    sending_queue:\n      <<: &queue {queue_size: 5000}\n      storage: file_storage",
			want:     false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			found := checkRule(t, "missing-batch", exporting(tt.settings), rule.Environment{})
			assert.Len(t, found, map[bool]int{true: 1, false: 0}[tt.want])
		})
	}
}

// The hint is the whole point of the change: exporter-side batching first, with
// the reason it comes first, and the processor named as what also works.
func TestMissingBatchReadsAsASentence(t *testing.T) {
	t.Parallel()

	const hint = "the batch processor also works but drops data that fails to send"

	found := checkRule(t, "missing-batch", exporting(""), rule.Environment{})
	require.Len(t, found, 1)

	assert.Equal(t, `pipeline "traces" has no batch processor, and none of its exporters batches in sending_queue`,
		found[0].Message)
	assert.Equal(t, "configure sending_queue.batch on the exporters, which batches behind the retry queue; "+
		hint, found[0].Hint)
	assert.Equal(t, "service.pipelines.traces.processors", found[0].Path)
	assert.Equal(t, diag.Info, found[0].Severity)
	assert.Contains(t, found[0].Docs, "exporter/exporterhelper/README.md")
}

// A pipeline where only some exporters batch is under-batched on the legs that
// do not, so the finding names the exporter rather than the pipeline: the fix
// is written in that exporter's settings and nowhere else.
func TestMissingBatchReportsPerExporter(t *testing.T) {
	t.Parallel()

	src := `
receivers: {otlp: }
exporters:
  otlphttp:
    endpoint: http://backend:4318
    sending_queue:
      batch: {}
  otlphttp/second:
    endpoint: http://other:4318
  otlphttp/third:
    endpoint: http://third:4318
    sending_queue:
      batch:
        flush_timeout: 200ms
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [otlphttp, otlphttp/second, otlphttp/third]}
`

	found := checkRule(t, "missing-batch", src, rule.Environment{})
	require.Len(t, found, 1)

	assert.Equal(t, `exporter "otlphttp/second" sends unbatched in pipeline "traces", `+
		"which has no batch processor", found[0].Message)
	assert.Equal(t, "configure sending_queue.batch on this exporter, which batches behind the retry queue; "+
		"the batch processor also works but drops data that fails to send", found[0].Hint)
	assert.Equal(t, "service.pipelines.traces.exporters[1]", found[0].Path)
}

// processorFirst is the opening of every hint that leads with the processor.
const processorFirst = "add batch before the exporters to reduce the number of outgoing requests; "

// An exporter the schema says has no sending queue -- debug and nop among them
// -- can only be batched in front of, and a hint naming a setting it does not
// have would be no fix.
func TestMissingBatchWithoutASendingQueue(t *testing.T) {
	t.Parallel()

	pipeline := checkRule(t, "missing-batch", debugging(""), rule.Environment{})
	require.Len(t, pipeline, 1)
	assert.Equal(t, processorFirst+"these exporters have no sending_queue.batch to batch in", pipeline[0].Hint)
	assert.Contains(t, pipeline[0].Docs, "processor/batchprocessor/README.md")

	mixed := checkRule(t, "missing-batch", `
receivers: {otlp: }
exporters:
  debug:
  otlphttp:
    endpoint: http://backend:4318
    sending_queue: {batch: {}}
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [debug, otlphttp]}
`, rule.Environment{})
	require.Len(t, mixed, 1)
	assert.Equal(t, `exporter "debug" sends unbatched in pipeline "traces", which has no batch processor`,
		mixed[0].Message)
	assert.Equal(t, processorFirst+"this exporter has no sending_queue.batch to batch in", mixed[0].Hint)
}

// A finding covering several exporters at once can only name the queue batcher
// when all of them accept it. Where they disagree, naming it would be advice
// half of them cannot take, and naming the reason the processor comes first
// would be a claim that is false of the other half.
func TestMissingBatchWhereTheExportersDisagree(t *testing.T) {
	t.Parallel()

	found := checkRule(t, "missing-batch", `
receivers: {otlp: }
exporters:
  debug:
  otlphttp:
    endpoint: http://backend:4318
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [debug, otlphttp]}
`, rule.Environment{})
	require.Len(t, found, 1)

	assert.Equal(t, `pipeline "traces" has no batch processor, and none of its exporters batches in sending_queue`,
		found[0].Message)
	assert.Equal(t, "add batch before the exporters to reduce the number of outgoing requests", found[0].Hint)
	assert.Contains(t, found[0].Docs, "processor/batchprocessor/README.md")
}

// The queue batcher is younger than the queue. On a release that has the queue
// and not the batcher, the hint has to name the processor: writing
// sending_queue.batch there is a setting the collector rejects on startup, and
// unknown-field reads the same schema and would say so in the same run.
func TestMissingBatchBeforeTheQueueHadABatcher(t *testing.T) {
	t.Parallel()

	found := runMissingBatch(t, exporting(""), &schema.Schema{
		CollectorVersion: "v0.110.0",
		Components: map[config.Kind]map[string]*schema.Component{
			config.KindExporter: {
				// The sending_queue upstream shipped before the batcher moved
				// into it: closed, and with no batch under it.
				"otlphttp": {Type: "otlphttp", Fields: &schema.Field{Type: "map",
					Children: map[string]*schema.Field{
						"endpoint": {Type: "string"},
						"sending_queue": {Type: "map", Children: map[string]*schema.Field{
							"enabled":    {Type: "bool"},
							"queue_size": {Type: "int"},
							"storage":    {Type: "string"},
						}},
					}}},
			},
		},
	})
	require.Len(t, found, 1)

	assert.Equal(t, processorFirst+"these exporters have no sending_queue.batch to batch in", found[0].Hint)
	assert.Contains(t, found[0].Docs, "processor/batchprocessor/README.md")
}

func TestMissingBatchStaysQuiet(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		// The processor is not deprecated, so a pipeline that uses it batches
		// and this rule has nothing to say about it.
		"a batch processor": `
receivers: {otlp: }
processors: {batch: }
exporters:
  otlphttp:
    endpoint: http://backend:4318
service:
  pipelines:
    traces: {receivers: [otlp], processors: [batch], exporters: [otlphttp]}
`,
		// A named instance is the same processor.
		"a batch processor under another name": `
receivers: {otlp: }
processors: {batch/large: }
exporters:
  otlphttp:
    endpoint: http://backend:4318
service:
  pipelines:
    traces: {receivers: [otlp], processors: [batch/large], exporters: [otlphttp]}
`,
		// Every leg batches in its own queue, and a batch processor on top
		// would be a second layer with a flush timing of its own.
		"every exporter batching in its queue": `
receivers: {otlp: }
exporters:
  otlphttp:
    endpoint: http://backend:4318
    sending_queue: {batch: {}}
  otlphttp/second:
    endpoint: http://other:4318
    sending_queue: {batch: {flush_timeout: 1s}}
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [otlphttp, otlphttp/second]}
`,
		// An anchor is the same queue written once and used twice.
		"a queue behind an anchor": `
receivers: {otlp: }
exporters:
  otlphttp:
    endpoint: http://backend:4318
    sending_queue: &queue
      batch: {}
  otlphttp/second:
    endpoint: http://other:4318
    sending_queue: *queue
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [otlphttp, otlphttp/second]}
`,
		// A merge may be what supplies the batcher, and the document is read as
		// written.
		"settings built from a merge key": `
receivers: {otlp: }
exporters:
  otlphttp:
    <<: &base {sending_queue: {batch: {}}}
    endpoint: http://backend:4318
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [otlphttp]}
`,
		// A connector is not a leg out of the collector: it feeds another
		// pipeline, which has exporters of its own to batch on.
		"a pipeline that only feeds a connector": `
receivers: {otlp: }
connectors: {spanmetrics: }
exporters:
  otlphttp:
    endpoint: http://backend:4318
    sending_queue: {batch: {}}
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [spanmetrics]}
    metrics: {receivers: [spanmetrics], exporters: [otlphttp]}
`,
		// Nothing declares it, so the collector does not start at all and
		// undefined-reference is the finding worth having.
		"an exporter nobody declared": `
receivers: {otlp: }
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [otlphttp]}
`,
		"a pipeline with no exporters": `
receivers: {otlp: }
service:
  pipelines:
    traces: {receivers: [otlp]}
`,
		"no service block": `
exporters:
  otlphttp:
    endpoint: http://backend:4318
`,
	}

	for name, src := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Empty(t, checkRule(t, "missing-batch", src, rule.Environment{}))
		})
	}
}

// What a schema does not describe is still a pipeline with no batch processor
// and no batcher written under any of its queues, so the finding stands. It is
// the hint that gives way: nothing says sending_queue.batch is a setting these
// exporters would accept, so it names the one fix that needs no schema.
//
// The exporter with no fields is the shape that matters most. testSchema's
// logging entry has it, and so does the datadog exporter's, which really does
// carry a queue and a great many other settings.
func TestMissingBatchWhereTheSchemaSaysNothing(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		src    string
		schema *schema.Schema
	}{
		"no schema at all":           {src: exporting(""), schema: &schema.Schema{}},
		"an exporter with no fields": {src: exportingLogging(), schema: testSchema()},
		"a type the schema misses":   {src: exportingUnknownType(), schema: testSchema()},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			found := runMissingBatch(t, tt.src, tt.schema)
			require.Len(t, found, 1)
			assert.Equal(t, processorFirst+"these exporters have no sending_queue.batch to batch in", found[0].Hint)
			assert.Contains(t, found[0].Docs, "processor/batchprocessor/README.md")
		})
	}
}

// exportingLogging wires up the exporter testSchema describes with no fields,
// which is the shape the datadog entry has.
func exportingLogging() string {
	return `
receivers: {otlp: }
exporters: {logging: }
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [logging]}
`
}

// exportingUnknownType wires up an exporter the schema has no entry for at all.
func exportingUnknownType() string {
	return `
receivers: {otlp: }
exporters: {kafka: }
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [kafka]}
`
}

// runMissingBatch runs the rule against a schema the caller chooses, which
// checkRule does not allow.
func runMissingBatch(t *testing.T, src string, s *schema.Schema) diag.Diagnostics {
	t.Helper()

	f, err := config.Parse("test.yaml", []byte(src))
	require.NoError(t, err, "parse")

	r, ok := rule.Lookup("missing-batch")
	require.True(t, ok, "rule is not registered")

	return rule.Run(r, rule.Context{File: f, Schema: s, Index: rule.NewIndex(f)}, r.Severity())
}

func TestNoPersistentQueue(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		settings string
		want     bool // whether the queue should be reported
	}{
		"no queue settings at all": {
			settings: "",
			want:     true,
		},
		"a queue with no storage": {
			settings: "    sending_queue:\n      queue_size: 5000",
			want:     true,
		},
		"a queue turned on and left in memory": {
			settings: "    sending_queue:\n      enabled: true",
			want:     true,
		},
		"an empty storage": {
			settings: "    sending_queue:\n      storage:",
			want:     true,
		},
		"a storage extension": {
			settings: "    sending_queue:\n      storage: file_storage",
			want:     false,
		},
		"a storage resolved at runtime": {
			settings: "    sending_queue:\n      storage: ${env:STORAGE}",
			want:     false,
		},
		"the queue turned off": {
			settings: "    sending_queue:\n      enabled: false",
			want:     false,
		},
		// The variable may resolve to false, and then there is no queue to
		// lose. enabled is read the same way storage is: a value this rule
		// cannot see, not one nobody wrote.
		"a queue enabled at runtime": {
			settings: "    sending_queue:\n      enabled: ${env:QUEUE}",
			want:     false,
		},
		// A string is not the boolean confmap wants, so it does not turn the
		// queue off; invalid-value is what reports the type.
		"the queue turned off with a string": {
			settings: `    sending_queue:` + "\n" + `      enabled: "false"`,
			want:     true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			found := checkRule(t, "no-persistent-queue", exporting(tt.settings), rule.Environment{})
			assert.Len(t, found, map[bool]int{true: 1, false: 0}[tt.want])
		})
	}
}

// The finding is a design note, so what it says is the whole of its value: it
// has to name the exporter and all three steps persistence takes.
func TestNoPersistentQueueReadsAsASentence(t *testing.T) {
	t.Parallel()

	found := checkRule(t, "no-persistent-queue", exporting(""), rule.Environment{})
	require.Len(t, found, 1)

	assert.Equal(t, `exporter "otlphttp" takes the default sending_queue, which is held in memory; `+
		"a restart drops whatever is still queued", found[0].Message)
	assert.Equal(t, "persistence takes three steps: declare a storage extension such as file_storage, "+
		"list it in service.extensions so the collector starts it, and name it here as sending_queue.storage",
		found[0].Hint)
	assert.Equal(t, "exporters.otlphttp.sending_queue", found[0].Path)
	assert.Equal(t, diag.Info, found[0].Severity)

	written := checkRule(t, "no-persistent-queue",
		exporting("    sending_queue:\n      queue_size: 5000"), rule.Environment{})
	require.Len(t, written, 1)
	assert.Equal(t, `exporter "otlphttp" has a sending_queue with no storage, so the queue is held in memory; `+
		"a restart drops whatever is still queued", written[0].Message)
}

func TestNoPersistentQueueStaysQuiet(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		// The schema says the debug exporter has no queue, so there is none to
		// lose.
		"an exporter with no queue": `
receivers: {otlp: }
exporters: {debug: }
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [debug]}
`,
		// Writing a queue does not give an exporter one. The schema says debug
		// has no sending_queue, so there is nothing to persist and
		// unknown-field is what has something to say about the block.
		"a queue on an exporter that has none": `
receivers: {otlp: }
exporters:
  debug:
    sending_queue:
      queue_size: 5000
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [debug]}
`,
		// unused-component is what has something to say about this one; an
		// exporter no pipeline reaches is never instantiated.
		"an exporter no pipeline references": `
receivers: {otlp: }
exporters:
  debug: {}
  otlphttp:
    endpoint: http://backend:4318
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [debug]}
`,
		// A merge may be what supplies the storage, and the document is read as
		// written.
		"a queue built from a merge key": `
receivers: {otlp: }
exporters:
  otlphttp:
    endpoint: http://backend:4318
    sending_queue:
      <<: &queue {storage: file_storage}
      queue_size: 5000
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [otlphttp]}
`,
		"settings built from a merge key": `
receivers: {otlp: }
exporters:
  otlphttp:
    <<: &base {sending_queue: {storage: file_storage}}
    endpoint: http://backend:4318
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [otlphttp]}
`,
		// An anchor is the same queue written once and used twice.
		"a queue behind an anchor": `
receivers: {otlp: }
exporters:
  otlphttp:
    endpoint: http://backend:4318
    sending_queue: &queue
      storage: file_storage
  otlphttp/second:
    endpoint: http://other:4318
    sending_queue: *queue
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [otlphttp, otlphttp/second]}
`,
		// Without a service block nothing is instantiated, and service-required
		// already says so.
		"no service block": `
exporters:
  otlphttp:
    endpoint: http://backend:4318
`,
	}

	for name, src := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Empty(t, checkRule(t, "no-persistent-queue", src, rule.Environment{}))
		})
	}
}

// A component the schema resolved no fields for is not a component known to
// have no settings: the datadog exporter, which has a queue, sits in that
// bucket next to nop. What the config writes is then the only evidence there
// is. testSchema's logging exporter has the same shape.
func TestNoPersistentQueueWithoutFields(t *testing.T) {
	t.Parallel()

	const declared = `
receivers: {otlp: }
exporters:
  logging:
`

	const wired = `
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [logging]}
`

	quiet := checkRule(t, "no-persistent-queue", declared+wired, rule.Environment{})
	assert.Empty(t, quiet, "an exporter that writes no queue says nothing about having one")

	written := checkRule(t, "no-persistent-queue",
		declared+"    sending_queue:\n      queue_size: 5000\n"+wired, rule.Environment{})
	assert.Len(t, written, 1)
}

// Without a schema there is nothing to say which exporters have a queue, so
// only a queue the config writes itself is evidence of one.
func TestNoPersistentQueueWithoutASchema(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		settings string
		want     int
	}{
		"a queue nobody wrote": {settings: "", want: 0},
		"a queue with no storage": {
			settings: "    sending_queue:\n      queue_size: 5000",
			want:     1,
		},
		"a queue with storage": {
			settings: "    sending_queue:\n      storage: file_storage",
			want:     0,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f, err := config.Parse("test.yaml", []byte(exporting(tt.settings)))
			require.NoError(t, err, "parse")

			r, ok := rule.Lookup("no-persistent-queue")
			require.True(t, ok, "rule is not registered")

			found := rule.Run(r, rule.Context{File: f, Schema: &schema.Schema{}, Index: rule.NewIndex(f)}, r.Severity())
			assert.Len(t, found, tt.want)
		})
	}
}

// Reporting per exporter is the deliberate choice: the fix is written per
// exporter, so the count of findings is the count of edits.
func TestNoPersistentQueueReportsPerExporter(t *testing.T) {
	t.Parallel()

	src := `
receivers: {otlp: }
exporters:
  otlphttp:
    endpoint: http://backend:4318
  otlphttp/second:
    endpoint: http://other:4318
    sending_queue:
      storage: file_storage
  otlphttp/third:
    endpoint: http://third:4318
extensions: {file_storage: }
service:
  extensions: [file_storage]
  pipelines:
    traces: {receivers: [otlp], exporters: [otlphttp, otlphttp/second, otlphttp/third]}
`

	found := checkRule(t, "no-persistent-queue", src, rule.Environment{})
	require.Len(t, found, 2)
	assert.Equal(t, "exporters.otlphttp.sending_queue", found[0].Path)
	assert.Equal(t, "exporters.otlphttp/third.sending_queue", found[1].Path)
}
