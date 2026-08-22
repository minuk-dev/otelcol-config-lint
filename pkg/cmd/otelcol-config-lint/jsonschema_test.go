package otelcolconfiglint_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/cmd/otelcol-config-lint/exit"
	"github.com/minuk-dev/otelcol-config-lint/pkg/cmd/otelcol-config-lint/settings"
	"github.com/minuk-dev/otelcol-config-lint/pkg/ruleset"
)

// committedSchema is the generated copy an editor is pointed at.
const committedSchema = "../../../" + settings.SchemaFile

// settingsFixtures holds one settings file per case the schema has to get
// right. The valid ones are what an editor must not underline; the invalid ones
// are what it has to catch.
const settingsFixtures = "../../../testdata/settings"

// TestTheCommittedSchemaIsUpToDate is what keeps an editor honest: the rule
// names the schema enumerates come from the rule set, so a rule added without
// regenerating would leave every editor underlining a name that works.
//
// Regenerate with: make config-schema.
func TestTheCommittedSchemaIsUpToDate(t *testing.T) {
	t.Parallel()

	want, err := settings.Schema()
	require.NoError(t, err)

	got, err := os.ReadFile(committedSchema)
	require.NoError(t, err, "the schema an editor points at has to be committed")

	assert.Equal(t, string(want), string(got), "%s is stale; run: make config-schema", committedSchema)
}

// TestTheSchemaCommandPrintsTheSchema pins that the command and the committed
// file are the same document, since the command is how it is regenerated.
func TestTheSchemaCommandPrintsTheSchema(t *testing.T) {
	t.Parallel()

	code, out, errOut := run(t, "", "config-schema")
	require.Equal(t, 0, code, "%s", errOut)

	want, err := os.ReadFile(committedSchema)
	require.NoError(t, err)

	assert.Equal(t, string(want), out)
}

// TestTheSchemaNamesEveryRule guards the part of the document that moves. The
// generator builds the list from rule.All, so this fails only if a rule name
// stopped reaching the schema at all.
func TestTheSchemaNamesEveryRule(t *testing.T) {
	t.Parallel()

	doc, err := settings.Schema()
	require.NoError(t, err)

	var parsed struct {
		Defs struct {
			Rule struct {
				Enum []string `json:"enum"`
			} `json:"rule"`
		} `json:"$defs"`
	}

	require.NoError(t, json.Unmarshal(doc, &parsed))

	for _, r := range ruleset.All() {
		assert.Contains(t, parsed.Defs.Rule.Enum, r.Name())
	}

	assert.Len(t, parsed.Defs.Rule.Enum, len(ruleset.All()), "the enum should hold the rule set and nothing else")
}

// TestTheSettingsFixturesAreWhatTheySayTheyAre holds the linter to the same
// verdict CI holds the JSON Schema to. Both read the same fixtures, so a schema
// that disagreed with the loader would show up as one of the two accepting a
// file the other rejects.
func TestTheSettingsFixturesAreWhatTheySayTheyAre(t *testing.T) {
	t.Parallel()

	for _, group := range []struct {
		dir     string
		usage   bool
		explain string
	}{
		{dir: "valid", usage: false, explain: "should be accepted"},
		{dir: "invalid", usage: true, explain: "should be a usage error"},
	} {
		files, err := filepath.Glob(filepath.Join(settingsFixtures, group.dir, "*.yaml"))
		require.NoError(t, err)
		require.NotEmpty(t, files, "%s holds no fixtures", group.dir)

		for _, path := range files {
			// No severity flags: a flag wins over the file, and would hide a
			// fixture whose whole point is the value it writes.
			code, _, errOut := lint(t, "", "--config", path, validConfig)

			assert.Equalf(t, group.usage, code == exit.Usage,
				"%s %s, got exit %d: %s", path, group.explain, code, errOut)
		}
	}
}
