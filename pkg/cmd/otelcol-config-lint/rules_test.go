package otelcolconfiglint_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/ruleset"
)

// rulesDir holds one invalid config per rule, each next to the settings file
// that says which rules the run has on. The point of a fixture per rule is that
// a rule cannot be added without a config that shows what it reports, and a
// rule cannot stop reporting without a test saying so.
const rulesDir = "../../../testdata/rules"

// settingsSuffix is what tells a fixture's settings file from a fixture. Both
// are YAML in the same directory, so the suffix is what a walk sorts on.
const settingsSuffix = ".settings.yaml"

// fixture is the declaration next to a fixture, in the shape golangci-lint
// uses: the rules switched on, the ones switched off, and per-rule options.
// Everything under "run" describes the run itself and has a default.
type fixture struct {
	Rules struct {
		// Enable names the rules that must report on the fixture.
		Enable []string `yaml:"enable"`
		// Disable names the rules the run turns off, which must then stay
		// silent. It is how a fixture stays about one mistake when the config
		// that shows it cannot help making another.
		Disable []string `yaml:"disable"`
		// Settings holds per-rule options, keyed by rule name. No rule takes
		// one yet; the key is here so that adding one is a fixture edit rather
		// than a change to how fixtures are written.
		Settings map[string]map[string]any `yaml:"settings"`
	} `yaml:"rules"`

	Run struct {
		CollectorVersion string `yaml:"collectorVersion"`
		Distribution     string `yaml:"distribution"`
		// SchemaLocations are relative to the fixture directory, and searched
		// before the repository's schemas.
		SchemaLocations []string `yaml:"schemaLocations"`
		Strict          bool     `yaml:"strict"`
		// MinSeverity defaults to info, so a fixture for an info-level rule
		// needs to say nothing.
		MinSeverity string `yaml:"minSeverity"`
		Kubernetes  struct {
			MemoryRequest string `yaml:"memoryRequest"`
			MemoryLimit   string `yaml:"memoryLimit"`
		} `yaml:"kubernetes"`
	} `yaml:"run"`
}

// args renders the fixture's run as the command line that produces it, so what
// a test checks is a run anyone can repeat by hand.
func (f fixture) args(path string) []string {
	args := []string{"--output", "json", "--min-severity", lo.CoalesceOrEmpty(f.Run.MinSeverity, "info")}

	for _, loc := range f.Run.SchemaLocations {
		args = append(args, "--schema-location", filepath.Join(rulesDir, loc))
	}

	for flag, value := range map[string]string{
		"--collector-version": f.Run.CollectorVersion,
		"--distribution":      f.Run.Distribution,
		"--memory-request":    f.Run.Kubernetes.MemoryRequest,
		"--memory-limit":      f.Run.Kubernetes.MemoryLimit,
	} {
		if value != "" {
			args = append(args, flag, value)
		}
	}

	if f.Run.Strict {
		args = append(args, "--strict")
	}

	if len(f.Rules.Disable) > 0 {
		args = append(args, "--disable", strings.Join(f.Rules.Disable, ","))
	}

	return append(args, path)
}

// ruleFixtures reads every fixture in rulesDir, keyed by rule name.
func ruleFixtures(t *testing.T) map[string]fixture {
	t.Helper()

	paths, err := filepath.Glob(filepath.Join(rulesDir, "*.yaml"))
	require.NoError(t, err)

	out := map[string]fixture{}

	for _, path := range paths {
		if strings.HasSuffix(path, settingsSuffix) {
			continue
		}

		name := strings.TrimSuffix(filepath.Base(path), ".yaml")
		out[name] = readFixture(t, strings.TrimSuffix(path, ".yaml")+settingsSuffix)
	}

	require.NotEmpty(t, out, "no fixtures found in %s", rulesDir)

	return out
}

func readFixture(t *testing.T, path string) fixture {
	t.Helper()

	src, err := os.ReadFile(path)
	require.NoError(t, err, "every fixture needs a settings file saying which rules it is about")

	var f fixture

	dec := yaml.NewDecoder(strings.NewReader(string(src)))
	dec.KnownFields(true)

	require.NoError(t, dec.Decode(&f), "%s", path)

	return f
}

// TestEveryRuleHasAFixture is the coverage gate: a new rule is not finished
// until testdata carries a config it reports on, and the settings file next to
// it says so.
func TestEveryRuleHasAFixture(t *testing.T) {
	t.Parallel()

	fixtures := ruleFixtures(t)

	for _, r := range ruleset.All() {
		f, ok := fixtures[r.Name()]
		if !ok {
			t.Errorf("rule %q has no fixture; add %s/%s.yaml", r.Name(), rulesDir, r.Name())

			continue
		}

		assert.Contains(t, f.Rules.Enable, r.Name(),
			"%s.settings.yaml should enable the rule it is named after", r.Name())
	}

	for name, f := range fixtures {
		_, known := ruleset.Lookup(name)
		assert.Truef(t, known, "fixture %q names no registered rule", name)

		for _, r := range slices.Concat(f.Rules.Enable, f.Rules.Disable, lo.Keys(f.Rules.Settings)) {
			_, known := ruleset.Lookup(r)
			assert.Truef(t, known, "%s.settings.yaml names the unknown rule %q", name, r)
		}
	}
}

// TestRuleFixturesReportTheirRule runs each fixture through the command line
// the settings file describes: the rules it enables have to report, and the
// ones it disables have to stay quiet.
func TestRuleFixturesReportTheirRule(t *testing.T) {
	t.Parallel()

	for name, f := range ruleFixtures(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(rulesDir, name+".yaml")

			_, out, errOut := lint(t, "", f.args(path)...)
			fired := rulesFired(t, out)[name+".yaml"]

			for _, want := range f.Rules.Enable {
				assert.Containsf(t, fired, want,
					"%s.yaml should report %s; it reported %v\n%s", name, want, fired, errOut)
			}

			for _, off := range f.Rules.Disable {
				assert.NotContainsf(t, fired, off, "%s is disabled for %s.yaml", off, name)
			}
		})
	}
}

// TestRuleFixturesCommentTheirMistake keeps the fixtures readable as
// documentation: whoever opens one should read what is wrong on the line that
// is wrong, not have to run the linter to find out. A comment naming the rule
// has to sit on, or just above, a line the rule reports.
func TestRuleFixturesCommentTheirMistake(t *testing.T) {
	t.Parallel()

	for name, f := range ruleFixtures(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(rulesDir, name+".yaml")

			src, err := os.ReadFile(path)
			require.NoError(t, err)

			_, out, _ := lint(t, "", f.args(path)...)

			marked := commentedLines(string(src), name)
			require.NotEmptyf(t, marked, "%s.yaml should say in a comment what breaks %s", name, name)

			reported := reportedLines(t, out, name)
			require.NotEmpty(t, reported, "the fixture reported nothing to comment on")

			assert.Truef(t, lo.SomeBy(reported, func(line int) bool {
				return marked[line] || marked[line-1]
			}), "%s.yaml comments %s on lines %v, but reports it on lines %v",
				name, name, lo.Keys(marked), reported)
		})
	}
}

// commentedLines returns the lines carrying a comment that names the rule, as a
// set of 1-based line numbers.
func commentedLines(src, rule string) map[int]bool {
	out := map[int]bool{}

	for i, line := range strings.Split(src, "\n") {
		_, comment, found := strings.Cut(line, "#")
		if found && strings.Contains(comment, rule) {
			out[i+1] = true
		}
	}

	return out
}

// reportedLines returns the lines one rule reported on.
func reportedLines(t *testing.T, out, rule string) []int {
	t.Helper()

	var report struct {
		Files []struct {
			Diagnostics []struct {
				Rule     string `json:"rule"`
				Position struct {
					Line int `json:"line"`
				} `json:"position"`
			} `json:"diagnostics"`
		} `json:"files"`
	}

	require.NoError(t, json.Unmarshal([]byte(out), &report), "output is not valid JSON:\n%s", out)

	var lines []int

	for _, f := range report.Files {
		for _, d := range f.Diagnostics {
			if d.Rule == rule {
				lines = append(lines, d.Position.Line)
			}
		}
	}

	return lines
}
