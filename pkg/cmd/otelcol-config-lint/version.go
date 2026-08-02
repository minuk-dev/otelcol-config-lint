package otelcolconfiglint

import (
	"github.com/spf13/cobra"
)

// Version is the linter's own version.
//
//nolint:gochecknoglobals // injected at build time with -ldflags
var Version = "dev"

// versionTemplate renders the built-in --version flag the same way the
// "version" subcommand prints it, so the two never drift apart.
const versionTemplate = "otelcol-config-lint {{.Version}}\n"

// newVersionCommand builds the "version" subcommand.
func newVersionCommand() *cobra.Command {
	return &cobra.Command{ //nolint:exhaustruct // cobra's zero values are the defaults we want
		Use:   "version",
		Short: "Print the linter version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			cmd.Printf("otelcol-config-lint %s\n", Version)
		},
	}
}
