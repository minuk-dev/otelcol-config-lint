// Package otelcolconfiglint implements the otelcol-config-lint command: flag
// parsing, file discovery, settings files and result reporting.
package otelcolconfiglint

import (
	"errors"
	"fmt"
	"runtime"
	"strings"

	"github.com/samber/mo"
	"github.com/spf13/afero"
	"github.com/spf13/cobra"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/lint"
	"github.com/minuk-dev/otelcol-config-lint/pkg/scanner"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
	"github.com/minuk-dev/otelcol-config-lint/pkg/sets"
	"github.com/minuk-dev/otelcol-config-lint/pkg/termutil"
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

// maxDefaultWorkers caps the default parallelism; beyond this the run is bound
// by reading files, not by checking them.
const maxDefaultWorkers = 8

// Options holds everything the command was asked to do. The fields are filled
// in by RegisterFlags and then by the settings file, in that order.
type Options struct {
	// Fs is the filesystem the settings file, the config files and any local
	// schema location are read from. A nil Fs means the real one, which is
	// what the binary uses.
	Fs afero.Fs

	// flags
	collectorVersion string
	distribution     string
	schemaLocations  []string
	output           string
	settingsFile     string
	ruleDefault      string
	enable           []string
	disable          []string
	severity         []string
	exclude          []string
	minSeverity      string
	failOn           string
	memoryRequest    string
	memoryLimit      string
	concurrency      int
	noConfig         bool
	kubernetes       bool
	strict           bool
	ignoreMissing    bool
	summary          bool
	verbose          bool
	noColor          bool
	exitOnError      bool

	// internal state
	store schema.Store
	// policy is which rules run, at what level, and with what settings, once
	// the flags and the file have both had their say.
	policy rulePolicy
	// kubernetesEnabled is what the flag or the settings file said about
	// running in Kubernetes; nil when neither said anything, which leaves the
	// answer to be read from the memory numbers.
	kubernetesEnabled *bool
	// kubernetesOverrides are the per-path environments, which only the
	// settings file can state.
	kubernetesOverrides []kubernetesOverride
	// envPolicy resolves the environment of each file linted.
	envPolicy lint.EnvironmentPolicy
}

// NewCommand builds the root command. A nil opts is allowed, in which case a
// zero value is used.
func NewCommand(opts *Options) *cobra.Command {
	if opts == nil {
		//nolint:exhaustruct // RegisterFlags fills in every flag, and a nil Fs is the real filesystem
		opts = &Options{}
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

	cmd.AddCommand(newRunCommand(opts), newListCommand(opts), newVersionCommand())

	cmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		cmd.PrintErr(cmd.UsageString())

		return err
	})

	return cmd
}

// RegisterFlags declares every flag the lint run takes. Each one mirrors a key
// of the settings file: the file is what a repository commits, and the flag is
// how a single run departs from it.
func (o *Options) RegisterFlags(cmd *cobra.Command) {
	// The groups below are shared with the list subcommands, which take only
	// the flags that change what they print.
	o.registerSettingsFlags(cmd)
	o.registerSchemaLocationFlag(cmd)
	o.registerDistributionFlag(cmd)
	o.registerRuleFlags(cmd)

	flags := cmd.Flags()

	flags.StringVar(&o.collectorVersion, "collector-version", schema.Latest,
		"collector release to validate against, e.g. v0.157.0")
	flags.StringVar(&o.output, "output", "text", "output format: text, json, junit, tap or github")
	flags.StringSliceVar(&o.exclude, "exclude", nil, "glob patterns to skip when walking directories")
	flags.StringVar(&o.minSeverity, "min-severity", "info", "lowest severity to report: error, warning or info")
	flags.StringVar(&o.failOn, "fail-on", "error", "severity that makes a file invalid: error, warning or info")
	// Spelled -n before this took golangci-lint's name for it, so -n stays the
	// shorthand: it is what the documentation and every existing workflow use.
	flags.IntVarP(&o.concurrency, "concurrency", "n", defaultWorkers(), "number of files to check in parallel")
	flags.BoolVar(&o.kubernetes, "kubernetes", false, "the config runs in a Kubernetes pod")
	flags.StringVar(&o.memoryRequest, "memory-request", "", "container memory request, e.g. 256Mi")
	flags.StringVar(&o.memoryLimit, "memory-limit", "", "container memory limit, e.g. 512Mi")
	flags.BoolVar(&o.strict, "strict", false, "report unknown component settings as errors")
	flags.BoolVar(&o.ignoreMissing, "ignore-missing-schemas", false,
		"do not fail on components missing from the schema")
	flags.BoolVar(&o.summary, "summary", false, "print a summary of the results")
	flags.BoolVar(&o.verbose, "verbose", false, "also report files that passed, and say which settings file was read")
	flags.BoolVar(&o.noColor, "no-color", false, "disable coloured output")
	flags.BoolVar(&o.exitOnError, "exit-on-error", false, "stop at the first file that fails")
}

// Prepare folds the settings file into the parsed flags and builds the schema
// store. Flags given on the command line win over the file.
func (o *Options) Prepare(cmd *cobra.Command) error {
	fileSettings, path, err := o.loadSettings()
	if err != nil {
		return err
	}

	legacy := fileSettings.normalize()

	o.applySettings(fileSettings, cmd.Flags().Changed)

	o.policy = o.rulePolicy(fileSettings)

	o.store = schema.Store{Locations: o.schemaLocations, Distribution: o.distribution, Fs: o.Fs}

	o.envPolicy, err = o.environmentPolicy()
	if err != nil {
		return err
	}

	if o.verbose && path != "" {
		cmd.PrintErrf("otelcol-config-lint: settings read from %s\n", path)
	}

	if len(legacy) > 0 {
		cmd.PrintErrf("otelcol-config-lint: %s: deprecated top-level keys: %s;"+
			" move them under run, rules, issues or output\n", path, strings.Join(legacy, ", "))
	}

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

// fs returns the filesystem to read, which is the real one unless the caller
// named another.
func (o *Options) fs() afero.Fs {
	if o.Fs == nil {
		return afero.NewOsFs()
	}

	return o.Fs
}

// registerSettingsFlags declares --config and --no-config. Every command
// honours them: the settings file states rule and schema policy, not only lint
// options.
func (o *Options) registerSettingsFlags(cmd *cobra.Command) {
	flags := cmd.Flags()

	flags.StringVarP(&o.settingsFile, "config", "c", "",
		"settings file (default: "+DefaultSettingsFile+", searched for here and in each parent)")
	flags.BoolVar(&o.noConfig, "no-config", false, "ignore any settings file and use the flags alone")
}

// registerDistributionFlag declares --distribution, shared with
// "list versions": both answer differently depending on which binary is meant.
func (o *Options) registerDistributionFlag(cmd *cobra.Command) {
	cmd.Flags().StringVar(&o.distribution, "distribution", schema.DefaultDistribution,
		"collector distribution to validate against: core, contrib, k8s or otlp")
}

// registerSchemaLocationFlag declares --schema-location, shared with
// "list versions".
func (o *Options) registerSchemaLocationFlag(cmd *cobra.Command) {
	cmd.Flags().StringSliceVar(&o.schemaLocations, "schema-location", nil,
		"where to find schemas: a directory, a {{.Version}} template, a URL, or \"default\";\n"+
			"repeat to search several in order (default: the published registry)")
}

// registerRuleFlags declares the rule selection, shared with "list rules".
func (o *Options) registerRuleFlags(cmd *cobra.Command) {
	flags := cmd.Flags()

	flags.StringVar(&o.ruleDefault, "default", "",
		"rule set to start from: "+defaultAll+" (the default) or "+defaultNone)
	flags.StringSliceVarP(&o.enable, "enable", "E", nil, "rules to turn on, on top of --default")
	flags.StringSliceVarP(&o.disable, "disable", "D", nil, "rules to turn off")
	flags.StringSliceVar(&o.severity, "severity", nil,
		"rule=level overrides, e.g. missing-batch=warning")
}

// runLint resolves what to check and how to report it, then does the work.
func (o *Options) runLint(cmd *cobra.Command, paths []string) error {
	sc := scanner.New(o.exclude)
	sc.Fs = o.Fs

	files, err := sc.Scan(paths)
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
	for r := range linter.LintAll(onDisk, o.concurrency) {
		results[r.Path] = r
	}

	// An environment decides whether some rules run at all, so a verbose run
	// says which one each file resolved to.
	sayEnvironment := o.verbose && o.envPolicy.Configured()

	for _, f := range sets.List(files) {
		r := results[f]

		summary.Add(r)

		if sayEnvironment {
			cmd.PrintErrf("otelcol-config-lint: %s: %s\n", r.Path, describeEnvironment(o.envPolicy.Resolve(r.Path)))
		}

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
	cat, err := o.loadSchema(cmd)
	if err != nil {
		return nil, err
	}

	severities, err := o.policy.resolve()
	if err != nil {
		return nil, err
	}

	rules, err := o.policy.rules()
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
		Schema:               cat,
		Fs:                   o.Fs,
		Availability:         lint.NewVersionIndex(o.store),
		Distributions:        lint.NewDistributionIndex(o.store, cat.CollectorVersion),
		Rules:                rules,
		Severities:           severities,
		Environment:          o.envPolicy.Resolve,
		Strict:               o.strict,
		IgnoreMissingSchemas: o.ignoreMissing,
		MinSeverity:          minSeverity,
		FailOn:               failOn,
	}), nil
}

// loadSchema resolves the targeted release, falling back to the newest
// schema that is not newer than the request when there is no exact match.
func (o *Options) loadSchema(cmd *cobra.Command) (*schema.Schema, error) {
	cat, err := o.store.Load(o.collectorVersion)
	if err == nil {
		return cat, nil
	}

	var unknown *schema.UnknownVersionError
	if !errors.As(err, &unknown) {
		return nil, fmt.Errorf("load schema: %w", err)
	}

	near, hasNear := unknown.Nearest()
	if !hasNear {
		return nil, fmt.Errorf("load schema: %w", err)
	}

	cmd.PrintErrf("otelcol-config-lint: no schema for %s, falling back to %s\n", unknown.Version, near)

	cat, err = o.store.Load(near)
	if err != nil {
		return nil, fmt.Errorf("load schema %s: %w", near, err)
	}

	return cat, nil
}

// applySettings folds a settings file into the options. changed reports whether
// a flag was given on the command line; those always win over the file. The
// rule lists are not here: they merge rather than replace, which rulePolicy
// does.
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

	str("collector-version", &o.collectorVersion, s.Run.CollectorVersion)
	str("distribution", &o.distribution, s.Run.Distribution)
	str("memory-request", &o.memoryRequest, s.Run.Kubernetes.MemoryRequest)
	str("memory-limit", &o.memoryLimit, s.Run.Kubernetes.MemoryLimit)
	str("output", &o.output, s.Output.Format)
	str("min-severity", &o.minSeverity, s.Issues.MinSeverity)
	str("fail-on", &o.failOn, s.Issues.FailOn)
	boolean("strict", &o.strict, s.Run.Strict)
	boolean("ignore-missing-schemas", &o.ignoreMissing, s.Run.IgnoreMissingSchemas)
	boolean("summary", &o.summary, s.Output.Summary)
	boolean("verbose", &o.verbose, s.Output.Verbose)
	boolean("exit-on-error", &o.exitOnError, s.Issues.ExitOnError)

	// The file says whether to colour, the flag says whether to stop; one is
	// the negation of the other.
	if !changed("no-color") && s.Output.Color != nil {
		o.noColor = !*s.Output.Color
	}

	if !changed("concurrency") && s.Run.Concurrency != nil {
		o.concurrency = *s.Run.Concurrency
	}

	// The deployment environment is a tri-state: the flag wins, then the file,
	// and with neither the memory numbers speak for themselves.
	switch {
	case changed("kubernetes"):
		o.kubernetesEnabled = &o.kubernetes
	case s.Run.Kubernetes.Enabled != nil:
		o.kubernetesEnabled = s.Run.Kubernetes.Enabled
	}

	o.kubernetesOverrides = s.Run.Kubernetes.Overrides

	if !changed("schema-location") && len(s.Run.SchemaLocations) > 0 {
		o.schemaLocations = append(o.schemaLocations, s.Run.SchemaLocations...)
	}

	if !changed("exclude") && len(s.Run.Exclude) > 0 {
		o.exclude = append(o.exclude, s.Run.Exclude...)
	}
}

func defaultWorkers() int {
	if n := runtime.NumCPU(); n < maxDefaultWorkers {
		return n
	}

	return maxDefaultWorkers
}
