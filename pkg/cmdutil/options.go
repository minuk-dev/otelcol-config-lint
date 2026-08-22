package cmdutil

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/spf13/afero"
	"github.com/spf13/cobra"

	"github.com/minuk-dev/otelcol-config-lint/pkg/cmdutil/settings"
)

// GlobalOptions are what every command shares: which settings file to read,
// which filesystem to read it and everything else from, and the file itself
// once it has been read.
//
// They hold nothing about linting. Each command keeps its own flags and its
// own resolved state in its own package, and folds in only the blocks it is
// about; this is the part all of them honour, so the file is read once however
// the commands are nested.
type GlobalOptions struct {
	// Fs is the filesystem the settings file, the config files and any local
	// schema location are read from. A nil Fs means the real one, which is
	// what the binary uses.
	Fs afero.Fs

	// flags
	settingsFile string
	noConfig     bool

	// internal state
	// file is what the commands fold into their flags. It is nil until
	// Prepare has read it, and never nil after: a run without a settings file
	// is a file that says nothing.
	file *settings.File
	// path is where it was read from, and "" when there was none to read.
	path string
}

// RegisterFlags declares --config and --no-config. Every command that reads
// the file honours them: the settings file states rule and schema policy, not
// only lint options.
func (o *GlobalOptions) RegisterFlags(cmd *cobra.Command) {
	flags := cmd.Flags()

	flags.StringVarP(&o.settingsFile, "config", "c", "",
		"settings file (default: "+settings.DefaultName+", searched for here and in each parent)")
	flags.BoolVar(&o.noConfig, "no-config", false, "ignore any settings file and use the flags alone")
}

// Prepare reads the settings file the commands share, so each of them can then
// fold the blocks it is about into its own flags. Reading it twice reads the
// file once, because a subcommand prepares what it inherits as well as itself.
func (o *GlobalOptions) Prepare(cmd *cobra.Command) error {
	if o.file != nil {
		return nil
	}

	file, path, err := o.load()
	if err != nil {
		return err
	}

	// The flat first-release keys are folded into the blocks here rather than
	// in each command, so a file is normalised once and reported once.
	legacy := file.Normalize()

	o.file, o.path = file, path

	if len(legacy) > 0 {
		cmd.PrintErrf("otelcol-config-lint: %s: deprecated top-level keys: %s;"+
			" move them under run, rules, issues or output\n", path, strings.Join(legacy, ", "))
	}

	return nil
}

// Settings is the file the commands fold into their flags, which Prepare has
// read by the time any of them asks for it.
func (o *GlobalOptions) Settings() *settings.File {
	return o.file
}

// SettingsPath is where that file was read from, and "" when there was none to
// read, which is what --verbose reports.
func (o *GlobalOptions) SettingsPath() string {
	return o.path
}

// Fold returns the folder that applies the settings file to cmd's flags,
// leaving alone whatever the command line already stated.
func (o *GlobalOptions) Fold(cmd *cobra.Command) settings.Fold {
	return settings.Fold{Changed: cmd.Flags().Changed}
}

// FS returns the filesystem to read, which is the real one unless the caller
// named another.
func (o *GlobalOptions) FS() afero.Fs {
	if o.Fs == nil {
		return afero.NewOsFs()
	}

	return o.Fs
}

// load reads the settings file and reports where it was read from. When no
// path was given the default file is looked for, and not finding one is not an
// error; an explicitly named file that is missing is.
func (o *GlobalOptions) load() (*settings.File, string, error) {
	//nolint:exhaustruct // an absent file means every option keeps its default
	empty := &settings.File{}

	path := o.settingsFile

	switch {
	case o.noConfig:
		return empty, "", nil
	case path == "":
		path = settings.Find(o.FS(), workingDir())
		if path == "" {
			return empty, "", nil
		}
	}

	src, err := afero.ReadFile(o.FS(), path)
	if err != nil {
		// A discovered file that vanished between the two calls is treated as
		// no file at all, which is what it was a moment ago.
		if o.settingsFile == "" && errors.Is(err, fs.ErrNotExist) {
			return empty, "", nil
		}

		return nil, "", fmt.Errorf("read settings: %w", err)
	}

	file, err := settings.Parse(src)
	if err != nil {
		return nil, "", AsUsageError(fmt.Errorf("%s: %w", path, err))
	}

	return file, path, nil
}

// workingDir is where the search for a settings file starts. A working
// directory that cannot be read leaves the search relative, which finds a file
// sitting right here and nothing above it.
func workingDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}

	return dir
}
