package rule_test

import (
	"strings"
	"testing"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// hints reports whether any finding carries a hint mentioning substr.
func hints(found diag.Diagnostics, substr string) bool {
	for _, d := range found {
		if strings.Contains(d.Hint, substr) {
			return true
		}
	}

	return false
}

func TestUndefinedExtensionReference(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		src  string
		want string // a phrase the finding has to carry
		hint string // a phrase its hint has to carry
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
			want: `exporter "otlp" references storage extension "file_storage" ` +
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
			want: `exporter "otlp" references auth extension "oauth2client" ` +
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
			want: `references storage extension "file_storage" which is declared but ` +
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
			want: `references storage extension "batch" which is not declared under extensions`,
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
			want: `receiver "otlp" references auth extension "oidc"`,
			hint: "no extensions are declared in this config",
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
			want: `references storage extension "filestorage/wal" which is not declared under extensions`,
			hint: `did you mean "file_storage/wal"?`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			found := checkRule(t, "undefined-extension-reference", tt.src, rule.Environment{})
			if !reports(found, tt.want) {
				t.Errorf("want a finding carrying %q, got %+v", tt.want, found)
			}

			if !hints(found, tt.hint) {
				t.Errorf("want a hint carrying %q, got %+v", tt.hint, found)
			}
		})
	}
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

			if found := checkRule(t, "undefined-extension-reference", src, rule.Environment{}); len(found) > 0 {
				t.Errorf("expected no findings, got %+v", found)
			}
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

	found := checkRule(t, "undefined-extension-reference", src, rule.Environment{})
	if len(found) != 2 {
		t.Fatalf("want a finding for each exporter, got %+v", found)
	}
}

// unused-component reports an extension the service block does not list. Once
// a component's settings name it, the two rules would be reporting the same
// line, and undefined-extension-reference is the one that explains it.
func TestSettingsReferenceSilencesUnusedComponent(t *testing.T) {
	t.Parallel()

	src := `
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
`

	if found := checkRule(t, "unused-component", src, rule.Environment{}); len(found) > 0 {
		t.Errorf("an extension named by a component's settings is used, got %+v", found)
	}
}
