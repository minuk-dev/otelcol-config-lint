package lint_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
