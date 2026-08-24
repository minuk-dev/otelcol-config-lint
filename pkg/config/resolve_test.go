package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
)

// settings returns the settings a processor was parsed with, as key to value,
// so a test can say what a rule reading that component would find.
func settings(t *testing.T, f *config.File, id string) map[string]string {
	t.Helper()

	c, found := f.Component(config.KindProcessor, config.ParseID(id))
	require.True(t, found, "processor %s should be declared", id)
	require.NotNil(t, c.ValueNode, "processor %s should have settings", id)

	out := map[string]string{}
	for i := 0; i+1 < len(c.ValueNode.Content); i += 2 {
		out[c.ValueNode.Content[i].Value] = c.ValueNode.Content[i+1].Value
	}

	return out
}

func parse(t *testing.T, src string) *config.File {
	t.Helper()

	f, err := config.Parse("test.yaml", []byte(src))
	require.NoError(t, err)

	return f
}

// TestAMergeKeySuppliesSettings pins the shape the issue opens with: the
// collector resolves the merge before it reads a single setting, so a rule has
// to see the merged mapping rather than a key called "<<".
func TestAMergeKeySuppliesSettings(t *testing.T) {
	t.Parallel()

	f := parse(t, `
processors:
  memory_limiter/base: &shared
    check_interval: 1s
    limit_mib: 512
  memory_limiter:
    <<: *shared
    spike_limit_mib: 128
`)

	assert.Equal(t, map[string]string{
		"check_interval":  "1s",
		"limit_mib":       "512",
		"spike_limit_mib": "128",
	}, settings(t, f, "memory_limiter"))
}

// TestAKeyWrittenLocallyWinsOverAMergedOne pins the merge key's whole purpose:
// the anchor is the base and what the component writes itself is the override.
func TestAKeyWrittenLocallyWinsOverAMergedOne(t *testing.T) {
	t.Parallel()

	f := parse(t, `
processors:
  batch/base: &base
    send_batch_size: 500
    timeout: 5s
  batch:
    <<: *base
    send_batch_size: 8000
`)

	assert.Equal(t, "8000", settings(t, f, "batch")["send_batch_size"],
		"the local key should win over the merged one")
}

// TestAKeyWrittenAfterTheMergeStillWins pins that precedence is the merge
// key's, not the file's order: yaml.v3 lets the override sit below the "<<"
// line, and most configs write it that way.
func TestAKeyWrittenAfterTheMergeStillWins(t *testing.T) {
	t.Parallel()

	f := parse(t, "processors:\n  batch/base: &base {timeout: 5s}\n  batch:\n    <<: *base\n    timeout: 10s\n")

	assert.Equal(t, "10s", settings(t, f, "batch")["timeout"])
}

// TestAnAliasIsTheWholeComponentBody pins the shape the issue calls worse,
// because nothing in the old report hinted at the cause: every setting read as
// absent and the rules reported the absence.
func TestAnAliasIsTheWholeComponentBody(t *testing.T) {
	t.Parallel()

	f := parse(t, `
processors:
  memory_limiter/base: &shared
    check_interval: 1s
    limit_mib: 512
  memory_limiter: *shared
`)

	assert.Equal(t, map[string]string{"check_interval": "1s", "limit_mib": "512"},
		settings(t, f, "memory_limiter"))
}

// TestASequenceOfMergesAppliesTheFirstThatCarriesTheKey pins the order
// yaml.v3 applies "<<: [*a, *b]" in.
func TestASequenceOfMergesAppliesTheFirstThatCarriesTheKey(t *testing.T) {
	t.Parallel()

	f := parse(t, `
processors:
  batch/first: &first
    timeout: 1s
  batch/second: &second
    timeout: 2s
    send_batch_size: 500
  batch:
    <<: [*first, *second]
`)

	assert.Equal(t, map[string]string{"timeout": "1s", "send_batch_size": "500"},
		settings(t, f, "batch"),
		"the earlier merge should win the key both carry, and the later one should still supply its own")
}

// TestAMergeInsideAnAnchorIsResolvedToo pins that an anchor built on another
// anchor arrives merged, however many levels deep the base goes.
func TestAMergeInsideAnAnchorIsResolvedToo(t *testing.T) {
	t.Parallel()

	f := parse(t, `
processors:
  batch/root: &root
    timeout: 5s
  batch/middle: &middle
    <<: *root
    send_batch_size: 500
  batch:
    <<: *middle
    send_batch_max_size: 1000
`)

	assert.Equal(t, map[string]string{
		"timeout":             "5s",
		"send_batch_size":     "500",
		"send_batch_max_size": "1000",
	}, settings(t, f, "batch"))
}

// TestAMergedSettingKeepsThePositionItWasWrittenAt pins where a finding about
// a merged setting lands: on the line in the anchor, which is the line the
// reader has to edit. Repositioning it onto the component would point at a
// line that does not hold the setting.
func TestAMergedSettingKeepsThePositionItWasWrittenAt(t *testing.T) {
	t.Parallel()

	f := parse(t, "processors:\n  batch/base: &base\n    timeout: 5s\n  batch:\n    <<: *base\n")

	c, found := f.Component(config.KindProcessor, config.ParseID("batch"))
	require.True(t, found)
	require.Len(t, c.ValueNode.Content, 2)

	assert.Equal(t, 3, f.Pos(c.ValueNode.Content[1]).Line, "the value should keep the anchor's line")
}

// TestAQuotedMergeKeyIsAKeyOfItsOwn pins that the tag decides, not the
// spelling: yaml.v3 reads a quoted "<<" as a plain string key, and so does the
// collector.
func TestAQuotedMergeKeyIsAKeyOfItsOwn(t *testing.T) {
	t.Parallel()

	f := parse(t, "processors:\n  batch/base: &base {timeout: 5s}\n  batch:\n    \"<<\": *base\n")

	assert.Contains(t, settings(t, f, "batch"), "<<",
		"a quoted key is not a merge key")
}

// TestAMergeOfSomethingThatIsNotAMappingIsLeftAlone pins the one merge the
// collector cannot load either: yaml.v3 fails the decode outright, so dropping
// the key quietly would leave nothing for a rule to report.
func TestAMergeOfSomethingThatIsNotAMappingIsLeftAlone(t *testing.T) {
	t.Parallel()

	f := parse(t, "processors:\n  batch:\n    <<: hello\n")

	assert.Contains(t, settings(t, f, "batch"), "<<")
}

// TestACyclicAliasDoesNotHang pins that a document yaml.v3 refuses to decode
// still parses: the node tree comes back with the loop in it, and a linter
// that recursed into it would never report anything at all.
func TestACyclicAliasDoesNotHang(t *testing.T) {
	t.Parallel()

	done := make(chan struct{})

	go func() {
		defer close(done)

		_, err := config.Parse("test.yaml", []byte("processors:\n  batch: &loop\n    nested: [*loop]\n"))
		assert.NoError(t, err)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("parsing an anchor that contains itself did not finish")
	}
}

// TestALocalOverrideIsNotADuplicateKey pins that duplicates are read from the
// document as written. Merging first would make every override look like the
// same key declared twice, which is the merge key's purpose rather than a bug
// in the config.
func TestALocalOverrideIsNotADuplicateKey(t *testing.T) {
	t.Parallel()

	f := parse(t, `
processors:
  batch/base: &base
    timeout: 5s
  batch:
    <<: *base
    timeout: 10s
`)

	assert.Empty(t, f.DuplicateKeys)
}

// TestADuplicateInsideAnAnchorIsStillReported pins the other half: resolving
// does not hide a key the anchor itself declares twice.
func TestADuplicateInsideAnAnchorIsStillReported(t *testing.T) {
	t.Parallel()

	f := parse(t, "processors:\n  batch/base: &base\n    timeout: 5s\n    timeout: 10s\n  batch:\n    <<: *base\n")

	require.Len(t, f.DuplicateKeys, 1)
	assert.Equal(t, "timeout", f.DuplicateKeys[0].Key)
}

// TestAnAnchorMergedTwiceIsResolvedOnce pins that a base shared by several
// components is applied to each of them and not to itself twice over.
func TestAnAnchorMergedTwiceIsResolvedOnce(t *testing.T) {
	t.Parallel()

	f := parse(t, `
processors:
  batch/base: &base
    timeout: 5s
  batch/one:
    <<: *base
  batch/two:
    <<: *base
`)

	want := map[string]string{"timeout": "5s"}
	assert.Equal(t, want, settings(t, f, "batch/one"))
	assert.Equal(t, want, settings(t, f, "batch/two"))
	assert.Equal(t, want, settings(t, f, "batch/base"))
}
