package lint_test

import (
	"bytes"
	"encoding/xml"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/lint"
)

func TestJUnitOutputMatchesResultStatus(t *testing.T) {
	t.Parallel()

	diagnostic := func(severity diag.Severity) diag.Diagnostic {
		return diag.Diagnostic{
			Rule: "unknown-field", Severity: severity, Message: "unknown field <endpiont>",
			Position: diag.Position{File: "config.yaml", Line: 6, Column: 9},
		}
	}
	results := []lint.Result{
		{Path: "error.yaml", Status: lint.Invalid, Diagnostics: diag.Diagnostics{diagnostic(diag.Error)}},
		{Path: "warning.yaml", Status: lint.Invalid, Diagnostics: diag.Diagnostics{diagnostic(diag.Warning)}},
		{Path: "info.yaml", Status: lint.Invalid, Diagnostics: diag.Diagnostics{diagnostic(diag.Info)}},
		{Path: "hidden.yaml", Status: lint.Invalid},
		{Path: "multiple.yaml", Status: lint.Invalid, Diagnostics: diag.Diagnostics{
			diagnostic(diag.Error), diagnostic(diag.Warning),
		}},
		{Path: "valid.yaml", Status: lint.Valid},
		{Path: "valid-warning.yaml", Status: lint.Valid, Diagnostics: diag.Diagnostics{diagnostic(diag.Warning)}},
		{Path: "valid-info.yaml", Status: lint.Valid, Diagnostics: diag.Diagnostics{diagnostic(diag.Info)}},
		{Path: "gate-off.yaml", Status: lint.Valid, Diagnostics: diag.Diagnostics{diagnostic(diag.Error)}},
		{Path: "unreadable.yaml", Status: lint.Error, Err: assert.AnError},
		{Path: "skipped.txt", Status: lint.Skipped},
	}

	var buf bytes.Buffer

	f, err := lint.NewFormatter("junit", &buf, lint.FormatterOptions{})
	require.NoError(t, err)

	var summary lint.Summary

	for _, result := range results {
		require.NoError(t, f.Result(result))
		summary.Add(result)
	}

	require.NoError(t, f.Finish(summary))

	var report struct {
		Tests    int `xml:"tests,attr"`
		Failures int `xml:"failures,attr"`
		Errors   int `xml:"errors,attr"`
		Cases    []struct {
			Name     string `xml:"name,attr"`
			Failures []struct {
				Message string `xml:"message,attr"`
				Type    string `xml:"type,attr"`
				Text    string `xml:",chardata"`
			} `xml:"failure"`
			Errors []struct {
				Message string `xml:"message,attr"`
			} `xml:"error"`
		} `xml:"testcase"`
	}

	require.NoError(t, xml.Unmarshal(buf.Bytes(), &report))
	require.Len(t, report.Cases, len(results))
	assert.Equal(t, len(results), report.Tests)
	assert.Equal(t, summary.Invalid, report.Failures)
	assert.Equal(t, summary.Errors, report.Errors)

	failedCases, errorCases := 0, 0

	for i, result := range results {
		t.Run(result.Path, func(t *testing.T) {
			t.Parallel()

			c := report.Cases[i]
			assert.Equal(t, result.Path, c.Name)

			if result.Status == lint.Invalid {
				require.NotEmpty(t, c.Failures)

				if len(result.Diagnostics) == 0 {
					require.Len(t, c.Failures, 1)
					assert.NotEmpty(t, c.Failures[0].Message)
					assert.Contains(t, c.Failures[0].Message, "filtered")
				} else {
					require.Len(t, c.Failures, len(result.Diagnostics))

					for j, d := range result.Diagnostics {
						assert.Equal(t, d.Message, c.Failures[j].Message)
						assert.Equal(t, d.Rule, c.Failures[j].Type)
						assert.Equal(t, d.Position.String()+": "+d.Message, c.Failures[j].Text)
					}
				}
			} else {
				assert.Empty(t, c.Failures)
			}

			if result.Status == lint.Error {
				require.Len(t, c.Errors, 1)
				assert.Equal(t, result.Message(), c.Errors[0].Message)
			} else {
				assert.Empty(t, c.Errors)
			}
		})

		if len(report.Cases[i].Failures) > 0 {
			failedCases++
		}

		if len(report.Cases[i].Errors) > 0 {
			errorCases++
		}
	}

	assert.Equal(t, report.Failures, failedCases)
	assert.Equal(t, report.Errors, errorCases)
}

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

	const firstFileWarnings = 12

	var repeated diag.Diagnostics
	for i := 1; i <= firstFileWarnings; i++ {
		repeated = append(repeated, diag.Diagnostic{
			Rule: "binds", Severity: diag.Warning, Message: "binds all interfaces",
			Position: diag.Position{File: "a.yaml", Line: i, Column: 1},
		})
	}

	require.NoError(t, f.Result(lint.Result{Path: "a.yaml", Status: lint.Valid, Diagnostics: repeated}))
	require.NoError(t, f.Result(lint.Result{Path: "b.yaml", Status: lint.Valid, Diagnostics: diag.Diagnostics{
		{
			Rule: "binds", Severity: diag.Warning, Message: "binds all interfaces",
			Position: diag.Position{File: "b.yaml", Line: 1, Column: 1},
		},
		{
			Rule: "tls", Severity: diag.Warning, Message: "insecure TLS",
			Position: diag.Position{File: "b.yaml", Line: 2, Column: 1},
		},
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

	const firstFileErrors = 11

	for i := range firstFileErrors {
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
