package config

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestSyntaxErrorKeepsEveryFailure covers what a parse failure is allowed to
// lose. A yaml.TypeError carries one entry per value that would not decode,
// and reporting only the first would hand them to the reader one re-run at a
// time.
func TestSyntaxErrorKeepsEveryFailure(t *testing.T) {
	t.Parallel()

	// Decoding into a yaml.Node does not produce a *yaml.TypeError today; the
	// branch that reads one is there for a decode into a value, and what it
	// does with the entries is worth pinning either way.
	err := syntaxError("bad.yaml", &yaml.TypeError{Errors: []string{
		"line 3: cannot unmarshal !!str into int",
		"line 7: cannot unmarshal !!seq into map",
	}})

	var syn *SyntaxError

	require.ErrorAs(t, err, &syn)

	diags := syn.Diagnostics()
	require.Len(t, diags, 2)
	assert.Equal(t, 3, diags[0].Position.Line)
	assert.Equal(t, "cannot unmarshal !!str into int", diags[0].Message)
	assert.Equal(t, 7, diags[1].Position.Line)
	assert.Equal(t, "cannot unmarshal !!seq into map", diags[1].Message)

	for _, d := range diags {
		assert.Equal(t, "yaml-syntax", d.Rule)
		assert.Equal(t, "bad.yaml", d.Position.File)
	}

	// The error itself still says everything, for a caller that only logs it.
	assert.Contains(t, err.Error(), "bad.yaml:3")
	assert.Contains(t, err.Error(), "bad.yaml:7")

	// Diagnostic stays the first of them, which is what a caller wanting one
	// finding asked for.
	assert.Equal(t, diags[0], syn.Diagnostic())
}

// TestSyntaxErrorSaysWhenItHasNoLine covers a message yaml.v3 formatted some
// other way: the diagnostic lands at the top of the file, and has to say that
// is where the position ran out rather than read as a finding about line 1.
func TestSyntaxErrorSaysWhenItHasNoLine(t *testing.T) {
	t.Parallel()

	for name, msg := range map[string]string{
		"no line prefix":              "yaml: control characters are not allowed",
		"no colon after line":         "yaml: line 4 is where it went wrong",
		"a line that is not a number": "yaml: line four: something went wrong",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var syn *SyntaxError

			require.ErrorAs(t, syntaxError("bad.yaml", errors.New(msg)), &syn) //nolint:err113 // stands in for yaml.v3
			assert.Equal(t, 0, syn.Line)
			assert.True(t, strings.HasSuffix(syn.Msg, unlocated), "message should say it has no line: %q", syn.Msg)
			assert.Contains(t, syn.Msg, strings.TrimPrefix(msg, "yaml: "))
		})
	}
}
