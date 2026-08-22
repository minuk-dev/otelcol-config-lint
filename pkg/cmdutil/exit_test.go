package cmdutil_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/cmdutil"
)

var (
	errBroken  = errors.New("something the user did not ask for")
	errNoInput = cmdutil.NewUsageError("no files or directories specified")
)

// TestUsageErrorKeepsItsMessage pins what the mark is for: a caller can
// classify the failure, and the user still reads the sentence the package that
// raised it wrote.
func TestUsageErrorKeepsItsMessage(t *testing.T) {
	t.Parallel()

	inner := errors.New("read settings: permission denied") //nolint:err113 // a stand-in for an error in hand
	marked := cmdutil.AsUsageError(inner)

	assert.Equal(t, inner.Error(), marked.Error(), "the mark should add nothing to the message")
	require.ErrorIs(t, marked, cmdutil.ErrUsage, "a marked error is a usage failure")
	require.ErrorIs(t, marked, inner, "and is still the error it was")
	assert.Equal(t, "no files or directories specified", errNoInput.Error(),
		"a declared usage error reads as its own sentence")
}

// TestUsageSurvivesWrapping keeps the mark useful where it is actually read:
// several frames above the command that made it.
func TestUsageSurvivesWrapping(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("collect files: %w", errNoInput)

	require.ErrorIs(t, err, cmdutil.ErrUsage)
	require.ErrorIs(t, err, errNoInput)
}

// TestAnUnmarkedFailureIsNotUsage is the other half of the contract: a failure
// nobody classified is the environment's, not the invocation's.
func TestAnUnmarkedFailureIsNotUsage(t *testing.T) {
	t.Parallel()

	require.NotErrorIs(t, errBroken, cmdutil.ErrUsage)
	require.NoError(t, cmdutil.AsUsageError(nil), "nothing to mark is nothing to return")
}

// TestExitCodesOfEachCondition pins the mapping the binary reports.
func TestExitCodesOfEachCondition(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		in   error
		want int
	}{
		"a clean run":       {in: nil, want: cmdutil.ExitOK},
		"findings":          {in: cmdutil.ErrFilesInvalid, want: cmdutil.ExitInvalid},
		"wrapped findings":  {in: fmt.Errorf("lint: %w", cmdutil.ErrFilesInvalid), want: cmdutil.ExitInvalid},
		"a usage failure":   {in: errNoInput, want: cmdutil.ExitUsage},
		"an unmarked error": {in: errBroken, want: cmdutil.ExitUsage},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, cmdutil.ExitCode(tt.in))
		})
	}
}
