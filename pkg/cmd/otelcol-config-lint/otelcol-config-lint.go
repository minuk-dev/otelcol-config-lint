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

	"github.com/minuk-dev/otelcol-config-lint/pkg/catalog"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/lint"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
	"github.com/samber/lo"
	"github.com/samber/mo"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var ErrUnknownRule = errors.New("unknown rule")

// Version is the linter's own version.
//
//nolint:gochecknoglobals // injected at build time with -ldflags
var Version = "dev"

// maxDefaultWorkers caps the default parallelism; beyond this the run is bound
// by reading files, not by checking them.
const maxDefaultWorkers = 8

type Options struct {
	// flags
	collectorVersion string
	catalogLocations multiFlag
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
	store *catalog.Store
}

func NewCommand(opts *Options) *cobra.Command {
	if opts == nil {
		opts = &Options{}
	}
	cmd := &cobra.Command{
		Use:   "otelcol-config-lint [flags] [file-or-directory ...]",
		Short: "Validate OpenTelemetry Collector config files against a specific collector release",
		Example: `otelcol-config-lint config.yaml
  otelcol-config-lint -collector-version v0.157.0 -summary ./configs
  cat config.yaml | otelcol-config-lint -
  otelcol-config-lint -output json -strict ./configs > report.json
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			err := opts.Prepare(cmd, args)
			if err != nil {
				return fmt.Errorf("failed to prepare: %w", err)
			}

			err = opts.Run(cmd, args)
			if err != nil {
				return fmt.Errorf("failed to run: %w", err)
			}

			return nil
		},
	}

	opts.RegisterFlags(cmd)

	return cmd
}

func (o *Options) RegisterFlags(cmd *cobra.Command) {
	flags := cmd.Flags()

	flags.StringVar(&o.collectorVersion, "collector-version", catalog.Latest,
		"collector release to validate against, e.g. v0.157.0")
	flags.Var(&o.catalogLocations, "catalog-location",
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
	flags.IntVar(&o.workers, "n", defaultWorkers(), "number of files to check in parallel")
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

func (opts *Options) Prepare(cmd *cobra.Command, args []string) error {
	fileSettings, err := loadSettings(opts.settingsFile)
	if err != nil {
		fmt.Errorf("failed to load settings: %w", err)
	}

	opts.applySettings(fileSettings)

	opts.store = &catalog.Store{
		Locations: opts.catalogLocations,
	}

	return nil
}

func (o *Options) Run(cmd *cobra.Command, args []string) error {
	switch {
	case o.showVersion:
		cmd.Printf("otelcol-config-lint %s\n", Version)

		return nil
	case o.listVersions:
		err := o.showlistVersions(cmd)
		if err != nil {
			return fmt.Errorf("failed to list versions: %w", err)
		}
		return nil
	case o.listRules:
		err := o.showlistRules(cmd)
		if err != nil {
			return fmt.Errorf("failed to list rules: %w", err)
		}
		return nil
	}

	paths := args
	if len(paths) == 0 {
		return errors.New("no files or directories specified")
	}

	err := o.runLint(cmd, paths)
	if err != nil {
		return fmt.Errorf("failed to lint: %w", err)
	}

	return nil
}

func (o *Options) showlistRules(cmd *cobra.Command) error {
	overrides, err := o.severityOverrides()
	if err != nil {
		return fmt.Errorf("invalid severity overrides: %w", err)
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

func (o *Options) runLint(cmd *cobra.Command, paths []string) error {
	files, err := collect(paths, splitList(o.exclude))
	if err != nil {
		return fmt.Errorf("failed to collect files: %w", err)
	}

	if len(files) == 0 {
		return fmt.Errorf("no YAML files found in %s", strings.Join(paths, ", "))
	}

	linter, err := o.newLinter(cmd)
	if err != nil {
		return fmt.Errorf("failed to create linter: %w", err)
	}

	formatter, err := lint.NewFormatter(o.output, cmd.OutOrStdout(), lint.FormatterOptions{
		Verbose: o.verbose,
		Summary: o.summary,
		Color:   !o.noColor && o.output == "text" && isTerminal(cmd.OutOrstdout()),
	})
	if err != nil {
		return fmt.Errorf("failed to create formatter: %w", err)
	}
	return lintAll(linter, formatter, files, o, stdin, stderr)
}

func (o *Options) newLinter(cmd *cobra.Command) (*lint.Linter, error) {
	cat, err := o.loadCatalog(cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to load catalog: %w", err)
	}

	severities, err := o.severityOverrides()
	if err != nil {
		return nil, fmt.Errorf("invalid severity overrides: %w", err)
	}

	minSeverity, err := diag.ParseSeverity(o.minSeverity)
	if err != nil {
		return nil, fmt.Errorf("invalid -min-severity: %w", err)
	}

	failOn, err := diag.ParseSeverity(o.failOn)
	if err != nil {
		return nil, fmt.Errorf("invalid -fail-on: %w", err)
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

func splitList(s string) []string {
	var out []string

	for part := range strings.SplitSeq(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}

	return out
}

func collect(paths []string, exclude []string) ([]string, error) {
	var out []string

	seen := map[string]bool{}
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}

	errs := lo.Map(paths, func(path string, _ int) error {
		if path == "-" {
			add("-")
			return nil
		}
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}

		if !info.IsDir() {
			// An explicitly named file is linted even if it would be excluded
			// by a directory walk, matching how other linters behave.
			add(filepath.Clean(path))
			return nil
		}
		err = filepath.WalkDir(path, func(subPath string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			name := d.Name()
			if d.IsDir() {
				if subPath != path && (strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules") {
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
			return fmt.Errorf("walk %s: %w", path, err)
		}

		return nil
	})

	err := errors.Join(errs...)
	if err != nil {
		return nil, fmt.Errorf("failed to collect files: %w", err)
	}

	return out, nil
}

// isConfigExt reports whether a file extension is one a directory walk picks up.
func isConfigExt(ext string) bool {
	return ext == ".yaml" || ext == ".yml"
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

func (o *Options) showlistVersions(cmd *cobra.Command) error {
	versions := o.store.Versions()
	if len(versions) == 0 {
		cmd.PrintErrf("no catalog versions found in %v", o.store.Locations)
		return errors.New("no catalog versions found")
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

// DefaultSettingsFile is looked for in the working directory when no
// -config flag is given.
const DefaultSettingsFile = ".otelcol-config-lint.yaml"

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

func (o *Options) applySettings(s *settings) {
	o.collectorVersion = mo.Some(s.CollectorVersion).OrElse(o.collectorVersion)
	o.catalogLocations = append(o.catalogLocations, s.CatalogLocations...)
	o.output = mo.Some(s.Output).OrElse(o.output)
	o.minSeverity = mo.Some(s.MinSeverity).OrElse(o.minSeverity)
	o.failOn = mo.Some(s.FailOn).OrElse(o.failOn)
	o.strict = mo.PointerToOption(s.Strict).OrElse(o.strict)
	o.ignoreMissing = mo.PointerToOption(s.IgnoreMissingSchemas).OrElse(o.ignoreMissing)
	o.summary = mo.PointerToOption(s.Summary).OrElse(o.summary)
	o.exclude = mo.Some(strings.Join(s.Exclude, ",")).OrElse(o.exclude)
	o.disable = mo.Some(strings.Join(s.Disable, ",")).OrElse(o.disable)
	o.severity = mo.Some(
		strings.Join(
			lo.MapEntries(s.Severity, func(k, v string) string {
				return fmt.Sprintf("%s=%s", k, v)
			}).Slices().Sort(),
			",",
		)).OrElse(o.severity)
}

func defaultWorkers() int {
	if n := runtime.NumCPU(); n < maxDefaultWorkers {
		return n
	}

	return maxDefaultWorkers
}

// columns renders aligned help output for -list-rules and -list-versions.
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

// severityOverrides builds the rule severity map from -disable and -severity.
func (o *Options) severityOverrides() (map[string]diag.Severity, error) {
	out := map[string]diag.Severity{}

	for _, name := range splitList(o.disable) {
		if _, ok := rule.Lookup(name); !ok {
			return nil, fmt.Errorf("-disable: %w %q", ErrUnknownRule, name)
		}

		out[name] = diag.Off
	}

	for _, pair := range splitList(o.severity) {
		name, level, found := strings.Cut(pair, "=")
		if !found {
			return nil, fmt.Errorf("-severity: %q is %w", pair, ErrBadSeverityPair)
		}

		if _, ok := rule.Lookup(name); !ok {
			return nil, fmt.Errorf("-severity: %w %q", ErrUnknownRule, name)
		}

		sev, err := diag.ParseSeverity(level)
		if err != nil {
			return nil, fmt.Errorf("-severity %s: %w", name, err)
		}

		out[name] = sev
	}

	return out, nil
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

func (o *Options) lintAll(
	cmd *cobra.Command,
	linter *lint.Linter,
	formatter lint.Formatter,
	files []string,
) (int, error) {
	var summary lint.Summary

	report := func(r lint.Result) error {
		summary.Add(r)

		return formatter.Result(r)
	}

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

		err := report(r)
		if err != nil {
			return 0, fmt.Errorf("failed to report result for %s: %w", r.Path, err)
		}

		if o.exitOnError && (r.Status == lint.Invalid || r.Status == lint.Error) {
			break
		}
	}

	err := formatter.Finish(summary)
	if err != nil {
		return 0, fmt.Errorf("failed to finish formatter: %w", err)
	}

	if summary.Failed() {
		// TODO: return error to send exitCode
		return 0, nil
	}

	return 0, nil
}
