package lint_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/lint"
)

func TestTextRuleSummary(t *testing.T) {
	t.Parallel()

	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "enabled", false: "disabled"}[enabled], func(t *testing.T) {
			t.Parallel()

			var output bytes.Buffer

			formatter, err := lint.NewFormatter("text", &output, lint.FormatterOptions{Summary: enabled})
			require.NoError(t, err)

			for _, name := range []string{"zeta", "alpha", "zeta", "beta"} {
				require.NoError(t, formatter.Result(lint.Result{Path: "config.yaml", Status: lint.Valid,
					Diagnostics: diag.Diagnostics{{Rule: name, Severity: diag.Warning, Message: "finding"}},
				}))
			}

			require.NoError(t, formatter.Result(lint.Result{Path: "broken.yaml", Status: lint.Error, Err: assert.AnError}))
			require.NoError(t, formatter.Result(lint.Result{Path: "skip.yaml", Status: lint.Skipped}))
			require.NoError(t, formatter.Finish(lint.Summary{Valid: 4, Errors: 1, Skipped: 1, Warnings: 4}))

			if enabled {
				assert.True(t, strings.HasSuffix(output.String(),
					"Findings by rule (shown diagnostics):\n  zeta: 2\n  alpha: 1\n  beta: 1\n"), output.String())
			} else {
				assert.NotContains(t, output.String(), "Findings by rule")
			}
		})
	}
}

func TestEmptyRuleSummary(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer

	formatter, err := lint.NewFormatter("text", &output, lint.FormatterOptions{Summary: true})
	require.NoError(t, err)
	require.NoError(t, formatter.Finish(lint.Summary{}))
	assert.Contains(t, output.String(), "Summary: 0 file(s)")
	assert.NotContains(t, output.String(), "Findings by rule")
}
