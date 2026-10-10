package invalidcomponentid_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/invalidcomponentid"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/ruletest"
)

func TestDeclarations(t *testing.T) {
	t.Parallel()

	for _, kind := range config.Kinds() {
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()

			found, err := ruletest.Run(invalidcomponentid.New(), kind.Section()+":\n  ' otlp/ ': {}\n")
			require.NoError(t, err)
			require.Len(t, found, 1)
			assert.Equal(t, "invalid-component-id", found[0].Rule)
			assert.Equal(t, diag.Error, found[0].Severity)
			assert.Contains(t, found[0].Message, `" otlp/ "`)
			assert.Equal(t, kind.Section()+". otlp/ ", found[0].Path)
			assert.Equal(t, diag.Position{File: "test.yaml", Line: 2, Column: 3}, found[0].Position)
		})
	}
}

func TestReferences(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		path string
	}{
		{
			name: "service extensions", src: "service:\n  extensions: [zpages/]\n",
			path: "service.extensions[0]",
		},
		{
			name: "receiver", src: "service:\n  pipelines:\n    traces:\n      receivers: [otlp/]\n",
			path: "service.pipelines.traces.receivers[0]",
		},
		{
			name: "processor", src: "service:\n  pipelines:\n    traces:\n      processors: [batch/]\n",
			path: "service.pipelines.traces.processors[0]",
		},
		{
			name: "exporter", src: "service:\n  pipelines:\n    traces:\n      exporters: [debug/]\n",
			path: "service.pipelines.traces.exporters[0]",
		},
		{
			name: "storage extension", src: "exporters:\n  otlp:\n    sending_queue:\n      storage: file_storage/\n",
			path: "exporters.otlp.sending_queue.storage",
		},
		{
			name: "auth extension", src: "exporters:\n  otlp:\n    auth:\n      authenticator: basicauth/\n",
			path: "exporters.otlp.auth.authenticator",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			found, err := ruletest.Run(invalidcomponentid.New(), tt.src)
			require.NoError(t, err)
			require.Len(t, found, 1)
			assert.Equal(t, tt.path, found[0].Path)
			assert.Contains(t, found[0].Message, "/\"")
			assert.Positive(t, found[0].Position.Line)
			assert.Positive(t, found[0].Position.Column)
		})
	}
}

func TestValidIdentifiers(t *testing.T) {
	t.Parallel()

	found, err := ruletest.Run(invalidcomponentid.New(), ruletest.Clean)
	require.NoError(t, err)
	assert.Empty(t, found)
}
