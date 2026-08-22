// Package otelcolconfiglint implements the otelcol-config-lint command: flag
// parsing, file discovery, settings files and result reporting.
package otelcolconfiglint

import (
	"errors"
	"strings"

	"github.com/spf13/afero"
	"github.com/spf13/cobra"

	"github.com/minuk-dev/otelcol-config-lint/pkg/version"
)

// Errors reported for bad input.
var (
	// ErrNoInput reports that no file, directory or "-" was given.
	ErrNoInput = errors.New("no files or directories specified")
	// ErrNoSchemas reports that no schema version could be found.
	ErrNoSchemas = errors.New("no schemas available")
	// ErrNoYAMLFiles reports that the given paths held nothing to lint.
	ErrNoYAMLFiles = errors.New("no YAML files found")

	// ErrFilesInvalid ends the run with ExitInvalid. It carries no message
	// worth printing: the formatter has already reported every finding, so
	// the caller should map it to an exit code and stay quiet.
	ErrFilesInvalid = errors.New("at least one file is invalid")
)

// Exit codes, following the convention linters are expected to use in CI.
const (
	// ExitOK reports that every file passed.
	ExitOK = 0
	// ExitInvalid reports that at least one file failed the gate.
	ExitInvalid = 1
	// ExitUsage reports that the command could not run at all.
	ExitUsage = 2
)

// ExitCode maps the error a command run returned to a process exit code.
func ExitCode(err error) int {
	switch {
	case err == nil:
		return ExitOK
	case errors.Is(err, ErrFilesInvalid):
		return ExitInvalid
	default:
		return ExitUsage
	}
}

// GlobalCmdOptions is what every command shares: which settings file to read,
// and which filesystem to read it and everything else from.
//
// It holds nothing about linting. Each command keeps its own flags and its own
// resolved state in its own options struct, and folds in only the blocks of
// the settings file it is about; this is the part all of them honour, so the
// file is read once however the commands are nested.
type GlobalCmdOptions struct {
	// Fs is the filesystem the settings file, the config files and any local
	// schema location are read from. A nil Fs means the real one, which is
	// what the binary uses.
	Fs afero.Fs

	// flags
	settingsFile string
	noConfig     bool

	// internal state
	// settings is the file the commands fold into their flags. It is nil
	// until Prepare has read it, and never nil after: a run without a file is
	// a settings struct that says nothing.
	settings *settings
	// settingsPath is where the file was read from, and "" when there was
	// none to read.
	settingsPath string
}

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
		Version: version.Version(),
		// The subcommands print command-level errors themselves, with the tool
		// prefix, and stay quiet about ErrFilesInvalid.
		SilenceErrors: true,
		// Findings are not usage errors, so the usage text is printed only
		// where it helps: bad flags and a missing argument.
		SilenceUsage: true,
	}

	// Setting Version above gives the root command a built-in --version flag;
	// this keeps it printing what the "version" subcommand prints.
	cmd.SetVersionTemplate(versionTemplate)

	cmd.AddCommand(
		newRunCommand(newRunCmdOptions(opts)),
		newListCommand(opts),
		newConfigSchemaCommand(newConfigSchemaCmdOptions(opts)),
		newVersionCommand(newVersionCmdOptions(opts)),
	)

	cmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		cmd.PrintErr(cmd.UsageString())

		return err
	})

	return cmd
}

// Prepare reads the settings file the commands share, so each of them can then
// fold the blocks it is about into its own flags. Reading it twice reads the
// file once, because a subcommand prepares what it inherits as well as itself.
func (o *GlobalCmdOptions) Prepare(cmd *cobra.Command) error {
	if o.settings != nil {
		return nil
	}

	s, path, err := o.loadSettings()
	if err != nil {
		return err
	}

	// The flat first-release keys are folded into the blocks here rather than
	// in each command, so a file is normalised once and reported once.
	legacy := s.normalize()

	o.settings, o.settingsPath = s, path

	if len(legacy) > 0 {
		cmd.PrintErrf("otelcol-config-lint: %s: deprecated top-level keys: %s;"+
			" move them under run, rules, issues or output\n", path, strings.Join(legacy, ", "))
	}

	return nil
}

// fold returns the folder that applies the settings file to cmd's flags,
// leaving alone whatever the command line already stated.
func (o *GlobalCmdOptions) fold(cmd *cobra.Command) settingsFold {
	return settingsFold{changed: cmd.Flags().Changed}
}

// fs returns the filesystem to read, which is the real one unless the caller
// named another.
func (o *GlobalCmdOptions) fs() afero.Fs {
	if o.Fs == nil {
		return afero.NewOsFs()
	}

	return o.Fs
}

// registerSettingsFlags declares --config and --no-config. Every command that
// reads the file honours them: the settings file states rule and schema
// policy, not only lint options.
func (o *GlobalCmdOptions) registerSettingsFlags(cmd *cobra.Command) {
	flags := cmd.Flags()

	flags.StringVarP(&o.settingsFile, "config", "c", "",
		"settings file (default: "+DefaultSettingsFile+", searched for here and in each parent)")
	flags.BoolVar(&o.noConfig, "no-config", false, "ignore any settings file and use the flags alone")
}
