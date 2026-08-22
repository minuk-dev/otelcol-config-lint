package otelcolconfiglint

import (
	"github.com/spf13/cobra"

	"github.com/minuk-dev/otelcol-config-lint/pkg/version"
)

// versionTemplate renders the built-in --version flag the same way the
// "version" subcommand prints it, so the two never drift apart.
const versionTemplate = "otelcol-config-lint {{.Version}}\n"

// VersionCmdOptions is what "version" was asked to do. It has no flags of its
// own and reads no settings file: what it prints is built into the binary.
type VersionCmdOptions struct {
	*GlobalCmdOptions
}

// newVersionCmdOptions builds the options "version" starts from.
func newVersionCmdOptions(global *GlobalCmdOptions) *VersionCmdOptions {
	return &VersionCmdOptions{GlobalCmdOptions: global}
}

// newVersionCommand builds the "version" subcommand.
func newVersionCommand(_ *VersionCmdOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the linter version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			cmd.Printf("otelcol-config-lint %s\n", version.Version())
		},
	}
}
