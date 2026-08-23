// Package run is the command that lints: every lint flag lives here, and it is
// the only command whose exit code carries findings.
package run

import (
	"errors"
	"fmt"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/minuk-dev/otelcol-config-lint/pkg/cmdutil"
	"github.com/minuk-dev/otelcol-config-lint/pkg/cmdutil/rulepolicy"
	"github.com/minuk-dev/otelcol-config-lint/pkg/cmdutil/settings"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/lint"
	"github.com/minuk-dev/otelcol-config-lint/pkg/scanner"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
	"github.com/minuk-dev/otelcol-config-lint/pkg/sets"
	"github.com/minuk-dev/otelcol-config-lint/pkg/termutil"
)

// Errors reported for input this command cannot lint.
var (
	// ErrNoInput reports that no file, directory or "-" was given.
	ErrNoInput = cmdutil.NewUsageError("no files or directories specified")
	// ErrNoYAMLFiles reports that the given paths held nothing to lint.
	ErrNoYAMLFiles = cmdutil.NewUsageError("no YAML files found")
	// ErrNoOverridePaths reports an environment override that matches nothing.
	ErrNoOverridePaths = cmdutil.NewUsageError("an override needs at least one path pattern")
)

// maxDefaultWorkers caps the default parallelism; beyond this the run is bound
// by reading files, not by checking them.
const maxDefaultWorkers = 8

// options is everything "run" was asked to do: the lint flags, the groups of
// them it shares with the listings, and the state resolved once the flags and
// the settings file have both had their say.
//
// No other command can see these, because no other command imports them: a
// flag that only shapes a lint run lives in this package, which is why
// "list rules --strict" is a usage error rather than a flag that quietly does
// nothing.
type options struct {
	*cmdutil.GlobalOptions

	// flags
	// Where the schemas come from, and which binary they describe.
	distribution    string
	schemaLocations []string
	// Which rules run, and at what level.
	ruleDefault string
	enable      []string
	disable     []string
	severity    []string
	// What the config runs in, for the rules that cannot judge one without.
	kubernetes    bool
	memoryRequest string
	memoryLimit   string

	collectorVersion string
	output           string
	exclude          []string
	minSeverity      string
	failOn           string
	concurrency      int
	strict           bool
	ignoreMissing    bool
	summary          bool
	verbose          bool
	noColor          bool
	exitOnError      bool

	// internal state
	// store is where the schemas are read from.
	store schema.Store
	// policy is which rules run, at what level, and with what settings, once
	// the flags and the file have both had their say.
	policy rulepolicy.Policy
	// envPolicy resolves the environment of each file linted.
	envPolicy lint.EnvironmentPolicy
	// kubernetesEnabled is what the flag or the settings file said about
	// running in Kubernetes; nil when neither said anything, which leaves the
	// answer to be read from the memory numbers.
	kubernetesEnabled *bool
	// kubernetesOverrides are the per-path environments, which only the
	// settings file can state.
	kubernetesOverrides []settings.KubernetesOverride
}

// NewCommand builds "run". Its options are filled in by declareFlags and then
// by the settings file, in that order.
func NewCommand(global *cmdutil.GlobalOptions) *cobra.Command {
	//nolint:exhaustruct // the flags fill themselves in, and the state is resolved by prepare
	opts := &options{GlobalOptions: global}

	cmd := &cobra.Command{
		Use:   "run [flags] <file|dir|->...",
		Short: "Lint the given config files",
		Example: `  otelcol-config-lint run config.yaml
  otelcol-config-lint run --collector-version v0.157.0 --summary ./configs
  cat config.yaml | otelcol-config-lint run -
  otelcol-config-lint run --output json --strict ./configs > report.json
  otelcol-config-lint run --default none -E invalid-value ./configs
  otelcol-config-lint run -c ci.yaml ./configs

The policy itself belongs in ` + settings.DefaultName + `, which is read from
this directory or any parent above it; every flag here mirrors one of its keys.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			err := opts.prepare(cmd)
			if err != nil {
				return err
			}

			err = opts.run(cmd, args)
			if err != nil {
				return err
			}

			return nil
		},
	}

	// Only this command can end in anything but 0 or 2, so the footer belongs
	// here rather than on the root where every subcommand would inherit it.
	cmd.SetUsageTemplate(cmd.UsageTemplate() + fmt.Sprintf(`
Exit codes:
  %d  every file passed
  %d  at least one file failed
  %d  the command could not run
`, cmdutil.ExitOK, cmdutil.ExitInvalid, cmdutil.ExitUsage))

	opts.declareFlags(cmd)

	return cmd
}

// declareFlags declares every flag the lint run takes. Each one mirrors a key
// of the settings file: the file is what a repository commits, and the flag is
// how a single run departs from it.
func (o *options) declareFlags(cmd *cobra.Command) {
	flags := cmd.Flags()

	o.RegisterFlags(cmd)
	flags.StringVar(&o.distribution, "distribution", schema.DefaultDistribution,
		"collector distribution to validate against: core, contrib, k8s or otlp")
	flags.StringSliceVar(&o.schemaLocations, "schema-location", nil,
		"where to find schemas: a directory, a {{.Version}} template, a URL, or \"default\";\n"+
			"repeat to search several in order (default: the published registry)")
	flags.StringVar(&o.ruleDefault, "default", "",
		"rule set to start from: "+settings.DefaultAll+" (the default) or "+settings.DefaultNone)
	flags.StringSliceVarP(&o.enable, "enable", "E", nil, "rules to turn on, on top of --default")
	flags.StringSliceVarP(&o.disable, "disable", "D", nil, "rules to turn off")
	flags.StringSliceVar(&o.severity, "severity", nil,
		"rule=level overrides, e.g. missing-batch=warning")
	flags.BoolVar(&o.kubernetes, kubernetesFlag, false, "the config runs in a Kubernetes pod")
	flags.StringVar(&o.memoryRequest, "memory-request", "", "container memory request, e.g. 256Mi")
	flags.StringVar(&o.memoryLimit, "memory-limit", "", "container memory limit, e.g. 512Mi")
	flags.StringVar(&o.collectorVersion, "collector-version", schema.Latest,
		"collector release to validate against, e.g. v0.157.0")
	flags.StringVar(&o.output, "output", "text", "output format: text, json, junit, tap or github")
	flags.StringSliceVar(&o.exclude, "exclude", nil, "glob patterns to skip when walking directories")
	flags.StringVar(&o.minSeverity, "min-severity", "info", "lowest severity to report: error, warning or info")
	flags.StringVar(&o.failOn, "fail-on", "error", "severity that makes a file invalid: error, warning or info")
	// Spelled -n before this took golangci-lint's name for it, so -n stays the
	// shorthand: it is what the documentation and every existing workflow use.
	flags.IntVarP(&o.concurrency, "concurrency", "n", defaultWorkers(), "number of files to check in parallel")
	flags.BoolVar(&o.strict, "strict", false, "report unknown component settings as errors")
	flags.BoolVar(&o.ignoreMissing, "ignore-missing-schemas", false,
		"do not fail on components missing from the schema")
	flags.BoolVar(&o.summary, "summary", false, "print a summary of the results")
	flags.BoolVar(&o.verbose, "verbose", false, "also report files that passed, and say which settings file was read")
	flags.BoolVar(&o.noColor, "no-color", false, "disable coloured output")
	flags.BoolVar(&o.exitOnError, "exit-on-error", false, "stop at the first file that fails")
}

// prepare folds the settings file into the parsed flags and resolves what the
// run will use: the schemas, the rules and the environment. Flags given on the
// command line win over the file.
func (o *options) prepare(cmd *cobra.Command) error {
	err := o.Prepare(cmd)
	if err != nil {
		return err
	}

	file, fold := o.Settings(), o.Fold(cmd)

	o.applySettings(file, fold)
	fold.Str("distribution", &o.distribution, file.Run.Distribution)
	fold.List("schema-location", &o.schemaLocations, file.Run.SchemaLocations)
	fold.Str("memory-request", &o.memoryRequest, file.Run.Kubernetes.MemoryRequest)
	fold.Str("memory-limit", &o.memoryLimit, file.Run.Kubernetes.MemoryLimit)

	// The deployment environment is a tri-state: the flag wins, then the file,
	// and with neither the memory numbers speak for themselves.
	switch {
	case fold.Changed(kubernetesFlag):
		o.kubernetesEnabled = &o.kubernetes
	case file.Run.Kubernetes.Enabled != nil:
		o.kubernetesEnabled = file.Run.Kubernetes.Enabled
	}

	o.kubernetesOverrides = file.Run.Kubernetes.Overrides

	o.policy = rulepolicy.New(file, rulepolicy.Selection{
		Default:  o.ruleDefault,
		Enable:   o.enable,
		Disable:  o.disable,
		Severity: o.severity,
	})
	o.store = schema.Store{Locations: o.schemaLocations, Distribution: o.distribution, Fs: o.FS()}

	o.envPolicy, err = o.environmentPolicy()
	if err != nil {
		return err
	}

	if o.verbose && o.SettingsPath() != "" {
		cmd.PrintErrf("otelcol-config-lint: settings read from %s\n", o.SettingsPath())
	}

	return nil
}

// run lints the given paths.
func (o *options) run(cmd *cobra.Command, args []string) error {
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

// applySettings folds the keys only a lint run has a flag for. The schema and
// environment groups fold their own, and the rule lists merge rather than
// replace, which rulepolicy does.
func (o *options) applySettings(s *settings.File, fold settings.Fold) {
	fold.Str("collector-version", &o.collectorVersion, s.Run.CollectorVersion)
	fold.Str("output", &o.output, s.Output.Format)
	fold.Str("min-severity", &o.minSeverity, s.Issues.MinSeverity)
	fold.Str("fail-on", &o.failOn, s.Issues.FailOn)
	fold.Bool("strict", &o.strict, s.Run.Strict)
	fold.Bool("ignore-missing-schemas", &o.ignoreMissing, s.Run.IgnoreMissingSchemas)
	fold.Bool("summary", &o.summary, s.Output.Summary)
	fold.Bool("verbose", &o.verbose, s.Output.Verbose)
	fold.Bool("exit-on-error", &o.exitOnError, s.Issues.ExitOnError)
	fold.List("exclude", &o.exclude, s.Run.Exclude)

	// The file says whether to colour, the flag says whether to stop; one is
	// the negation of the other.
	if !fold.Changed("no-color") && s.Output.Color != nil {
		o.noColor = !*s.Output.Color
	}

	if !fold.Changed("concurrency") && s.Run.Concurrency != nil {
		o.concurrency = *s.Run.Concurrency
	}
}

// runLint resolves what to check and how to report it, then does the work.
func (o *options) runLint(cmd *cobra.Command, paths []string) error {
	sc := scanner.New(o.FS(), o.exclude)

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
// cmdutil.ErrFilesInvalid when the gate was not met.
func (o *options) lintAll(
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
		return cmdutil.ErrFilesInvalid
	}

	return nil
}

func (o *options) newLinter(cmd *cobra.Command) (*lint.Linter, error) {
	cat, err := o.loadSchema(cmd)
	if err != nil {
		return nil, err
	}

	severities, err := o.policy.Severities()
	if err != nil {
		return nil, err
	}

	rules, err := o.policy.Rules()
	if err != nil {
		return nil, err
	}

	minSeverity, err := diag.ParseSeverity(o.minSeverity)
	if err != nil {
		return nil, cmdutil.AsUsageError(fmt.Errorf("--min-severity: %w", err))
	}

	failOn, err := diag.ParseSeverity(o.failOn)
	if err != nil {
		return nil, cmdutil.AsUsageError(fmt.Errorf("--fail-on: %w", err))
	}

	return lint.New(lint.Options{
		Schema:               cat,
		Fs:                   o.FS(),
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
func (o *options) loadSchema(cmd *cobra.Command) (*schema.Schema, error) {
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

func defaultWorkers() int {
	if n := runtime.NumCPU(); n < maxDefaultWorkers {
		return n
	}

	return maxDefaultWorkers
}
