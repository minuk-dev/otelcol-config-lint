package undefinedextensionreference_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/ruletest"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/undefinedextensionreference"
)

// check runs the rule over src, which every test in this package starts from.
func check(t *testing.T, src string) diag.Diagnostics {
	t.Helper()

	found, err := ruletest.Run(undefinedextensionreference.New(), src)
	require.NoError(t, err, "parse")

	return found
}

func TestUndefinedExtensionReference(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		src     string
		message string
		hint    string // a phrase the hint has to carry
	}{
		"storage extension nothing declares": {
			src: `
receivers: {otlp: }
exporters:
  otlp:
    endpoint: backend:4317
    sending_queue:
      storage: file_storage
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [otlp]}
`,
			message: `exporter "otlp" references storage extension "file_storage" ` +
				`which is not declared under extensions`,
			hint: "no extensions are declared in this config",
		},
		"authenticator nothing declares": {
			src: `
receivers: {otlp: }
exporters:
  otlp:
    endpoint: backend:4317
    auth:
      authenticator: oauth2client
extensions: {zpages: }
service:
  extensions: [zpages]
  pipelines:
    traces: {receivers: [otlp], exporters: [otlp]}
`,
			message: `exporter "otlp" references auth extension "oauth2client" ` +
				`which is not declared under extensions`,
			hint: "declared extensions: zpages",
		},
		"storage extension declared but not enabled": {
			src: `
receivers: {otlp: }
exporters:
  otlp:
    endpoint: backend:4317
    sending_queue:
      storage: file_storage
extensions: {file_storage: }
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [otlp]}
`,
			message: `exporter "otlp" references storage extension "file_storage" which is declared but ` +
				`missing from service.extensions, so the collector never starts it`,
			hint: `add "file_storage" to service.extensions`,
		},
		"name belongs to another section": {
			src: `
receivers: {otlp: }
processors: {batch: }
exporters:
  otlp:
    endpoint: backend:4317
    sending_queue:
      storage: batch
service:
  pipelines:
    traces: {receivers: [otlp], processors: [batch], exporters: [otlp]}
`,
			message: `exporter "otlp" references storage extension "batch" ` +
				`which is not declared under extensions`,
			hint: `"batch" is declared under processors`,
		},
		"authenticator on a receiver protocol": {
			src: `
receivers:
  otlp:
    protocols:
      grpc:
        auth:
          authenticator: oidc
exporters: {debug: }
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [debug]}
`,
			message: `receiver "otlp" references auth extension "oidc" which is not declared under extensions`,
			hint:    "no extensions are declared in this config",
		},
		"a named instance written without its underscores": {
			src: `
receivers: {otlp: }
exporters:
  otlp:
    endpoint: backend:4317
    sending_queue:
      storage: filestorage/wal
extensions: {file_storage/wal: }
service:
  extensions: [file_storage/wal]
  pipelines:
    traces: {receivers: [otlp], exporters: [otlp]}
`,
			message: `exporter "otlp" references storage extension "filestorage/wal" ` +
				`which is not declared under extensions`,
			hint: `did you mean "file_storage/wal"?`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			found := check(t, tt.src)
			require.Len(t, found, 1)
			assert.Equal(t, tt.message, found[0].Message)
			assert.Contains(t, found[0].Hint, tt.hint)
			assert.Equal(t, diag.Error, found[0].Severity)
		})
	}
}

// A finding has to point at the line the name is written on, not at the
// component, so the reader can go straight to it.
func TestUndefinedExtensionReferenceIsAnchored(t *testing.T) {
	t.Parallel()

	src := `
receivers: {otlp: }
exporters:
  otlp:
    endpoint: backend:4317
    sending_queue:
      storage: file_storage
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [otlp]}
`

	found := check(t, src)
	require.Len(t, found, 1)
	assert.Equal(t, "exporters.otlp.sending_queue.storage", found[0].Path)
	assert.Equal(t, 7, found[0].Position.Line)
	assert.Contains(t, found[0].Docs, "exporter/exporterhelper/README.md")
}

func TestUndefinedExtensionReferenceStaysQuiet(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"declared and enabled": `
receivers: {otlp: }
exporters:
  otlp:
    endpoint: backend:4317
    sending_queue:
      storage: file_storage
    auth:
      authenticator: oauth2client
extensions: {file_storage: , oauth2client: }
service:
  extensions: [file_storage, oauth2client]
  pipelines:
    traces: {receivers: [otlp], exporters: [otlp]}
`,
		"the name is resolved at runtime": `
receivers: {otlp: }
exporters:
  otlp:
    endpoint: backend:4317
    sending_queue:
      storage: ${env:STORAGE}
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [otlp]}
`,
		// A plain "auth: <string>" is not an extension reference. The
		// azureblob and azuremonitor receivers spell their authentication
		// mode that way, and reading it as an extension id would report every
		// one of them.
		"auth written as a plain string": `
receivers:
  azureblob:
    auth: connection_string
exporters: {debug: }
service:
  pipelines:
    logs: {receivers: [azureblob], exporters: [debug]}
`,
		"the queue is off and names nothing": `
receivers: {otlp: }
exporters:
  otlp:
    endpoint: backend:4317
    sending_queue:
      enabled: false
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [otlp]}
`,
		"no service block at all": `
exporters:
  otlp:
    endpoint: backend:4317
    sending_queue:
      storage: file_storage
extensions: {file_storage: }
`,
	}

	for name, src := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Empty(t, check(t, src))
		})
	}
}

// An anchor is how a config that repeats a queue block writes it once, and the
// reference inside it is still a reference.
func TestExtensionReferenceThroughAnAnchor(t *testing.T) {
	t.Parallel()

	src := `
receivers: {otlp: }
exporters:
  otlp:
    endpoint: backend:4317
    sending_queue: &queue
      storage: file_storage
  otlp/second:
    endpoint: other:4317
    sending_queue: *queue
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [otlp, otlp/second]}
`

	found := check(t, src)
	require.Len(t, found, 2, "each exporter names the extension")
}
