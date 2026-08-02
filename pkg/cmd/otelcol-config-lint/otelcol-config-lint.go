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
	"path/filepath"
	"runtime"
	"strings"
	"text/tabwriter"

	"github.com/samber/mo"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/catalog"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/lint"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
	"github.com/minuk-dev/otelcol-config-lint/pkg/sets"
)

// Version is the linter's own version.
//
//nolint:gochecknoglobals // injected at build time with -ldflags
var Version = "dev"

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
	listRules        bool
	listVersions     bool
	showVersion      bool

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
		Use:   "otelcol-config-lint [flags] <file|dir|->...",
		Short: "Validate OpenTelemetry Collector config files against a specific collector release",
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
	flags := cmd.Flags()

	flags.StringVar(&o.collectorVersion, "collector-version", catalog.Latest,
		"collector release to validate against, e.g. v0.157.0")
	flags.StringSliceVar(&o.catalogLocations, "catalog-location", nil,
		"where to find catalogs: a directory, a {{.Version}} template, a URL, or \"default\";\n"+
			"repeat to search several in order (default: the built-in catalogs)")
	flags.StringVar(&o.output, "output", "text", "output format: text, json, junit, tap or github")
	flags.StringVar(&o.settingsFile, "config", "", "settings file (default "+DefaultSettingsFile+" if present)")
	flags.StringVar(&o.disable, "disable", "", "comma-separated rules to turn off")
	flags.StringVar(&o.severity, "severity", "",
		"comma-separated rule=level overrides, e.g. missing-batch=warning")
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
	flags.BoolVar(&o.listRules, "list-rules", false, "print the rules and their default severities, then exit")
	flags.BoolVar(&o.listVersions, "list-versions", false, "print the available catalog versions, then exit")
	flags.BoolVar(&o.showVersion, "version", false, "print the linter version, then exit")
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

// Run dispatches to whichever mode the flags asked for.
func (o *Options) Run(cmd *cobra.Command, args []string) error {
	switch {
	case o.showVersion:
		cmd.Printf("otelcol-config-lint %s\n", Version)

		return nil
	case o.listVersions:
		err := o.runListVersions(cmd)
		if err != nil {
			return err
		}

		return nil
	case o.listRules:
		err := o.runListRules(cmd)
		if err != nil {
			return err
		}

		return nil
	}

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

// runLint resolves what to check and how to report it, then does the work.
func (o *Options) runLint(cmd *cobra.Command, paths []string) error {
	files, err := collect(paths, splitList(o.exclude))
	if err != nil {
		return err
	}

	if len(files) == 0 {
		return fmt.Errorf("%w in %s", ErrNoYAMLFiles, strings.Join(paths, ", "))
	}

	linter, err := o.newLinter(cmd)
	if err != nil {
		return err
	}

	formatter, err := lint.NewFormatter(o.output, cmd.OutOrStdout(), lint.FormatterOptions{
		Verbose: o.verbose,
		Summary: o.summary,
		Color:   !o.noColor && o.output == "text" && isTerminal(cmd.OutOrStdout()),
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

// lintAll checks every file and reports the results in argument order,
// returning ErrFilesInvalid when the gate was not met.
func (o *Options) lintAll(
	cmd *cobra.Command,
	linter *lint.Linter,
	formatter lint.Formatter,
	files []string,
) error {
	var summary lint.Summary

	// Results are buffered so output stays in file order even though the files
	// are checked concurrently.
	results := make(map[string]lint.Result, len(files))

	var toLint []string

	for _, f := range files {
		if f == "-" {
			results[f] = linter.LintReader("stdin", cmd.InOrStdin())

			continue
		}

		toLint = append(toLint, f)
	}

	for r := range linter.LintAll(toLint, o.workers) {
		results[r.Path] = r
	}

	for _, f := range files {
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

func (o *Options) runListRules(cmd *cobra.Command) error {
	overrides, err := o.severityOverrides()
	if err != nil {
		return err
	}

	w := newColumns(cmd.OutOrStdout())

	for _, r := range rule.All() {
		sev := r.Severity()

		note := ""
		if s, ok := overrides[r.Name()]; ok && s != sev {
			sev, note = s, " (overridden)"
		}

		w.row(r.Name(), string(sev)+note, r.Description())
	}

	w.flush()

	return nil
}

func (o *Options) runListVersions(cmd *cobra.Command) error {
	versions := o.store.Versions()
	if len(versions) == 0 {
		return ErrNoCatalogs
	}

	w := newColumns(cmd.OutOrStdout())

	for i, v := range versions {
		cat, err := o.store.Load(v)
		if err != nil {
			w.row(v, "unreadable: "+err.Error(), "")

			continue
		}

		note := ""
		if i == 0 {
			note = "(latest)"
		}

		w.row(v, fmt.Sprintf("%d components", cat.Count()),
			strings.Join(cat.Distributions, "+")+" "+note)
	}

	w.flush()

	return nil
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

// isConfigExt reports whether a file extension is one a directory walk picks up.
func isConfigExt(ext string) bool {
	return ext == ".yaml" || ext == ".yml"
}

// collect expands the command line arguments into a list of files to lint.
// Directories are walked recursively; "-" is kept as a marker for stdin.
func collect(args []string, exclude []string) ([]string, error) {
	var out []string

	// The same file can be named twice, or named and also walked into. Keep
	// the first mention, so the report follows the order of the arguments.
	seen := sets.New[string]()
	add := func(p string) {
		if seen.InsertNew(p) {
			out = append(out, p)
		}
	}

	for _, arg := range args {
		if arg == "-" {
			add("-")

			continue
		}

		info, err := os.Stat(arg)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", arg, err)
		}

		if !info.IsDir() {
			// An explicitly named file is linted even if it would be excluded
			// by a directory walk, matching how other linters behave.
			add(filepath.Clean(arg))

			continue
		}

		err = filepath.WalkDir(arg, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			name := d.Name()
			if d.IsDir() {
				if path != arg && (strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules") {
					return fs.SkipDir
				}

				return nil
			}

			if !isConfigExt(strings.ToLower(filepath.Ext(name))) {
				return nil
			}

			if excluded(path, exclude) {
				return nil
			}

			add(filepath.Clean(path))

			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk %s: %w", arg, err)
		}
	}

	return out, nil
}

// excluded reports whether a path matches any exclude pattern. Patterns are
// matched against both the full path and the base name.
func excluded(path string, patterns []string) bool {
	base := filepath.Base(path)
	for _, p := range patterns {
		if ok, _ := filepath.Match(p, base); ok {
			return true
		}

		if ok, _ := filepath.Match(p, path); ok {
			return true
		}

		if strings.Contains(path, strings.Trim(p, "*")) && strings.Contains(p, "*") {
			return true
		}
	}

	return false
}

// columns renders aligned help output for --list-rules and --list-versions.
type columns struct{ w *tabwriter.Writer }

// columnPadding is the gap between columns, in spaces.
const columnPadding = 2

func newColumns(w io.Writer) columns {
	return columns{w: tabwriter.NewWriter(w, 0, 0, columnPadding, ' ', 0)}
}

func (c columns) row(cells ...string) {
	for i, cell := range cells {
		if i > 0 {
			_, _ = io.WriteString(c.w, "\t")
		}

		_, _ = io.WriteString(c.w, cell)
	}

	_, _ = io.WriteString(c.w, "\n")
}

func (c columns) flush() { _ = c.w.Flush() }

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

// isTerminal reports whether w is a character device, so colour is only used
// when a human is watching.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}

	info, err := f.Stat()

	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
