package missingbatch_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/missingbatch"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/ruletest"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// check runs the rule over src, which every test in this package starts from.
func check(t *testing.T, src string) diag.Diagnostics {
	t.Helper()

	found, err := ruletest.Run(missingbatch.New(), src)
	require.NoError(t, err, "parse")

	return found
}

// checkAgainst runs the rule against a schema the caller chooses, for the tests
// about what the targeted release does and does not describe.
func checkAgainst(t *testing.T, src string, s *schema.Schema) diag.Diagnostics {
	t.Helper()

	found, err := ruletest.RunWith(missingbatch.New(), src, ruletest.Options{Schema: s})
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

			found := check(t, exporting(tt.settings))
			assert.Len(t, found, map[bool]int{true: 1, false: 0}[tt.want])
		})
	}
}

// The hint is the whole point of the change: exporter-side batching first, with
// the reason it comes first, and the processor named as what also works.
func TestMissingBatchReadsAsASentence(t *testing.T) {
	t.Parallel()

	const hint = "the batch processor also works but drops data that fails to send"

	found := check(t, exporting(""))
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

	found := check(t, src)
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

	pipeline := check(t, debugging(""))
	require.Len(t, pipeline, 1)
	assert.Equal(t, processorFirst+"these exporters have no sending_queue.batch to batch in", pipeline[0].Hint)
	assert.Contains(t, pipeline[0].Docs, "processor/batchprocessor/README.md")

	mixed := check(t, `
receivers: {otlp: }
exporters:
  debug:
  otlphttp:
    endpoint: http://backend:4318
    sending_queue: {batch: {}}
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [debug, otlphttp]}
`)
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

	found := check(t, `
receivers: {otlp: }
exporters:
  debug:
  otlphttp:
    endpoint: http://backend:4318
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [debug, otlphttp]}
`)
	require.Len(t, found, 1)

	assert.Equal(t, `pipeline "traces" has no batch processor, and none of its exporters batches in sending_queue`,
		found[0].Message)
	assert.Equal(t, processorFirst+"only some of them can take a sending_queue.batch of their own", found[0].Hint)
	assert.Contains(t, found[0].Docs, "processor/batchprocessor/README.md")
}

// The queue batcher is younger than the queue. On a release that has the queue
// and not the batcher, the hint has to name the processor: writing
// sending_queue.batch there is a setting the collector rejects on startup, and
// unknown-field reads the same schema and would say so in the same run.
func TestMissingBatchBeforeTheQueueHadABatcher(t *testing.T) {
	t.Parallel()

	found := checkAgainst(t, exporting(""), &schema.Schema{
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

			assert.Empty(t, check(t, src))
		})
	}
}

// What a schema does not describe is still a pipeline with no batch processor
// and no batcher written under any of its queues, so the finding stands. It is
// the hint that gives way: nothing says sending_queue.batch is a setting these
// exporters would accept, so it names the one fix that needs no schema.
//
// The exporter with no fields is the shape that matters most. The stand-in
// schema's logging entry has it, and so does the datadog exporter's, which
// really does carry a queue and a great many other settings.
func TestMissingBatchWhereTheSchemaSaysNothing(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		src    string
		schema *schema.Schema
	}{
		"no schema at all":           {src: exporting(""), schema: &schema.Schema{}},
		"an exporter with no fields": {src: exportingLogging(), schema: ruletest.Schema()},
		"a type the schema misses":   {src: exportingUnknownType(), schema: ruletest.Schema()},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			found := checkAgainst(t, tt.src, tt.schema)
			require.Len(t, found, 1)
			assert.Equal(t, processorFirst+"these exporters have no sending_queue.batch to batch in", found[0].Hint)
			assert.Contains(t, found[0].Docs, "processor/batchprocessor/README.md")
		})
	}
}

// exportingLogging wires up the exporter the stand-in schema describes with no
// fields, which is the shape the datadog entry has.
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
