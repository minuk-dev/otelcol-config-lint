// Package otelcolconfiglint assembles the otelcol-config-lint command out of
// the packages that implement it: one per subcommand, plus the settings file
// they share and the exit contract the binary reports.
package otelcolconfiglint

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/minuk-dev/otelcol-config-lint/pkg/cmd/otelcol-config-lint/configschema"
	"github.com/minuk-dev/otelcol-config-lint/pkg/cmd/otelcol-config-lint/list"
	"github.com/minuk-dev/otelcol-config-lint/pkg/cmd/otelcol-config-lint/run"
	"github.com/minuk-dev/otelcol-config-lint/pkg/cmd/otelcol-config-lint/version"
	"github.com/minuk-dev/otelcol-config-lint/pkg/cmdutil"
)

// The codes a run can end in. They are cmdutil's, named here as well because
// ExitCode returns them and the binary is what reads both.
const (
	// ExitOK reports that every file passed.
	ExitOK = cmdutil.ExitOK
	// ExitInvalid reports that at least one file failed the gate.
	ExitInvalid = cmdutil.ExitInvalid
	// ExitUsage reports that the command could not run at all.
	ExitUsage = cmdutil.ExitUsage
)

// ErrFilesInvalid is what "run" returns when a file failed the gate. It is
// named here as well because it is the one error the binary reads: findings
// have already been printed, so it is mapped to an exit code and nothing else.
var ErrFilesInvalid = run.ErrFilesInvalid

// ExitCode maps the error a command run returned to a process exit code. The
// root is where this lives because it is the only place that knows every
// command, and so the only place that can say what each of their errors means.
func ExitCode(err error) int {
	switch {
	case err == nil:
		return ExitOK
	case errors.Is(err, ErrFilesInvalid):
		return ExitInvalid
	default:
		// Anything else could not run: a failure that is not findings is not a
		// clean run either.
		return ExitUsage
	}
}

// GlobalCmdOptions is what every command shares: the filesystem to read, and
// which settings file to read. It is cmdutil's, named here as well because it
// is what an embedder passes to NewCommand.
type GlobalCmdOptions = cmdutil.GlobalOptions

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
		// prefix, and stay quiet about ErrFilesInvalid.
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
