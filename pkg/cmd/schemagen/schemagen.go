// Package schemagen implements the schemagen command: flag parsing, the
// harvest of a distribution's modules and writing the schemas out.
//
// A distribution is described by the same OCB builder manifest that builds it,
// so the schema lists exactly the components the binary carries. Every module
// the manifest names is downloaded through the go command -- which is what
// makes a private component work, since GOPROXY, GOPRIVATE and any credentials
// this machine builds with apply unchanged -- along with everything those
// modules require. Components come from the metadata.yaml each one ships, and
// field-level schemas from their Config structs and the config.schema.yaml
// upstream publishes.
package schemagen

import (
	"errors"
	"fmt"
	"io"
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
	errNoManifests    = errors.New("no builder manifests specified")
	errNoFormats      = errors.New("no schema formats specified")
	errUnknownFormat  = errors.New("unknown schema format")
	errSkipped        = errors.New("could not be generated")
	errTwoOutputs     = errors.New("--out and --registry name two different places to write")
	errManyToOneFile  = errors.New("several manifests write several schemas, which needs --registry")
	errNoPrevious     = errors.New("--summary has nothing to compare against without --registry")
	errNothingToPrune = errors.New("--retain has nothing to prune without --registry")
)

// defaultRetainEvery keeps every tenth minor release for good, which is the
// spacing the published registry was seeded at.
const defaultRetainEvery = 10

// Errors reported for bad flag values. Each one is a usage error, so ExitCode
// maps it to ExitUsage the way a bad flag is mapped.
var (
	// ErrNoManifests reports that no builder manifest was given to read a
	// distribution out of.
	ErrNoManifests error = usageError{errNoManifests}
	// ErrNoFormats reports that --formats was emptied out.
	ErrNoFormats error = usageError{errNoFormats}
	// ErrTwoOutputs reports --out and --registry given together.
	ErrTwoOutputs error = usageError{errTwoOutputs}
	// ErrManyToOneFile reports several manifests written to a single file.
	ErrManyToOneFile error = usageError{errManyToOneFile}
	// ErrNoPrevious reports --summary given without a registry to read the
	// previously served release out of.
	ErrNoPrevious error = usageError{errNoPrevious}
	// ErrNothingToPrune reports --retain given without a registry to apply it to.
	ErrNothingToPrune error = usageError{errNothingToPrune}
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

// commandTimeout bounds one go command: resolving a large distribution
// downloads a few hundred modules, so it is generous.
const commandTimeout = 10 * time.Minute

// Options holds everything the command was asked to do. The fields are filled
// in by RegisterFlags and then by Prepare, in that order.
type Options struct {
	// flags
	builders    []string
	outFile     string
	registryDir string
	formats     string
	summaryFile string
	retain      int
	retainEvery int
	cacheDir    string
	timeout     time.Duration

	// internal state. The schema is the command's output and progress is not,
	// so they go to different streams: "--out -" has to be pipeable.
	out      io.Writer
	progress io.Writer
	// diffs is what each generated distribution changes against the release
	// the registry served before it, collected for --summary.
	diffs []*schema.Diff
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
		Example: `  schemagen --builder builder.yaml --out schema.yaml
  schemagen --builder core=otelcol/manifest.yaml --builder contrib=otelcol-contrib/manifest.yaml \
    --registry ../otelcol-config-schemas`,
		// Every input is a flag. A stray argument -- "schemagen manifest.yaml"
		// for "--builder manifest.yaml" -- is a usage error like any bad flag,
		// so it is reported the same way rather than through cobra's own path.
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
		// only where it helps: bad flags and a missing --builder.
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

	flags.StringSliceVar(&o.builders, "builder", nil,
		"OCB builder manifest describing a distribution, as \"[name=]path\";\n"+
			"name overrides what the registry files it under; repeat for several")
	flags.StringVar(&o.outFile, "out", "-",
		"file to write the schema to, or \"-\" for stdout; one manifest only")
	flags.StringVar(&o.registryDir, "registry", "",
		"registry directory to write <distribution>/<version>.<format> into,\n"+
			"alongside the index.json listing them")
	flags.StringVar(&o.formats, "formats", "yaml,json", "schema formats to write: yaml, json")
	flags.StringVar(&o.summaryFile, "summary", "",
		"file to write a Markdown summary of what the generated releases add,\n"+
			"remove, rename and restabilise, or \"-\" for stdout; needs --registry")
	flags.IntVar(&o.retain, "retain", 0,
		"keep only the newest n releases of each distribution in the registry;\n"+
			"0 keeps every release")
	flags.IntVar(&o.retainEvery, "retain-every", defaultRetainEvery,
		"never drop a release whose minor version is a multiple of n, however\n"+
			"old it is; 0 keeps no milestones")
	flags.StringVar(&o.cacheDir, "cache", defaultCacheDir(), "directory to resolve the modules in")
	flags.DurationVar(&o.timeout, "timeout", commandTimeout, "timeout for one go command")
}

// Prepare wires up what the run needs beyond the flags: where the schema is
// written and where progress is reported.
func (o *Options) Prepare(cmd *cobra.Command) error {
	o.out = cmd.OutOrStdout()
	o.progress = cmd.ErrOrStderr()

	if o.timeout <= 0 {
		o.timeout = commandTimeout
	}

	return nil
}

// Run generates a schema for every distribution it was given a manifest for.
// Prepare is expected to have run first; a caller that composed the options
// itself gets the defaults instead of a half-built run.
func (o *Options) Run(cmd *cobra.Command) error {
	if o.out == nil || o.progress == nil {
		err := o.Prepare(cmd)
		if err != nil {
			return err
		}
	}

	manifests := o.builders
	if len(manifests) == 0 {
		cmd.PrintErr(cmd.UsageString())

		return ErrNoManifests
	}

	// Everything the flags can disagree about is settled before the first
	// download, so a typo does not surface after several minutes of fetching.
	formats, err := o.parseFormats()
	if err != nil {
		return err
	}

	err = o.checkDestination(manifests)
	if err != nil {
		return err
	}

	var skipped []string

	for _, builder := range manifests {
		// A distribution is skipped rather than fatal: a run is usually given
		// every manifest a registry serves, and one that cannot be resolved --
		// a module this machine cannot reach, say -- should not discard the
		// schemas the others produced.
		err := o.generate(builder, formats)
		if err != nil {
			o.logf("  %s: skipped (%v)\n", builder, err)
			skipped = append(skipped, builder)

			continue
		}
	}

	if o.registryDir != "" {
		// Pruning runs before the index so the index is written from what the
		// registry is left holding, which is the whole point of rebuilding it
		// by listing the directory.
		err = o.prune()
		if err != nil {
			return err
		}

		err = o.writeIndex()
		if err != nil {
			return err
		}
	}

	err = o.writeSummary()
	if err != nil {
		return err
	}

	// Carrying on past a manifest that failed is what keeps it from discarding
	// the distributions that did generate; it is not a way to succeed at less
	// than was asked for. The run ends in failure with the rest written, so a
	// registry is never quietly published a distribution short.
	if len(skipped) > 0 {
		return fmt.Errorf("%d of %d manifests %w: %s",
			len(skipped), len(manifests), errSkipped, strings.Join(skipped, ", "))
	}

	return nil
}

// checkDestination settles where the schemas are written. A run either writes
// one distribution, to a file or to stdout, or fills a registry with as many as
// it was given manifests; asking for both says two different things.
func (o *Options) checkDestination(manifests []string) error {
	switch {
	case o.registryDir != "" && o.outFile != "" && o.outFile != stdoutMarker:
		return ErrTwoOutputs
	case o.registryDir == "" && len(manifests) > 1:
		return ErrManyToOneFile
	// Both read the registry back: the summary for the release it served
	// before this run, the retention policy for the ones it still holds.
	case o.registryDir == "" && o.summaryFile != "":
		return ErrNoPrevious
	case o.registryDir == "" && o.retain > 0:
		return ErrNothingToPrune
	case o.registryDir == "":
		return nil
	}

	err := os.MkdirAll(o.registryDir, dirPerm)
	if err != nil {
		return fmt.Errorf("create registry directory: %w", err)
	}

	return nil
}

// generate reads one manifest and writes the distribution it describes.
func (o *Options) generate(builder string, formats []schema.Format) error {
	name, path := splitBuilder(builder)

	man, err := readManifest(path, name)
	if err != nil {
		return err
	}

	o.logf("%s: %s %s\n", path, man.Dist.Name, man.collectorVersion())

	cat, err := o.build(man)
	if err != nil {
		return err
	}

	if o.registryDir != "" {
		// Before the write, so the comparison is against what the registry
		// served up to now even when a release is regenerated in place.
		o.summarise(cat)

		return o.writeRegistry(cat, formats)
	}

	return o.writeFile(cat, formats)
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

// logf reports progress, on the error stream: the schema is what this command
// outputs, and "--out -" is meant to be piped.
func (o *Options) logf(format string, args ...any) {
	_, _ = fmt.Fprintf(o.progress, format, args...)
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
