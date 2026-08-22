// Package otelcolconfiglint assembles the otelcol-config-lint command out of
// the packages that implement it: one per subcommand, plus the settings file
// they share and the exit contract the binary reports.
package otelcolconfiglint

import (
	"github.com/spf13/cobra"

	"github.com/minuk-dev/otelcol-config-lint/pkg/cmd/otelcol-config-lint/configschema"
	"github.com/minuk-dev/otelcol-config-lint/pkg/cmd/otelcol-config-lint/list"
	"github.com/minuk-dev/otelcol-config-lint/pkg/cmd/otelcol-config-lint/run"
	"github.com/minuk-dev/otelcol-config-lint/pkg/cmd/otelcol-config-lint/settings"
	"github.com/minuk-dev/otelcol-config-lint/pkg/cmd/otelcol-config-lint/version"
)

// GlobalCmdOptions is what every command shares: the filesystem to read, and
// which settings file to read. It is the settings package's, named here as
// well because it is what an embedder passes to NewCommand.
type GlobalCmdOptions = settings.Options

// NewCommand builds the root command. A nil opts is allowed, in which case a
// zero value is used.
func NewCommand(opts *GlobalCmdOptions) *cobra.Command {
	if opts == nil {
		//nolint:exhaustruct // the flags fill themselves in, and a nil Fs is the real filesystem
		opts = &GlobalCmdOptions{}
	}

	// The root carries no work of its own: every mode is a subcommand, so a
	// bare invocation prints the help that lists them.
	cmd := &cobra.Command{
		Use:     "otelcol-config-lint <command> [flags]",
		Short:   "Validate OpenTelemetry Collector config files against a specific collector release",
		Version: version.Current(),
		// The subcommands print command-level errors themselves, with the tool
		// prefix, and stay quiet about cmdutil.ErrFilesInvalid.
		SilenceErrors: true,
		// Findings are not usage errors, so the usage text is printed only
		// where it helps: bad flags and a missing argument.
		SilenceUsage: true,
	}

	// Setting Version above gives the root command a built-in --version flag;
	// this keeps it printing what the "version" subcommand prints.
	cmd.SetVersionTemplate(version.Template)

	cmd.AddCommand(
		run.NewCommand(opts),
		list.NewCommand(opts),
		configschema.NewCommand(),
		version.NewCommand(),
	)

	cmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		cmd.PrintErr(cmd.UsageString())

		return err
	})

	return cmd
}
