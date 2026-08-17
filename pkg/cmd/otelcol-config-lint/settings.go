package otelcolconfiglint

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/afero"
	"gopkg.in/yaml.v3"
)

// ErrUnknownSettingsVersion reports a settings file written against a schema
// this release does not know.
var ErrUnknownSettingsVersion = errors.New("unknown settings version")

// SettingsVersion is the schema the settings file is read as. A file may state
// it to be explicit; a file that does not is read as this version. It exists so
// a later schema can be told apart from this one rather than guessed at.
const SettingsVersion = "1"

// DefaultSettingsFile is the file looked for when no --config is given. It is
// searched for in the working directory and then in each parent.
const DefaultSettingsFile = ".otelcol-config-lint.yaml"

// settingsNames are the spellings of the default file, tried in order. Both
// YAML extensions are accepted because a repository should not have to rename
// a file it already has.
func settingsNames() []string {
	return []string{DefaultSettingsFile, ".otelcol-config-lint.yml"}
}

// settings is the file form of the command line options, so a repository can
// commit its linting policy instead of repeating flags in CI.
//
// The layout follows golangci-lint: what to check under "run", which rules
// under "rules", what counts as a failure under "issues", and how it is printed
// under "output". Every flag has a key here; the flags are the single-run
// override, and the file is what the repository commits.
//
// The flat keys the first release shipped are still read -- see normalize --
// because a settings file that quietly stops taking effect is a worse outcome
// than one that is out of style.
type settings struct {
	// Version names the schema this file is written against.
	Version string `yaml:"version"`

	// Run is what to check, and against which collector.
	Run runSettings `yaml:"run"`
	// Rules is which rules run and at what level.
	Rules rulesSettings `yaml:"rules"`
	// Issues is which findings make a file fail.
	Issues issuesSettings `yaml:"issues"`
	// Output is how the findings are printed.
	Output outputSettings `yaml:"output"`

	// The flat form of the first release. Anything written here is folded into
	// the blocks above, which win where both are written.
	CollectorVersion     string              `yaml:"collectorVersion"`
	Distribution         string              `yaml:"distribution"`
	SchemaLocations      []string            `yaml:"schemaLocations"`
	Strict               *bool               `yaml:"strict"`
	IgnoreMissingSchemas *bool               `yaml:"ignoreMissingSchemas"`
	Summary              *bool               `yaml:"summary"`
	MinSeverity          string              `yaml:"minSeverity"`
	FailOn               string              `yaml:"failOn"`
	Disable              []string            `yaml:"disable"`
	Severity             map[string]string   `yaml:"severity"`
	Exclude              []string            `yaml:"exclude"`
	Kubernetes           *kubernetesSettings `yaml:"kubernetes"`
}

// runSettings is the "run" block: which collector the configs target and which
// files are checked against it.
type runSettings struct {
	// CollectorVersion is the release to validate against, e.g. v0.157.0.
	CollectorVersion string `yaml:"collectorVersion"`
	// Distribution names the collector binary the config will run on.
	Distribution string `yaml:"distribution"`
	// SchemaLocations are searched in order before the published registry.
	SchemaLocations []string `yaml:"schemaLocations"`
	// Strict reports unknown component settings as errors.
	Strict *bool `yaml:"strict"`
	// IgnoreMissingSchemas keeps components absent from the schema from
	// failing the run, for a custom distribution.
	IgnoreMissingSchemas *bool `yaml:"ignoreMissingSchemas"`
	// Concurrency is how many files are checked in parallel.
	Concurrency *int `yaml:"concurrency"`
	// Exclude are glob patterns skipped when walking a directory.
	Exclude []string `yaml:"exclude"`
	// Kubernetes describes the pods the configs run in, per path, for the
	// rules that cannot judge a config without knowing what it runs in.
	Kubernetes kubernetesSettings `yaml:"kubernetes"`
}

// rulesSettings is the "rules" block: which rules run, at what level, and with
// what settings of their own.
type rulesSettings struct {
	// Default is the set to start from: "all", every registered rule, or
	// "none", which runs only what enable names.
	Default string `yaml:"default"`
	// Enable turns rules on, and is how anything runs under "none".
	Enable []string `yaml:"enable"`
	// Disable turns rules off.
	Disable []string `yaml:"disable"`
	// Severity re-levels a rule, e.g. missing-batch: warning. Writing "off"
	// disables it, exactly as listing it under disable does.
	Severity map[string]string `yaml:"severity"`
	// Settings holds each rule's own block, keyed by rule name, the way
	// golangci-lint's linters take settings under their name. A rule that
	// reads no settings reports a block written for it rather than ignoring
	// it -- see rule.Configurable.
	Settings map[string]yaml.Node `yaml:"settings"`
}

// issuesSettings is the "issues" block: which findings are reported and which
// of them make a file fail.
type issuesSettings struct {
	// MinSeverity is the lowest severity worth printing.
	MinSeverity string `yaml:"minSeverity"`
	// FailOn is the severity that makes a file invalid.
	FailOn string `yaml:"failOn"`
	// ExitOnError stops the run at the first file that fails.
	ExitOnError *bool `yaml:"exitOnError"`
}

// outputSettings is the "output" block: how results are printed, as opposed to
// what counts as a result.
type outputSettings struct {
	// Format is text, json, junit, tap or github.
	Format string `yaml:"format"`
	// Summary appends a count of the outcomes.
	Summary *bool `yaml:"summary"`
	// Verbose also reports the files that passed.
	Verbose *bool `yaml:"verbose"`
	// Color prints in colour when the destination is a terminal. Setting it
	// false is what --no-color does.
	Color *bool `yaml:"color"`
}

// UnmarshalYAML also accepts the block written as a bare format name, which is
// the flat form the first release shipped: "output: json" and
// "output: {format: json}" mean the same thing.
func (o *outputSettings) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		err := node.Decode(&o.Format)
		if err != nil {
			return fmt.Errorf("output: %w", err)
		}

		return nil
	}

	// The block is decoded through a type without this method, both to avoid
	// recursing and to get the strictness the top-level decoder was given:
	// Node.Decode does not carry KnownFields over.
	type output outputSettings

	var block output

	err := decodeStrict(node, &block)
	if err != nil {
		return fmt.Errorf("output: %w", err)
	}

	*o = outputSettings(block)

	return nil
}

// normalize folds the flat first-release keys into the blocks and returns the
// ones that were written, so the caller can say they are on their way out. A
// key written in both forms keeps the block's value: the new spelling is the
// one the file means.
func (s *settings) normalize() []string {
	return append(s.foldScalars(), s.foldLists()...)
}

// foldScalars folds the flat keys holding a single value.
func (s *settings) foldScalars() []string {
	var used []string

	str := func(name string, dst *string, v string) {
		if v == "" {
			return
		}

		used = append(used, name)

		if *dst == "" {
			*dst = v
		}
	}
	boolean := func(name string, dst **bool, v *bool) {
		if v == nil {
			return
		}

		used = append(used, name)

		if *dst == nil {
			*dst = v
		}
	}

	str("collectorVersion", &s.Run.CollectorVersion, s.CollectorVersion)
	str("distribution", &s.Run.Distribution, s.Distribution)
	str("minSeverity", &s.Issues.MinSeverity, s.MinSeverity)
	str("failOn", &s.Issues.FailOn, s.FailOn)
	boolean("strict", &s.Run.Strict, s.Strict)
	boolean("ignoreMissingSchemas", &s.Run.IgnoreMissingSchemas, s.IgnoreMissingSchemas)
	boolean("summary", &s.Output.Summary, s.Summary)

	return used
}

// foldLists folds the flat keys holding a collection, which cannot share the
// emptiness test the scalars use.
func (s *settings) foldLists() []string {
	var used []string

	list := func(name string, dst *[]string, v []string) {
		if len(v) == 0 {
			return
		}

		used = append(used, name)

		if len(*dst) == 0 {
			*dst = v
		}
	}

	list("schemaLocations", &s.Run.SchemaLocations, s.SchemaLocations)
	list("exclude", &s.Run.Exclude, s.Exclude)
	list("disable", &s.Rules.Disable, s.Disable)

	if len(s.Severity) > 0 {
		used = append(used, "severity")

		if len(s.Rules.Severity) == 0 {
			s.Rules.Severity = s.Severity
		}
	}

	if s.Kubernetes != nil {
		used = append(used, "kubernetes")

		if !s.Run.Kubernetes.written() {
			s.Run.Kubernetes = *s.Kubernetes
		}
	}

	return used
}

// loadSettings reads the settings file and reports where it was read from. When
// no path was given the default file is looked for, and not finding one is not
// an error; an explicitly named file that is missing is.
func (o *Options) loadSettings() (*settings, string, error) {
	//nolint:exhaustruct // an absent file means every option keeps its default
	empty := &settings{}

	path := o.settingsFile

	switch {
	case o.noConfig:
		return empty, "", nil
	case path == "":
		path = findSettings(o.fs(), workingDir())
		if path == "" {
			return empty, "", nil
		}
	}

	src, err := afero.ReadFile(o.fs(), path)
	if err != nil {
		// A discovered file that vanished between the two calls is treated as
		// no file at all, which is what it was a moment ago.
		if o.settingsFile == "" && errors.Is(err, fs.ErrNotExist) {
			return empty, "", nil
		}

		return nil, "", fmt.Errorf("read settings: %w", err)
	}

	s, err := parseSettings(src)
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", path, err)
	}

	return s, path, nil
}

// parseSettings decodes a settings file, rejecting keys it does not know: a
// misspelled key is policy that silently did not apply.
func parseSettings(src []byte) (*settings, error) {
	var s settings

	dec := yaml.NewDecoder(bytes.NewReader(src))
	dec.KnownFields(true)

	err := dec.Decode(&s)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err //nolint:wrapcheck // the caller names the file
	}

	if s.Version != "" && s.Version != SettingsVersion {
		return nil, fmt.Errorf("%w %q (this release reads version %s)",
			ErrUnknownSettingsVersion, s.Version, SettingsVersion)
	}

	return &s, nil
}

// findSettings looks for the default file in dir and then in each parent, so a
// policy committed at the repository root governs a run started from a
// subdirectory. The search stops at the directory holding .git: past the
// repository is somebody else's policy, not this project's.
func findSettings(fsys afero.Fs, dir string) string {
	for {
		for _, name := range settingsNames() {
			path := filepath.Join(dir, name)
			if ok, _ := afero.Exists(fsys, path); ok {
				return path
			}
		}

		if ok, _ := afero.Exists(fsys, filepath.Join(dir, ".git")); ok {
			return ""
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}

		dir = parent
	}
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

// decodeStrict decodes a node into target, rejecting keys the target does not
// have. It goes back through the text because that is the only decoder yaml.v3
// lets KnownFields be set on -- Node.Decode is always lenient.
func decodeStrict(node *yaml.Node, target any) error {
	src, err := yaml.Marshal(node)
	if err != nil {
		return fmt.Errorf("re-encode: %w", err)
	}

	dec := yaml.NewDecoder(bytes.NewReader(src))
	dec.KnownFields(true)

	err = dec.Decode(target)
	if err != nil && !errors.Is(err, io.EOF) {
		return err //nolint:wrapcheck // the caller names the block
	}

	return nil
}
