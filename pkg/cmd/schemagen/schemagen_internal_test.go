package schemagen

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// TestRunWithoutPrepare covers building the options directly, which only this
// package can do: the run falls back to the defaults instead of writing to a
// nil stream.
func TestRunWithoutPrepare(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	//nolint:exhaustruct // the point is what an unprepared run does
	opts := &options{}
	cmd := &cobra.Command{Use: "schemagen"}
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	require.ErrorIs(t, opts.run(cmd), ErrNoManifests)
}
