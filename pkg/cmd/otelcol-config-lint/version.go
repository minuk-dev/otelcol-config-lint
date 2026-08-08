package otelcolconfiglint

import (
	"github.com/spf13/cobra"

	"github.com/minuk-dev/otelcol-config-lint/pkg/version"
)

// versionTemplate renders the built-in --version flag the same way the
// "version" subcommand prints it, so the two never drift apart.
const versionTemplate = "otelcol-config-lint {{.Version}}\n"

// newVersionCommand builds the "version" subcommand.
func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the linter version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			cmd.Printf("otelcol-config-lint %s\n", version.Version())
		},
	}
}
