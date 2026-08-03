// Package schemagen implements the schemagen command: flag parsing, the
// harvest of the upstream sources and writing the schemas out.
//
// Every collector component ships a metadata.yaml declaring its type, class and
// per-signal stability. schemagen downloads the core and contrib source
// archives for a release, reads those files, and writes one schema per
// distribution into a registry directory. Field-level schemas come from the
// components' own Config structs and the config.schema.yaml upstream publishes,
// with the hand-written overlays merged on top.
package schemagen

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// These carry the messages; the exported errors below are what callers match
// on.
var (
	errNoVersions    = errors.New("no collector releases specified")
	errNoFormats     = errors.New("no schema formats specified")
	errUnknownFormat = errors.New("unknown schema format")
)

// Errors reported for bad flag values. Each one is a usage error, so ExitCode
// maps it to ExitUsage the way a bad flag is mapped.
var (
	// ErrNoVersions reports that no release was named to generate a schema for.
	ErrNoVersions error = usageError{errNoVersions}
	// ErrNoFormats reports that --formats was emptied out.
	ErrNoFormats error = usageError{errNoFormats}
)

// Exit codes: the command either produced schemas or it did not.
const (
	// ExitOK reports that every requested release was written.
	ExitOK = 0
	// ExitFailure reports that a schema could not be generated.
	ExitFailure = 1
	// ExitUsage reports that the command could not run at all.
	ExitUsage = 2
)

// usageError marks a bad invocation, as opposed to a harvest that was tried
// and failed, so ExitCode can tell the two apart.
type usageError struct{ err error }

func (e usageError) Error() string { return e.err.Error() }
func (e usageError) Unwrap() error { return e.err }

// ExitCode maps the error a command run returned to a process exit code.
func ExitCode(err error) int {
	var usage usageError

	switch {
	case err == nil:
		return ExitOK
	case errors.As(err, &usage):
		return ExitUsage
	default:
		return ExitFailure
	}
}

// downloadTimeout bounds a single source archive download.
const downloadTimeout = 5 * time.Minute

// Options holds everything the command was asked to do. The fields are filled
// in by RegisterFlags and then by Prepare, in that order.
type Options struct {
	// flags
	versions   string
	outDir     string
	overlayDir string
	formats    string
	cacheDir   string
	timeout    time.Duration

	// internal state
	client *http.Client
	out    io.Writer
}

// NewCommand builds the schemagen command. A nil opts is allowed, in which case
// a zero value is used.
func NewCommand(opts *Options) *cobra.Command {
	if opts == nil {
		opts = &Options{} //nolint:exhaustruct // every field is filled in by RegisterFlags and Prepare
	}

	cmd := &cobra.Command{
		Use:   "schemagen [flags]",
		Short: "Build component schemas from the upstream collector sources",
		Example: `  schemagen --version v0.157.0
  schemagen --version v0.150.0,v0.157.0 --out ../otelcol-config-schemas`,
		// Every input is a flag. A stray argument -- "schemagen v0.157.0" for
		// "--version v0.157.0" -- is a usage error like any bad flag, so it is
		// reported the same way rather than through cobra's own path.
		Args: func(cmd *cobra.Command, args []string) error {
			err := cobra.NoArgs(cmd, args)
			if err != nil {
				cmd.PrintErr(cmd.UsageString())

				return usageError{err}
			}

			return nil
		},
		// main prints command-level errors itself, with the tool prefix.
		SilenceErrors: true,
		// A failed harvest is not a usage error, so the usage text is printed
		// only where it helps: bad flags and a missing --version.
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			err := opts.Prepare(cmd)
			if err != nil {
				return err
			}

			err = opts.Run(cmd)
			if err != nil {
				return err
			}

			return nil
		},
	}

	opts.RegisterFlags(cmd)

	cmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		cmd.PrintErr(cmd.UsageString())

		return usageError{err}
	})

	return cmd
}

// RegisterFlags declares every flag the generator takes.
func (o *Options) RegisterFlags(cmd *cobra.Command) {
	flags := cmd.Flags()

	flags.StringVar(&o.versions, "version", "", "comma-separated collector releases, e.g. v0.157.0")
	flags.StringVar(&o.outDir, "out", "schemas", "registry directory to write schemas into")
	flags.StringVar(&o.overlayDir, "overlays", "overlays", "directory of field-schema overlays")
	flags.StringVar(&o.formats, "formats", "yaml,json", "schema formats to write: yaml, json")
	flags.StringVar(&o.cacheDir, "cache", defaultCacheDir(), "directory to cache downloaded archives in")
	flags.DurationVar(&o.timeout, "timeout", downloadTimeout, "per-download timeout")
}

// Prepare wires up what the run needs beyond the flags: where progress is
// reported and the client the archives are downloaded with.
func (o *Options) Prepare(cmd *cobra.Command) error {
	o.out = cmd.OutOrStdout()
	o.client = &http.Client{Timeout: o.timeout}

	return nil
}

// Run generates a schema for every requested release. Prepare is expected to
// have run first; a caller that composed the options itself gets the defaults
// instead of a half-built run.
func (o *Options) Run(cmd *cobra.Command) error {
	if o.out == nil || o.client == nil {
		err := o.Prepare(cmd)
		if err != nil {
			return err
		}
	}

	versions := splitList(o.versions)
	if len(versions) == 0 {
		cmd.PrintErr(cmd.UsageString())

		return ErrNoVersions
	}

	// The formats are resolved before the first download, so a typo does not
	// surface after several minutes of fetching.
	formats, err := o.parseFormats()
	if err != nil {
		return err
	}

	loaded, err := o.loadOverlays()
	if err != nil {
		return err
	}

	err = os.MkdirAll(o.outDir, dirPerm)
	if err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	var skipped []string

	for _, v := range versions {
		v = schema.Normalize(v)

		// A release is skipped rather than fatal: generating the full tag
		// history means asking for versions one repository tagged and the
		// other did not, and one gap should not discard the rest of the run.
		cat, err := o.build(v)
		if err != nil {
			o.logf("  %s: skipped (%v)\n", v, err)
			skipped = append(skipped, v)

			continue
		}

		applyOverlays(cat, loaded)

		err = o.writeDistributions(cat, formats)
		if err != nil {
			return err
		}
	}

	if len(skipped) > 0 {
		o.logf("skipped %d of %d releases: %s\n", len(skipped), len(versions), strings.Join(skipped, ", "))
	}

	return o.writeIndex()
}

// parseFormats resolves --formats. An unknown name is refused rather than
// written: Schema.Write treats anything that is not JSON as YAML, so a typo
// would otherwise put YAML under a file name the registry never reads back.
func (o *Options) parseFormats() ([]schema.Format, error) {
	names := splitList(o.formats)
	if len(names) == 0 {
		return nil, ErrNoFormats
	}

	out := make([]schema.Format, 0, len(names))

	for _, name := range names {
		format := schema.Format(name)
		if format != schema.YAML && format != schema.JSON {
			return nil, usageError{fmt.Errorf("--formats: %w %q", errUnknownFormat, name)}
		}

		out = append(out, format)
	}

	return out, nil
}

// logf reports progress. schemagen is a developer command whose output is the
// point, but it goes through the command's own writer so nothing prints to a
// stream the caller did not choose.
func (o *Options) logf(format string, args ...any) {
	_, _ = fmt.Fprintf(o.out, format, args...)
}

func defaultCacheDir() string {
	return filepath.Join(os.TempDir(), "otelcol-config-lint-schemagen")
}

func splitList(s string) []string {
	var out []string

	for part := range strings.SplitSeq(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}

	return out
}
