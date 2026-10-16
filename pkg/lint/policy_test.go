package lint_test

import (
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/lint"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/debugexporterverbosity"
)

func TestFileRulePolicy(t *testing.T) {
	t.Parallel()

	const src = "exporters:\n  debug: {}\nservice:\n  pipelines:\n    logs:\n      exporters: [debug]\n"

	policy := func(path string) map[string]diag.Severity {
		if path == "sink.yaml" {
			return map[string]diag.Severity{"debug-exporter-verbosity": diag.Off}
		}

		return map[string]diag.Severity{"debug-exporter-verbosity": diag.Warning}
	}

	fsys := afero.NewMemMapFs()
	for _, path := range []string{"sink.yaml", "other.yaml"} {
		require.NoError(t, afero.WriteFile(fsys, path, []byte(src), 0600))
	}

	linter := lint.New(lint.Options{Fs: fsys, Rules: []rule.Rule{debugexporterverbosity.New()},
		RulePolicy: policy, FailOn: diag.Warning})
	results, err := linter.LintAll(t.Context(), []string{"sink.yaml", "other.yaml", "sink.yaml"}, 3)
	require.NoError(t, err)
	require.Equal(t, lint.Valid, results[0].Status)
	require.Empty(t, results[0].Diagnostics)
	require.Equal(t, lint.Invalid, results[1].Status)
	require.Len(t, results[1].Diagnostics, 1)
	require.Equal(t, diag.Warning, results[1].Diagnostics[0].Severity,
		"explicit severity overrides the finding's info level")
	require.Empty(t, results[2].Diagnostics)

	disabled := lint.New(lint.Options{Rules: []rule.Rule{debugexporterverbosity.New()},
		Severities: map[string]diag.Severity{"debug-exporter-verbosity": diag.Off},
		RulePolicy: policy})
	require.Empty(t, disabled.Lint(t.Context(), "other.yaml", []byte(src)).Diagnostics)

	embedded := lint.New(lint.Options{Rules: []rule.Rule{debugexporterverbosity.New()},
		RulePolicy: policy, Embedded: true})
	require.Empty(t, embedded.Lint(t.Context(), "sink.yaml", []byte(`apiVersion: v1
kind: ConfigMap
data:
  collector.yaml: |
    exporters:
      debug: {}
    service:
      pipelines:
        logs:
          exporters: [debug]
`)).Diagnostics)
}
