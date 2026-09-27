package lint_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/lint"
)

// TestTAPOutputIsWellFormedWhenEmpty covers the run with nothing in it: the
// plan is the whole output, and the blank line that used to follow it is
// something a strict TAP consumer need not accept.
func TestTAPOutputIsWellFormedWhenEmpty(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	f, err := lint.NewFormatter("tap", &buf, lint.FormatterOptions{Verbose: false, Summary: false, Color: false})
	require.NoError(t, err)
	require.NoError(t, f.Finish(lint.Summary{}))
	assert.Equal(t, "1..0\n", buf.String())

	buf.Reset()

	f, err = lint.NewFormatter("tap", &buf, lint.FormatterOptions{Verbose: false, Summary: false, Color: false})
	require.NoError(t, err)
	require.NoError(t, f.Result(lint.Result{Path: "a.yaml", Status: lint.Valid, Diagnostics: nil, Err: nil}))
	require.NoError(t, f.Finish(lint.Summary{}))
	assert.Equal(t, "1..1\nok 1 - a.yaml\n", buf.String())
}

func TestGitHubOutputLimitsAndSpreadsAnnotations(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	f, err := lint.NewFormatter("github", &buf, lint.FormatterOptions{})
	require.NoError(t, err)

	var repeated diag.Diagnostics
	for i := 1; i <= 12; i++ {
		repeated = append(repeated, diag.Diagnostic{
			Rule: "binds", Severity: diag.Warning, Message: "binds all interfaces",
			Position: diag.Position{File: "a.yaml", Line: i, Column: 1},
		})
	}
	require.NoError(t, f.Result(lint.Result{Path: "a.yaml", Status: lint.Valid, Diagnostics: repeated}))
	require.NoError(t, f.Result(lint.Result{Path: "b.yaml", Status: lint.Valid, Diagnostics: diag.Diagnostics{
		{Rule: "binds", Severity: diag.Warning, Message: "binds all interfaces", Position: diag.Position{File: "b.yaml", Line: 1, Column: 1}},
		{Rule: "tls", Severity: diag.Warning, Message: "insecure TLS", Position: diag.Position{File: "b.yaml", Line: 2, Column: 1}},
	}}))
	require.NoError(t, f.Finish(lint.Summary{}))

	assert.Equal(t, 10, bytes.Count(buf.Bytes(), []byte("::warning file=")))
	assert.Contains(t, buf.String(), "::warning file=b.yaml,line=1,col=1::")
	assert.Contains(t, buf.String(), "::warning file=b.yaml,line=2,col=1::")
	assert.Contains(t, buf.String(), "::notice::10 of 14 warnings shown")
}

func TestGitHubOutputKeepsLaterFilesAndCapsErrors(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	f, err := lint.NewFormatter("github", &buf, lint.FormatterOptions{})
	require.NoError(t, err)

	for i := range 11 {
		require.NoError(t, f.Result(lint.Result{Path: "a.yaml", Status: lint.Valid, Diagnostics: diag.Diagnostics{{
			Rule: string(rune('a' + i)), Severity: diag.Error, Message: "invalid setting",
			Position: diag.Position{File: "a.yaml", Line: i + 1, Column: 1},
		}}}))
	}
	require.NoError(t, f.Result(lint.Result{Path: "b.yaml", Status: lint.Error, Err: assert.AnError}))
	require.NoError(t, f.Finish(lint.Summary{}))

	assert.Equal(t, 10, bytes.Count(buf.Bytes(), []byte("::error file=")))
	assert.Contains(t, buf.String(), "::error file=b.yaml::")
	assert.Contains(t, buf.String(), "::notice::10 of 12 errors shown")
}
