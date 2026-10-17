package lint_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/lint"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/debugexporterverbosity"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/unknowntoplevelkey"
)

func TestInlineSuppression(t *testing.T) {
	t.Parallel()

	const config = `exporters:
  debug:
    verbosity: detailed
service:
  pipelines:
    logs/mismatch:
      exporters:
        # otelcol-config-lint:disable-next-line debug-exporter-verbosity
        - debug
    logs/other:
      exporters: [debug]
`

	linter := lint.New(lint.Options{Rules: []rule.Rule{debugexporterverbosity.New()}, FailOn: diag.Warning})
	result := linter.Lint(t.Context(), "config.yaml", []byte(config))
	require.Equal(t, lint.Invalid, result.Status)
	require.Len(t, result.Diagnostics, 1)
	require.Contains(t, result.Diagnostics[0].Path, "logs/other")
	require.Equal(t, 11, result.Diagnostics[0].Position.Line)

	single, _, _ := strings.Cut(config, "    logs/other:")
	result = linter.Lint(t.Context(), "config.yaml", []byte(single))
	require.Equal(t, lint.Valid, result.Status)
	require.Empty(t, result.Diagnostics)

	embedded := lint.New(lint.Options{Rules: []rule.Rule{debugexporterverbosity.New()},
		Embedded: true, FailOn: diag.Warning})
	source := "apiVersion: v1\nkind: ConfigMap\ndata:\n  collector.yaml: |\n    " +
		strings.ReplaceAll(config, "\n", "\n    ")
	result = embedded.Lint(t.Context(), "map.yaml", []byte(source))
	require.Len(t, result.Diagnostics, 1)
	require.Equal(t, 15, result.Diagnostics[0].Position.Line)
}

func TestInlineSuppressionBoundaries(t *testing.T) {
	t.Parallel()

	const prefix = "# otelcol-config-lint:disable-next-line "
	for _, tt := range []struct {
		name, source string
		want         int
		invalid      bool
	}{
		{name: "plain comment", source: prefix + "unknown-top-level-key\nstray: true\n", want: 0, invalid: false},
		{name: "blank line", source: prefix + "unknown-top-level-key\n\nstray: true\n",
			want: 1, invalid: false},
		{name: "other rule", source: prefix + "debug-exporter-verbosity\nstray: true\n",
			want: 1, invalid: false},
		{name: "unknown rule", source: prefix + "typo\nstray: true\n",
			want: 2, invalid: true},
		{name: "missing name", source: "# otelcol-config-lint:disable-next-line\nstray: true\n",
			want: 2, invalid: true},
		{name: "unsupported directive",
			source: "# otelcol-config-lint:disable unknown-top-level-key\nstray: true\n",
			want:   2, invalid: true},
		{name: "literal string",
			source: "exporters:\n  debug:\n    text: |\n      " + prefix +
				"unknown-top-level-key\n# ordinary comment\nstray: true\n",
			want: 1, invalid: false},
		{name: "quoted string",
			source: "exporters:\n  debug:\n    text: \"first\n      " + prefix + "typo\n      last\"\nstray: true\n",
			want:   1, invalid: false},
		{name: "alias source",
			source: prefix + "unknown-top-level-key\nstray: &value true\nother: *value\n",
			want:   1, invalid: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			linter := lint.New(lint.Options{Rules: []rule.Rule{unknowntoplevelkey.New(), debugexporterverbosity.New()}})
			result := linter.Lint(t.Context(), "config.yaml", []byte(tt.source))
			require.Len(t, result.Diagnostics, tt.want)

			found := false
			for _, d := range result.Diagnostics {
				found = found || d.Rule == "invalid-suppression"
			}

			require.Equal(t, tt.invalid, found)
		})
	}
}

func TestInlineSuppressionWithFilePolicy(t *testing.T) {
	t.Parallel()

	const source = `exporters:
  debug: {}
service:
  pipelines:
    logs/suppressed:
      # otelcol-config-lint:disable-next-line debug-exporter-verbosity
      exporters: [debug]
    logs/other:
      exporters: [debug]
`

	linter := lint.New(lint.Options{
		Rules: []rule.Rule{debugexporterverbosity.New()},
		RulePolicy: func(string) map[string]diag.Severity {
			return map[string]diag.Severity{"debug-exporter-verbosity": diag.Error}
		},
		MinSeverity: diag.Error,
		FailOn:      diag.Error,
	})
	result := linter.Lint(t.Context(), "config.yaml", []byte(source))
	require.Equal(t, lint.Invalid, result.Status)
	require.Len(t, result.Diagnostics, 1)
	require.Contains(t, result.Diagnostics[0].Path, "logs/other")
	require.Equal(t, diag.Error, result.Diagnostics[0].Severity)

	single, _, _ := strings.Cut(source, "    logs/other:")
	result = linter.Lint(t.Context(), "config.yaml", []byte(single))
	require.Equal(t, lint.Valid, result.Status)
	require.Empty(t, result.Diagnostics)
}
