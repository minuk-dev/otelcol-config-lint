package schemagen_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/cmd/schemagen"
)

// seededDistribution is what the tests below file their fixtures under; it is
// the name --builder gives the generated schema, so the run compares against
// them.
const seededDistribution = "custom"

// seedRegistry writes a minimal schema for each version, which is what a
// registry filled by earlier runs looks like to this one.
func seedRegistry(t *testing.T, root string, versions ...string) {
	t.Helper()

	distribution := seededDistribution
	dir := filepath.Join(root, distribution)
	require.NoError(t, os.MkdirAll(dir, 0o750))

	for _, v := range versions {
		writeFile(t, filepath.Join(dir, v+".yaml"),
			"collectorVersion: "+v+"\ndistribution: "+distribution+
				"\ncomponents:\n  receiver:\n    legacy:\n      type: legacy\n")
	}
}

// TestSummaryComparesAgainstTheServedRelease covers --summary: what the run
// writes is the difference against the newest release the registry already
// held, which is what makes the generated files reviewable.
func TestSummaryComparesAgainstTheServedRelease(t *testing.T) {
	t.Parallel()

	registry := t.TempDir()
	seedRegistry(t, registry, "v0.150.0")

	manifest := singleComponentManifest(t, t.TempDir())
	summary := filepath.Join(t.TempDir(), "summary.md")

	code, _, stderr := run(t, "--builder", "custom="+manifest,
		"--registry", registry, "--summary", summary, "--cache", t.TempDir())

	require.Equal(t, schemagen.ExitOK, code, "run failed: %s", stderr)

	written := readFile(t, summary)
	assert.Contains(t, written, "### custom: `v0.150.0` → `v0.157.0`")
	assert.Contains(t, written, "- receiver `otlp`", "the added component is missing")
	assert.Contains(t, written, "- receiver `legacy`", "the dropped component is missing")
}

// A distribution the registry has never carried has nothing to compare
// against, which is reported as its size rather than as several hundred
// additions.
func TestSummaryOfAFirstRelease(t *testing.T) {
	t.Parallel()

	manifest := singleComponentManifest(t, t.TempDir())
	summary := filepath.Join(t.TempDir(), "summary.md")

	code, _, stderr := run(t, "--builder", "custom="+manifest,
		"--registry", t.TempDir(), "--summary", summary, "--cache", t.TempDir())

	require.Equal(t, schemagen.ExitOK, code, "run failed: %s", stderr)
	assert.Contains(t, readFile(t, summary), "### custom: new at `v0.157.0`")
}

// A release the registry already holds is being generated again because the
// generator changed, so the summary is what this run changes about that
// release -- not what upstream changed a release earlier, which in a backfill
// generating oldest first would be measured against a file the same run had
// just rewritten.
func TestSummaryOfARegeneratedReleaseComparesAgainstItself(t *testing.T) {
	t.Parallel()

	registry := t.TempDir()
	seedRegistry(t, registry, "v0.150.0", "v0.157.0")

	manifest := singleComponentManifest(t, t.TempDir())
	summary := filepath.Join(t.TempDir(), "summary.md")

	code, _, stderr := run(t, "--builder", "custom="+manifest,
		"--registry", registry, "--summary", summary, "--cache", t.TempDir())

	require.Equal(t, schemagen.ExitOK, code, "run failed: %s", stderr)

	written := readFile(t, summary)
	assert.Contains(t, written, "### custom: `v0.157.0` regenerated")
	assert.NotContains(t, written, "`v0.150.0`", "compared against the release before it")
}

func TestSummaryNeedsARegistry(t *testing.T) {
	t.Parallel()

	code, _, stderr := run(t, "--builder", "manifest.yaml",
		"--summary", filepath.Join(t.TempDir(), "summary.md"))

	assert.Equal(t, schemagen.ExitUsage, code)
	assert.Contains(t, stderr, schemagen.ErrNoPrevious.Error())
}

// TestRetainDropsOlderReleases covers the retention policy: the registry is
// left holding the window it was asked for, and the index is rebuilt from what
// survived rather than from what was generated.
func TestRetainDropsOlderReleases(t *testing.T) {
	t.Parallel()

	registry := t.TempDir()
	seedRegistry(t, registry, "v0.140.0", "v0.150.0", "v0.156.0")

	manifest := singleComponentManifest(t, t.TempDir())

	code, _, stderr := run(t, "--builder", "custom="+manifest, "--registry", registry,
		"--retain", "2", "--retain-every", "0", "--cache", t.TempDir())

	require.Equal(t, schemagen.ExitOK, code, "run failed: %s", stderr)

	assert.FileExists(t, filepath.Join(registry, "custom", "v0.157.0.yaml"))
	assert.FileExists(t, filepath.Join(registry, "custom", "v0.156.0.yaml"))
	assert.NoFileExists(t, filepath.Join(registry, "custom", "v0.150.0.yaml"))
	assert.NoFileExists(t, filepath.Join(registry, "custom", "v0.140.0.yaml"))

	index := readFile(t, filepath.Join(registry, "index.json"))
	assert.Contains(t, index, "v0.156.0")
	assert.NotContains(t, index, "v0.150.0", "the index still offers a release that was dropped")
}

// A milestone is kept however old it is, so a config pinned to a round release
// keeps resolving after the ones either side of it are gone.
func TestRetainKeepsMilestones(t *testing.T) {
	t.Parallel()

	registry := t.TempDir()
	seedRegistry(t, registry, "v0.140.0", "v0.150.0", "v0.156.0")

	manifest := singleComponentManifest(t, t.TempDir())

	code, _, stderr := run(t, "--builder", "custom="+manifest, "--registry", registry,
		"--retain", "1", "--cache", t.TempDir())

	require.Equal(t, schemagen.ExitOK, code, "run failed: %s", stderr)

	assert.FileExists(t, filepath.Join(registry, "custom", "v0.150.0.yaml"))
	assert.FileExists(t, filepath.Join(registry, "custom", "v0.140.0.yaml"))
	assert.NoFileExists(t, filepath.Join(registry, "custom", "v0.156.0.yaml"))
}

// Without --retain the registry keeps everything, which is what a local
// regeneration wants.
func TestRetainDefaultsToKeepingEverything(t *testing.T) {
	t.Parallel()

	registry := t.TempDir()
	seedRegistry(t, registry, "v0.156.0")

	manifest := singleComponentManifest(t, t.TempDir())

	code, _, stderr := run(t, "--builder", "custom="+manifest,
		"--registry", registry, "--cache", t.TempDir())

	require.Equal(t, schemagen.ExitOK, code, "run failed: %s", stderr)
	assert.FileExists(t, filepath.Join(registry, "custom", "v0.156.0.yaml"))
}

func TestRetainNeedsARegistry(t *testing.T) {
	t.Parallel()

	code, _, stderr := run(t, "--builder", "manifest.yaml", "--retain", "5")

	assert.Equal(t, schemagen.ExitUsage, code)
	assert.Contains(t, stderr, schemagen.ErrNothingToPrune.Error())
}
