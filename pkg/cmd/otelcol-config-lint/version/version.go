// Package version is the command that prints the linter version.
package version

import (
	"github.com/spf13/cobra"

	versionpkg "github.com/minuk-dev/otelcol-config-lint/pkg/version"
)

// Template renders the root command's built-in --version flag the same way
// this subcommand prints it, so the two never drift apart.
const Template = "otelcol-config-lint {{.Version}}\n"

// Current is the version the binary reports, which the root command needs as
// well: it is what fills in Template.
func Current() string {
	return versionpkg.Version()
}

// NewCommand builds the "version" subcommand. It takes nothing: what it prints
// is built into the binary, so there is no settings file to read and no flag
// that could change the answer.
func NewCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the linter version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			cmd.Printf("otelcol-config-lint %s\n", Current())
		},
	}
}
