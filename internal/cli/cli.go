// Package cli implements the otelcol-config-lint command.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"sort"
	"strings"

	"github.com/minuk-dev/otelcol-config-lint/pkg/catalog"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/lint"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// Version is the linter's own version.
//
//nolint:gochecknoglobals // injected at build time with -ldflags
var Version = "dev"

// registerFlags declares every command line flag on a flag set.
func registerFlags(flags *flag.FlagSet, o *options) {
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

// sayf writes a message for the person running the command. An output failure
// here has nowhere left to be reported, so it is deliberately ignored.
func sayf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

// fail writes a command-level error, prefixed like every other tool message.
func fail(w io.Writer, err error) {
	sayf(w, "otelcol-config-lint: %v\n", err)
}

// Errors reported for bad flag values.
var (
	// ErrUnknownRule names a rule that does not exist.
	ErrUnknownRule = errors.New("unknown rule")
	// ErrBadSeverityPair reports a -severity argument that is not rule=level.
	ErrBadSeverityPair = errors.New("not in rule=level form")
)

// Exit codes, following the convention linters are expected to use in CI.
const (
	exitOK      = 0
	exitInvalid = 1
	exitUsage   = 2
)

type options struct {
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
}

// Run executes the command and returns the process exit code.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	var o options

	flags := flag.NewFlagSet("otelcol-config-lint", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { usage(flags, stderr) }

	registerFlags(flags, &o)

	err := flags.Parse(args)
	if err != nil {
		return exitUsage
	}

	set := map[string]bool{}

	flags.Visit(func(f *flag.Flag) { set[f.Name] = true })

	fileSettings, err := loadSettings(o.settingsFile)
	if err != nil {
		fail(stderr, err)

		return exitUsage
	}

	applySettings(&o, fileSettings, set)

	store := catalog.Store{Locations: o.catalogLocations}

	if code, handled := runQuery(&o, store, stdout, stderr); handled {
		return code
	}

	paths := flags.Args()
	if len(paths) == 0 {
		usage(flags, stderr)

		return exitUsage
	}

	return runLint(&o, store, paths, stdin, stdout, stderr)
}

// runLint resolves what to check and how to report it, then does the work.
func runLint(o *options, store catalog.Store, paths []string, stdin io.Reader, stdout, stderr io.Writer) int {
	files, err := collect(paths, splitList(o.exclude))
	if err != nil {
		fail(stderr, err)

		return exitUsage
	}

	if len(files) == 0 {
		sayf(stderr, "otelcol-config-lint: no YAML files found in %s\n", strings.Join(paths, ", "))

		return exitUsage
	}

	linter, err := newLinter(o, store, stderr)
	if err != nil {
		fail(stderr, err)

		return exitUsage
	}

	formatter, err := lint.NewFormatter(o.output, stdout, lint.FormatterOptions{
		Verbose: o.verbose,
		Summary: o.summary,
		Color:   !o.noColor && o.output == "text" && isTerminal(stdout),
	})
	if err != nil {
		fail(stderr, err)

		return exitUsage
	}

	return lintAll(linter, formatter, files, o, stdin, stderr)
}

// runQuery handles the flags that print something and exit, reporting whether
// one of them was given.
func runQuery(o *options, store catalog.Store, stdout, stderr io.Writer) (int, bool) {
	switch {
	case o.showVersion:
		sayf(stdout, "otelcol-config-lint %s\n", Version)

		return exitOK, true
	case o.listVersions:
		return listVersions(store, stdout, stderr), true
	case o.listRules:
		return listRules(o, stdout, stderr), true
	default:
		return 0, false
	}
}

func lintAll(
	linter *lint.Linter, formatter lint.Formatter, files []string,
	o *options, stdin io.Reader, stderr io.Writer,
) int {
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
			results[f] = linter.LintReader("stdin", stdin)

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
			fail(stderr, err)

			return exitUsage
		}

		if o.exitOnError && (r.Status == lint.Invalid || r.Status == lint.Error) {
			break
		}
	}

	err := formatter.Finish(summary)
	if err != nil {
		fail(stderr, err)

		return exitUsage
	}

	if summary.Failed() {
		return exitInvalid
	}

	return exitOK
}

func newLinter(o *options, store catalog.Store, stderr io.Writer) (*lint.Linter, error) {
	cat, err := loadCatalog(store, o.collectorVersion, stderr)
	if err != nil {
		return nil, err
	}

	severities, err := severityOverrides(o)
	if err != nil {
		return nil, err
	}

	minSeverity, err := diag.ParseSeverity(o.minSeverity)
	if err != nil {
		return nil, fmt.Errorf("-min-severity: %w", err)
	}

	failOn, err := diag.ParseSeverity(o.failOn)
	if err != nil {
		return nil, fmt.Errorf("-fail-on: %w", err)
	}

	return lint.New(lint.Options{
		Catalog:              cat,
		Availability:         lint.NewVersionIndex(store),
		Severities:           severities,
		Strict:               o.strict,
		IgnoreMissingSchemas: o.ignoreMissing,
		MinSeverity:          minSeverity,
		FailOn:               failOn,
	}), nil
}

// loadCatalog resolves the targeted release, falling back to the newest
// catalog that is not newer than the request when there is no exact match.
func loadCatalog(store catalog.Store, version string, stderr io.Writer) (*catalog.Catalog, error) {
	cat, err := store.Load(version)
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

	sayf(stderr, "otelcol-config-lint: no catalog for %s, falling back to %s\n", unknown.Version, near)

	cat, err = store.Load(near)
	if err != nil {
		return nil, fmt.Errorf("load catalog %s: %w", near, err)
	}

	return cat, nil
}

// severityOverrides builds the rule severity map from -disable and -severity.
func severityOverrides(o *options) (map[string]diag.Severity, error) {
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

func applySettings(o *options, s *settings, set map[string]bool) {
	str := func(name string, dst *string, v string) {
		if !set[name] && v != "" {
			*dst = v
		}
	}
	boolean := func(name string, dst *bool, v *bool) {
		if !set[name] && v != nil {
			*dst = *v
		}
	}

	str("collector-version", &o.collectorVersion, s.CollectorVersion)

	if !set["catalog-location"] && len(s.CatalogLocations) > 0 {
		o.catalogLocations = append(o.catalogLocations, s.CatalogLocations...)
	}

	str("output", &o.output, s.Output)
	str("min-severity", &o.minSeverity, s.MinSeverity)
	str("fail-on", &o.failOn, s.FailOn)
	boolean("strict", &o.strict, s.Strict)
	boolean("ignore-missing-schemas", &o.ignoreMissing, s.IgnoreMissingSchemas)
	boolean("summary", &o.summary, s.Summary)

	if !set["exclude"] && len(s.Exclude) > 0 {
		o.exclude = joinList(o.exclude, strings.Join(s.Exclude, ","))
	}

	if len(s.Disable) > 0 {
		o.disable = joinList(o.disable, strings.Join(s.Disable, ","))
	}

	if len(s.Severity) > 0 {
		pairs := make([]string, 0, len(s.Severity))
		for _, name := range sortedKeys(s.Severity) {
			pairs = append(pairs, name+"="+s.Severity[name])
		}
		// Flags win, so file overrides are listed first.
		o.severity = joinList(strings.Join(pairs, ","), o.severity)
	}
}

func listRules(o *options, stdout, stderr io.Writer) int {
	overrides, err := severityOverrides(o)
	if err != nil {
		fail(stderr, err)

		return exitUsage
	}

	w := newColumns(stdout)

	for _, r := range rule.All() {
		sev := r.Severity()

		note := ""
		if s, ok := overrides[r.Name()]; ok && s != sev {
			sev, note = s, " (overridden)"
		}

		w.row(r.Name(), string(sev)+note, r.Description())
	}

	w.flush()

	return exitOK
}

func listVersions(store catalog.Store, stdout, stderr io.Writer) int {
	versions := store.Versions()
	if len(versions) == 0 {
		sayf(stderr, "otelcol-config-lint: no catalogs available\n")

		return exitUsage
	}

	w := newColumns(stdout)

	for i, v := range versions {
		cat, err := store.Load(v)
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

	return exitOK
}

func usage(flags *flag.FlagSet, w io.Writer) {
	sayf(w, `otelcol-config-lint validates OpenTelemetry Collector config files.

Usage:
  otelcol-config-lint [flags] <file|dir|->...

Examples:
  otelcol-config-lint config.yaml
  otelcol-config-lint -collector-version v0.157.0 -summary ./configs
  cat config.yaml | otelcol-config-lint -
  otelcol-config-lint -output json -strict ./configs > report.json

Flags:
`)
	flags.PrintDefaults()
	sayf(w, `
Exit codes:
  %d  every file passed
  %d  at least one file failed
  %d  the command could not run
`, exitOK, exitInvalid, exitUsage)
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

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	sort.Strings(out)

	return out
}

// maxDefaultWorkers caps the default parallelism; beyond this the run is bound
// by reading files, not by checking them.
const maxDefaultWorkers = 8

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
