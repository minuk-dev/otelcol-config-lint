// Package otelcolconfiglint implements the otelcol-config-lint command: flag
// parsing, file discovery, settings files and result reporting.
package otelcolconfiglint

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"runtime"
	"strings"

	"github.com/samber/mo"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/catalog"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/lint"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
	"github.com/minuk-dev/otelcol-config-lint/pkg/scanner"
	"github.com/minuk-dev/otelcol-config-lint/pkg/sets"
	"github.com/minuk-dev/otelcol-config-lint/pkg/termutil"
)

// Errors reported for bad flag values.
var (
	// ErrUnknownRule names a rule that does not exist.
	ErrUnknownRule = errors.New("unknown rule")
	// ErrBadSeverityPair reports a --severity argument that is not rule=level.
	ErrBadSeverityPair = errors.New("not in rule=level form")
	// ErrNoInput reports that no file, directory or "-" was given.
	ErrNoInput = errors.New("no files or directories specified")
	// ErrNoCatalogs reports that no catalog version could be found.
	ErrNoCatalogs = errors.New("no catalogs available")
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

// maxDefaultWorkers caps the default parallelism; beyond this the run is bound
// by reading files, not by checking them.
const maxDefaultWorkers = 8

// DefaultSettingsFile is looked for in the working directory when no
// --config flag is given.
const DefaultSettingsFile = ".otelcol-config-lint.yaml"

// Options holds everything the command was asked to do. The fields are filled
// in by RegisterFlags and then by the settings file, in that order.
type Options struct {
	// flags
	collectorVersion string
	catalogLocations []string
	output           string
	settingsFile     string
	disable          string
	severity         string
	exclude          string
	minSeverity      string
	failOn           string
	workers          int
	strict           bool
	ignoreMissing    bool
	summary          bool
	verbose          bool
	noColor          bool
	exitOnError      bool

	// internal state
	store catalog.Store
}

// NewCommand builds the cobra command. A nil opts is allowed, in which case a
// zero value is used.
func NewCommand(opts *Options) *cobra.Command {
	if opts == nil {
		opts = &Options{} //nolint:exhaustruct // every field is filled in by RegisterFlags
	}

	cmd := &cobra.Command{ //nolint:exhaustruct // cobra's zero values are the defaults we want
		Use:     "otelcol-config-lint [flags] <file|dir|->...",
		Short:   "Validate OpenTelemetry Collector config files against a specific collector release",
		Version: Version,
		Example: `  otelcol-config-lint config.yaml
  otelcol-config-lint --collector-version v0.157.0 --summary ./configs
  cat config.yaml | otelcol-config-lint -
  otelcol-config-lint --output json --strict ./configs > report.json`,
		Args: cobra.ArbitraryArgs,
		// Findings are not usage errors, so the usage text is printed only
		// where it helps: bad flags and a missing argument.
		SilenceUsage: true,
		// The caller prints command-level errors itself, with the tool prefix,
		// and stays quiet about ErrFilesInvalid.
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			err := opts.Prepare(cmd)
			if err != nil {
				return err
			}

			err = opts.Run(cmd, args)
			if err != nil {
				return err
			}

			return nil
		},
	}

	// Setting Version above gives the root command a built-in --version flag;
	// this keeps it printing what the "version" subcommand prints.
	cmd.SetVersionTemplate(versionTemplate)

	cmd.AddCommand(newVersionCommand(), newListCommand(opts))

	cmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		cmd.PrintErr(cmd.UsageString())

		return err
	})

	cmd.SetUsageTemplate(cmd.UsageTemplate() + fmt.Sprintf(`
Exit codes:
  %d  every file passed
  %d  at least one file failed
  %d  the command could not run
`, ExitOK, ExitInvalid, ExitUsage))

	opts.RegisterFlags(cmd)

	return cmd
}

// RegisterFlags declares every command line flag on the command.
func (o *Options) RegisterFlags(cmd *cobra.Command) {
	// The groups below are shared with the list subcommands, which take only
	// the flags that change what they print.
	o.registerSettingsFlag(cmd)
	o.registerCatalogLocationFlag(cmd)
	o.registerRuleFlags(cmd)

	flags := cmd.Flags()

	flags.StringVar(&o.collectorVersion, "collector-version", catalog.Latest,
		"collector release to validate against, e.g. v0.157.0")
	flags.StringVar(&o.output, "output", "text", "output format: text, json, junit, tap or github")
	flags.StringVar(&o.exclude, "exclude", "", "comma-separated glob patterns to skip when walking directories")
	flags.StringVar(&o.minSeverity, "min-severity", "info", "lowest severity to report: error, warning or info")
	flags.StringVar(&o.failOn, "fail-on", "error", "severity that makes a file invalid: error, warning or info")
	// Spelled -n before cobra; the shorthand keeps that working.
	flags.IntVarP(&o.workers, "n", "n", defaultWorkers(), "number of files to check in parallel")
	flags.BoolVar(&o.strict, "strict", false, "report unknown component settings as errors")
	flags.BoolVar(&o.ignoreMissing, "ignore-missing-schemas", false,
		"do not fail on components missing from the catalog")
	flags.BoolVar(&o.summary, "summary", false, "print a summary of the results")
	flags.BoolVar(&o.verbose, "verbose", false, "also report files that passed")
	flags.BoolVar(&o.noColor, "no-color", false, "disable coloured output")
	flags.BoolVar(&o.exitOnError, "exit-on-error", false, "stop at the first file that fails")
}

// Prepare folds the settings file into the parsed flags and builds the catalog
// store. Flags given on the command line win over the file.
func (o *Options) Prepare(cmd *cobra.Command) error {
	fileSettings, err := loadSettings(o.settingsFile)
	if err != nil {
		return err
	}

	o.applySettings(fileSettings, cmd.Flags().Changed)

	o.store = catalog.Store{Locations: o.catalogLocations}

	return nil
}

// Run lints the given paths.
func (o *Options) Run(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		cmd.PrintErr(cmd.UsageString())

		return ErrNoInput
	}

	err := o.runLint(cmd, args)
	if err != nil {
		return err
	}

	return nil
}

// registerSettingsFlag declares --config. Every command honours it: the
// settings file states rule and catalog policy, not only lint options.
func (o *Options) registerSettingsFlag(cmd *cobra.Command) {
	cmd.Flags().StringVar(&o.settingsFile, "config", "",
		"settings file (default "+DefaultSettingsFile+" if present)")
}

// registerCatalogLocationFlag declares --catalog-location, shared with
// "list versions".
func (o *Options) registerCatalogLocationFlag(cmd *cobra.Command) {
	cmd.Flags().StringSliceVar(&o.catalogLocations, "catalog-location", nil,
		"where to find catalogs: a directory, a {{.Version}} template, a URL, or \"default\";\n"+
			"repeat to search several in order (default: the built-in catalogs)")
}

// registerRuleFlags declares the severity overrides, shared with "list rules".
func (o *Options) registerRuleFlags(cmd *cobra.Command) {
	flags := cmd.Flags()

	flags.StringVar(&o.disable, "disable", "", "comma-separated rules to turn off")
	flags.StringVar(&o.severity, "severity", "",
		"comma-separated rule=level overrides, e.g. missing-batch=warning")
}

// runLint resolves what to check and how to report it, then does the work.
func (o *Options) runLint(cmd *cobra.Command, paths []string) error {
	files, err := scanner.New(splitList(o.exclude)).Scan(paths)
	if err != nil {
		return fmt.Errorf("collect files: %w", err)
	}

	if files.Len() == 0 {
		return fmt.Errorf("%w in %s", ErrNoYAMLFiles, strings.Join(paths, ", "))
	}

	linter, err := o.newLinter(cmd)
	if err != nil {
		return err
	}

	formatter, err := lint.NewFormatter(o.output, cmd.OutOrStdout(), lint.FormatterOptions{
		Verbose: o.verbose,
		Summary: o.summary,
		Color:   !o.noColor && o.output == "text" && termutil.IsTerminal(cmd.OutOrStdout()),
	})
	if err != nil {
		return fmt.Errorf("create formatter: %w", err)
	}

	err = o.lintAll(cmd, linter, formatter, files)
	if err != nil {
		return err
	}

	return nil
}

// lintAll checks every file and reports the results in path order, returning
// ErrFilesInvalid when the gate was not met.
func (o *Options) lintAll(
	cmd *cobra.Command,
	linter *lint.Linter,
	formatter lint.Formatter,
	files sets.Set[string],
) error {
	var summary lint.Summary

	// Results are buffered so output stays in path order even though the files
	// are checked concurrently.
	results := make(map[string]lint.Result, files.Len())

	if files.Has(scanner.StdinMarker) {
		results[scanner.StdinMarker] = linter.LintReader("stdin", cmd.InOrStdin())
	}

	onDisk := sets.List(files.Difference(sets.New(scanner.StdinMarker)))
	for r := range linter.LintAll(onDisk, o.workers) {
		results[r.Path] = r
	}

	for _, f := range sets.List(files) {
		r := results[f]

		summary.Add(r)

		err := formatter.Result(r)
		if err != nil {
			return fmt.Errorf("report %s: %w", r.Path, err)
		}

		if o.exitOnError && (r.Status == lint.Invalid || r.Status == lint.Error) {
			break
		}
	}

	err := formatter.Finish(summary)
	if err != nil {
		return fmt.Errorf("finish report: %w", err)
	}

	if summary.Failed() {
		return ErrFilesInvalid
	}

	return nil
}

func (o *Options) newLinter(cmd *cobra.Command) (*lint.Linter, error) {
	cat, err := o.loadCatalog(cmd)
	if err != nil {
		return nil, err
	}

	severities, err := o.severityOverrides()
	if err != nil {
		return nil, err
	}

	minSeverity, err := diag.ParseSeverity(o.minSeverity)
	if err != nil {
		return nil, fmt.Errorf("--min-severity: %w", err)
	}

	failOn, err := diag.ParseSeverity(o.failOn)
	if err != nil {
		return nil, fmt.Errorf("--fail-on: %w", err)
	}

	return lint.New(lint.Options{
		Catalog:              cat,
		Availability:         lint.NewVersionIndex(o.store),
		Severities:           severities,
		Strict:               o.strict,
		IgnoreMissingSchemas: o.ignoreMissing,
		MinSeverity:          minSeverity,
		FailOn:               failOn,
	}), nil
}

// loadCatalog resolves the targeted release, falling back to the newest
// catalog that is not newer than the request when there is no exact match.
func (o *Options) loadCatalog(cmd *cobra.Command) (*catalog.Catalog, error) {
	cat, err := o.store.Load(o.collectorVersion)
	if err == nil {
		return cat, nil
	}

	var unknown *catalog.UnknownVersionError
	if !errors.As(err, &unknown) {
		return nil, fmt.Errorf("load catalog: %w", err)
	}

	near, hasNear := unknown.Nearest()
	if !hasNear {
		return nil, fmt.Errorf("load catalog: %w", err)
	}

	cmd.PrintErrf("otelcol-config-lint: no catalog for %s, falling back to %s\n", unknown.Version, near)

	cat, err = o.store.Load(near)
	if err != nil {
		return nil, fmt.Errorf("load catalog %s: %w", near, err)
	}

	return cat, nil
}

// severityOverrides builds the rule severity map from --disable and --severity.
func (o *Options) severityOverrides() (map[string]diag.Severity, error) {
	out := map[string]diag.Severity{}

	for _, name := range splitList(o.disable) {
		if _, ok := rule.Lookup(name); !ok {
			return nil, fmt.Errorf("--disable: %w %q", ErrUnknownRule, name)
		}

		out[name] = diag.Off
	}

	for _, pair := range splitList(o.severity) {
		name, level, found := strings.Cut(pair, "=")
		if !found {
			return nil, fmt.Errorf("--severity: %q is %w", pair, ErrBadSeverityPair)
		}

		if _, ok := rule.Lookup(name); !ok {
			return nil, fmt.Errorf("--severity: %w %q", ErrUnknownRule, name)
		}

		sev, err := diag.ParseSeverity(level)
		if err != nil {
			return nil, fmt.Errorf("--severity %s: %w", name, err)
		}

		out[name] = sev
	}

	return out, nil
}

// settings is the file form of the command line options, so a repository can
// commit its linting policy instead of repeating flags in CI.
type settings struct {
	CollectorVersion string `yaml:"collectorVersion"`
	// CatalogLocations are searched in order before the built-in catalogs.
	CatalogLocations     []string          `yaml:"catalogLocations"`
	Output               string            `yaml:"output"`
	Strict               *bool             `yaml:"strict"`
	IgnoreMissingSchemas *bool             `yaml:"ignoreMissingSchemas"`
	Summary              *bool             `yaml:"summary"`
	MinSeverity          string            `yaml:"minSeverity"`
	FailOn               string            `yaml:"failOn"`
	Disable              []string          `yaml:"disable"`
	Severity             map[string]string `yaml:"severity"`
	Exclude              []string          `yaml:"exclude"`
}

// loadSettings reads a settings file. When path is empty the default file is
// used if it exists, and a missing default is not an error.
func loadSettings(path string) (*settings, error) {
	required := path != ""
	if path == "" {
		path = DefaultSettingsFile
	}

	src, err := os.ReadFile(path)
	if err != nil {
		if !required && errors.Is(err, fs.ErrNotExist) {
			return &settings{}, nil //nolint:exhaustruct // an absent file means every option keeps its default
		}

		return nil, fmt.Errorf("read settings: %w", err)
	}

	var s settings

	dec := yaml.NewDecoder(bytes.NewReader(src))
	dec.KnownFields(true)

	err = dec.Decode(&s)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	return &s, nil
}

// applySettings folds a settings file into the options. changed reports whether
// a flag was given on the command line; those always win over the file.
func (o *Options) applySettings(s *settings, changed func(name string) bool) {
	str := func(name string, dst *string, v string) {
		if !changed(name) {
			*dst = mo.EmptyableToOption(v).OrElse(*dst)
		}
	}
	boolean := func(name string, dst *bool, v *bool) {
		if !changed(name) {
			*dst = mo.PointerToOption(v).OrElse(*dst)
		}
	}

	str("collector-version", &o.collectorVersion, s.CollectorVersion)
	str("output", &o.output, s.Output)
	str("min-severity", &o.minSeverity, s.MinSeverity)
	str("fail-on", &o.failOn, s.FailOn)
	boolean("strict", &o.strict, s.Strict)
	boolean("ignore-missing-schemas", &o.ignoreMissing, s.IgnoreMissingSchemas)
	boolean("summary", &o.summary, s.Summary)

	if !changed("catalog-location") && len(s.CatalogLocations) > 0 {
		o.catalogLocations = append(o.catalogLocations, s.CatalogLocations...)
	}

	if !changed("exclude") && len(s.Exclude) > 0 {
		o.exclude = joinList(o.exclude, strings.Join(s.Exclude, ","))
	}

	// Rule lists merge instead of replacing: the file states the project
	// policy and the flags add to it for a single run.
	if len(s.Disable) > 0 {
		o.disable = joinList(o.disable, strings.Join(s.Disable, ","))
	}

	if len(s.Severity) > 0 {
		pairs := make([]string, 0, len(s.Severity))
		for _, name := range sets.List(sets.KeySet(s.Severity)) {
			pairs = append(pairs, name+"="+s.Severity[name])
		}
		// Later pairs win, so file overrides are listed first.
		o.severity = joinList(strings.Join(pairs, ","), o.severity)
	}
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

func joinList(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	default:
		return a + "," + b
	}
}

func defaultWorkers() int {
	if n := runtime.NumCPU(); n < maxDefaultWorkers {
		return n
	}

	return maxDefaultWorkers
}
