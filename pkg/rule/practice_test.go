package rule_test

import (
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
