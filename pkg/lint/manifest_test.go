package lint_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/lint"
)

func TestKubernetesManifestSkipping(t *testing.T) {
	t.Parallel()

	const manifest = "apiVersion: v1\nkind: Service\nmetadata: {name: sink}\n"

	tests := map[string]struct {
		source string
		skip   bool
	}{
		"service":                     {manifest, true},
		"multiple manifests":          {manifest + "---\n" + manifest, true},
		"empty trailing document":     {manifest + "---\n", true},
		"malformed trailing document": {manifest + "---\n[", false},
		"mixed stream":                {manifest + "---\nreceivers: {}\n", false},
		"collector":                   {"receivers: {}\n", false},
		"missing kind":                {"apiVersion: v1\n", false},
		"non string kind":             {"apiVersion: v1\nkind: []\n", false},
		"duplicate keys":              {manifest + "kind: Deployment\n", false},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			result := lint.New(lint.Options{}).Lint(context.Background(), "input.yaml", []byte(tt.source))
			assert.Equal(t, tt.skip, result.Status == lint.Skipped)

			if tt.skip {
				assert.Empty(t, result.Diagnostics)
				assert.Contains(t, result.Message(), "Kubernetes manifest")
			}
		})
	}
}

func TestManifestSkipReasonOutput(t *testing.T) {
	t.Parallel()

	result := lint.New(lint.Options{}).Lint(context.Background(), "service.yaml", []byte("apiVersion: v1\nkind: Service"))

	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			var output bytes.Buffer

			formatter, err := lint.NewFormatter(format, &output, lint.FormatterOptions{Verbose: true})
			require.NoError(t, err)
			require.NoError(t, formatter.Result(result))
			require.NoError(t, formatter.Finish(lint.Summary{}))
			assert.Contains(t, output.String(), "Kubernetes manifest")
		})
	}
}
