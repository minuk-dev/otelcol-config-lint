package otelcolconfiglint_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/settings"
)

// absSchemas resolves the schema fixture before the caller changes directory:
// the discovery tests run from a temporary tree, where a path relative to this
// package points nowhere.
func absSchemas(t *testing.T) string {
	t.Helper()

	abs, err := filepath.Abs(repoSchemas)
	require.NoError(t, err)

	return abs
}

// firedRules returns every rule a JSON report flagged, across all its files.
func firedRules(t *testing.T, out string) []string {
	t.Helper()

	return lo.Uniq(lo.Flatten(lo.Values(rulesFired(t, out))))
}

// v0110Config uses the logging exporter, which v0.110.0 ships and v0.157.0 does
// not, so which release a run actually targeted is visible in its result.
const v0110Config = "receivers:\n  otlp:\n    protocols:\n      grpc:\nexporters:\n  logging:\n" +
	"service:\n  pipelines:\n    traces:\n      receivers: [otlp]\n      exporters: [logging]\n"

// writeSettings writes a settings file into a fresh directory and returns its
// path, for the tests that name it with --config rather than have it found.
func writeSettings(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "settings.yaml")
	writeFile(t, path, content)

	return path
}

// TestTheBlocksAreWhereTheOptionsLive walks one setting out of each block, so a
// block that stopped being read shows up here rather than in a run that quietly
// ignored the policy it was given.
func TestTheBlocksAreWhereTheOptionsLive(t *testing.T) {
	t.Parallel()

	path := writeSettings(t, `version: "1"
run:
  collectorVersion: v0.110.0
rules:
  disable:
    - missing-batch
issues:
  minSeverity: error
output:
  format: json
  summary: true
`)

	code, out, errOut := lint(t, v0110Config, "--config", path, "-")
	require.Equal(t, 0, code, "run.collectorVersion should select v0.110.0: %s%s", out, errOut)

	assert.Contains(t, out, `"summary"`, "output.summary should append the counts")
	assert.NotContains(t, out, "missing-batch", "rules.disable should turn the rule off")
	assert.NotContains(t, out, `"severity":"warning"`, "issues.minSeverity should drop warnings")
}

func TestFlagsWinOverTheBlocks(t *testing.T) {
	t.Parallel()

	path := writeSettings(t, "run:\n  collectorVersion: v0.110.0\nissues:\n  minSeverity: error\n")

	code, out, _ := lint(t, v0110Config, "--config", path, "--collector-version", "v0.157.0", "-")

	assert.Equal(t, 1, code)
	assert.Contains(t, out, "unknown-component", "--collector-version should beat run.collectorVersion")
}

// TestTheFlatFormIsStillRead pins the compatibility promise: a settings file
// written against the first release keeps working, and says what to rename.
func TestTheFlatFormIsStillRead(t *testing.T) {
	t.Parallel()

	path := writeSettings(t, "collectorVersion: v0.110.0\nminSeverity: error\ndisable:\n  - missing-batch\n")

	code, out, errOut := lint(t, v0110Config, "--config", path, "-")
	require.Equal(t, 0, code, "the flat keys should still take effect: %s%s", out, errOut)

	assert.Contains(t, errOut, "deprecated top-level keys")
	assert.Contains(t, errOut, "collectorVersion")
	assert.Contains(t, errOut, "minSeverity")
	assert.Contains(t, errOut, "disable")
}

// TestABlockBeatsTheFlatKeyItReplaced settles the one ambiguity of reading both
// forms: a file that writes a setting twice means the newer spelling.
func TestABlockBeatsTheFlatKeyItReplaced(t *testing.T) {
	t.Parallel()

	path := writeSettings(t, "collectorVersion: v0.157.0\nminSeverity: error\nrun:\n  collectorVersion: v0.110.0\n")

	code, out, errOut := lint(t, v0110Config, "--config", path, "-")

	assert.Equal(t, 0, code, "run.collectorVersion should win over the flat key: %s%s", out, errOut)
}

// TestOutputAcceptsABareFormat keeps the flat "output: json" spelling working
// now that the key names a block.
func TestOutputAcceptsABareFormat(t *testing.T) {
	t.Parallel()

	path := writeSettings(t, "output: json\n")

	_, out, _ := lint(t, "", "--config", path, "--min-severity", "error", badConfig)

	assert.Contains(t, out, `"filename"`, "a bare format should still select json")
}

//nolint:paralleltest // t.Chdir cannot be called from a parallel test
func TestASettingsFileIsFoundInAParentDirectory(t *testing.T) {
	schemas, dir := absSchemas(t), t.TempDir()
	writeFile(t, filepath.Join(dir, ".otelcol-config-lint.yaml"),
		"run:\n  collectorVersion: v0.110.0\nissues:\n  minSeverity: error\n")
	writeFile(t, filepath.Join(dir, "a", "b", "keep.txt"), "")

	t.Chdir(filepath.Join(dir, "a", "b"))

	code, out, errOut := lint(t, v0110Config, "--schema-location", schemas, "--verbose", "-")
	require.Equal(t, 0, code, "the settings file above should apply: %s%s", out, errOut)

	assert.Contains(t, errOut, "settings read from", "--verbose should say which file was read")
}

// TestTheSearchStopsAtTheRepositoryRoot keeps a run from picking up whatever
// happens to sit above the project it is linting.
//
//nolint:paralleltest // t.Chdir cannot be called from a parallel test
func TestTheSearchStopsAtTheRepositoryRoot(t *testing.T) {
	schemas, dir := absSchemas(t), t.TempDir()
	writeFile(t, filepath.Join(dir, ".otelcol-config-lint.yaml"), "run:\n  collectorVersion: v0.110.0\n")
	writeFile(t, filepath.Join(dir, "repo", ".git"), "gitdir: elsewhere\n")
	writeFile(t, filepath.Join(dir, "repo", "sub", "keep.txt"), "")

	t.Chdir(filepath.Join(dir, "repo", "sub"))

	// v0.110.0 would accept the logging exporter; the default latest does not.
	code, out, _ := lint(t, v0110Config, "--schema-location", schemas, "--min-severity", "error", "-")

	assert.Equal(t, 1, code, "a file above the repository must not apply:\n%s", out)
}

//nolint:paralleltest // t.Chdir cannot be called from a parallel test
func TestTheYmlSpellingIsFoundToo(t *testing.T) {
	schemas, dir := absSchemas(t), t.TempDir()
	writeFile(t, filepath.Join(dir, ".otelcol-config-lint.yml"),
		"run:\n  collectorVersion: v0.110.0\nissues:\n  minSeverity: error\n")

	t.Chdir(dir)

	code, out, errOut := lint(t, v0110Config, "--schema-location", schemas, "-")

	assert.Equal(t, 0, code, "the .yml spelling should be read: %s%s", out, errOut)
}

//nolint:paralleltest // t.Chdir cannot be called from a parallel test
func TestNoConfigIgnoresTheFileThatWouldBeFound(t *testing.T) {
	schemas, dir := absSchemas(t), t.TempDir()
	writeFile(t, filepath.Join(dir, ".otelcol-config-lint.yaml"),
		"run:\n  collectorVersion: v0.110.0\nissues:\n  minSeverity: error\n")

	t.Chdir(dir)

	code, out, _ := lint(t, v0110Config,
		"--schema-location", schemas, "--no-config", "--min-severity", "error", "-")

	assert.Equal(t, 1, code, "--no-config should leave the run on the defaults:\n%s", out)
}

// TestDefaultNoneRunsOnlyWhatIsEnabled is golangci-lint's opt-in mode: a
// project that wants a short list states it rather than disabling the rest.
func TestDefaultNoneRunsOnlyWhatIsEnabled(t *testing.T) {
	t.Parallel()

	path := writeSettings(t, "rules:\n  default: none\n  enable:\n    - invalid-value\n")

	code, out, _ := lint(t, "", "--config", path, "--output", "json", badConfig)
	require.Equal(t, 1, code, "the enabled rule should still fail the file:\n%s", out)

	fired := firedRules(t, out)
	assert.Contains(t, fired, "invalid-value", "the enabled rule should run")
	assert.NotContains(t, fired, "unknown-component", "nothing else should")
}

func TestEnableAndDisableAreAlsoFlags(t *testing.T) {
	t.Parallel()

	code, out, _ := lint(t, "", "--default", "none", "-E", "invalid-value", "--output", "json", badConfig)
	require.Equal(t, 1, code)

	fired := firedRules(t, out)
	assert.Contains(t, fired, "invalid-value")
	assert.NotContains(t, fired, "unknown-component")

	_, out, _ = lint(t, "", "-D", "invalid-value,unknown-component", "--output", "json", badConfig)

	fired = firedRules(t, out)
	assert.NotContains(t, fired, "invalid-value", "-D should turn a rule off")
	assert.NotContains(t, fired, "unknown-component", "-D should take a comma-separated list")
}

// TestAFlagAddsToTheFilesRuleLists pins that the lists merge: the file states
// the project policy and a flag adds to it for one run.
func TestAFlagAddsToTheFilesRuleLists(t *testing.T) {
	t.Parallel()

	path := writeSettings(t, "rules:\n  disable:\n    - invalid-value\n")

	_, out, _ := lint(t, "", "--config", path, "-D", "unknown-component", "--output", "json", badConfig)

	fired := firedRules(t, out)
	assert.NotContains(t, fired, "invalid-value", "the file's disable should still hold")
	assert.NotContains(t, fired, "unknown-component", "the flag's should hold as well")
}

func TestARuleCannotBeBothEnabledAndDisabled(t *testing.T) {
	t.Parallel()

	path := writeSettings(t, "rules:\n  enable: [missing-batch]\n  disable: [missing-batch]\n")

	code, _, errOut := lint(t, "", "--config", path, badConfig)

	assert.Equal(t, 2, code)
	assert.Contains(t, errOut, "missing-batch")
}

func TestAnUnknownRuleSetIsAUsageError(t *testing.T) {
	t.Parallel()

	code, _, errOut := lint(t, "", "--default", "most", badConfig)

	assert.Equal(t, 2, code)
	assert.Contains(t, errOut, "most")
}

// TestSettingsForARuleThatTakesNoneIsAUsageError is the schema doing its job:
// the block is reserved for the rules that will read one, and writing a block
// nothing reads is reported rather than ignored.
func TestSettingsForARuleThatTakesNoneIsAUsageError(t *testing.T) {
	t.Parallel()

	path := writeSettings(t, "rules:\n  settings:\n    missing-batch:\n      timeout: 5s\n")

	code, _, errOut := lint(t, "", "--config", path, badConfig)

	assert.Equal(t, 2, code)
	assert.Contains(t, errOut, "missing-batch")
	assert.Contains(t, errOut, "takes no settings")
}

func TestSettingsForAnUnknownRuleIsAUsageError(t *testing.T) {
	t.Parallel()

	path := writeSettings(t, "rules:\n  settings:\n    no-such-rule:\n      x: 1\n")

	code, _, errOut := lint(t, "", "--config", path, badConfig)

	assert.Equal(t, 2, code)
	assert.Contains(t, errOut, "no-such-rule")
}

// TestAMisspelledKeyIsReported covers both levels: policy that silently did not
// apply is the failure mode a settings file is most prone to.
func TestAMisspelledKeyIsReported(t *testing.T) {
	t.Parallel()

	for _, content := range []string{"runn:\n  strict: true\n", "output:\n  formt: json\n"} {
		path := writeSettings(t, content)

		code, _, errOut := lint(t, "", "--config", path, badConfig)

		assert.Equal(t, 2, code, "%q should not be accepted", content)
		assert.Contains(t, errOut, "not found")
	}
}

func TestASettingsFileFromAnotherVersionIsReported(t *testing.T) {
	t.Parallel()

	path := writeSettings(t, "version: \"2\"\nrun:\n  strict: true\n")

	code, _, errOut := lint(t, "", "--config", path, badConfig)

	assert.Equal(t, 2, code)
	assert.Contains(t, errOut, settings.Version)
}

// TestConcurrencyKeepsItsOldShorthand guards the rename: -n was the whole flag
// before it took golangci-lint's name for it, and workflows still spell it that
// way.
func TestConcurrencyKeepsItsOldShorthand(t *testing.T) {
	t.Parallel()

	for _, arg := range [][]string{{"-n", "2"}, {"--concurrency", "2"}} {
		args := append(append([]string{}, arg...), "--min-severity", "error", validConfig)

		code, _, errOut := lint(t, "", args...)

		assert.Equal(t, 0, code, "%v should be accepted: %s", arg, errOut)
	}
}

// TestTheRunBlockCarriesTheRestOfTheFlags covers the settings that had no file
// form until the blocks arrived.
func TestTheRunBlockCarriesTheRestOfTheFlags(t *testing.T) {
	t.Parallel()

	path := writeSettings(t, `run:
  concurrency: 2
  exclude:
    - "typos.yaml"
issues:
  minSeverity: error
  exitOnError: true
output:
  verbose: true
`)

	code, out, errOut := lint(t, "", "--config", path, invalidConfig)
	require.Equal(t, 1, code, "%s%s", out, errOut)

	assert.NotContains(t, out, "typos.yaml", "run.exclude should skip the file")
	assert.Contains(t, out, "valid", "output.verbose should report the files that passed")
}

// TestOutputColorIsTheNegationOfNoColor pins the one key whose sense is
// inverted against the flag it mirrors.
func TestOutputColorIsTheNegationOfNoColor(t *testing.T) {
	t.Parallel()

	path := writeSettings(t, "output:\n  color: false\nissues:\n  minSeverity: error\n")

	code, out, errOut := lint(t, "", "--config", path, badConfig)
	require.Equal(t, 1, code, "%s%s", out, errOut)

	assert.NotContains(t, out, "\x1b[", "colour should stay off")
}

func TestListRulesHonoursTheRuleBlock(t *testing.T) {
	t.Parallel()

	path := writeSettings(t, "rules:\n  default: none\n  enable:\n    - missing-batch\n")

	code, out, errOut := run(t, "", "list", "rules", "--config", path)
	require.Equal(t, 0, code, "%s", errOut)

	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		name, rest, _ := strings.Cut(line, " ")
		if name == "missing-batch" {
			assert.NotContains(t, rest, "off", "an enabled rule should not be listed as off")

			continue
		}

		assert.Contains(t, rest, "off", "%q should be listed as off under default: none", name)
	}
}
