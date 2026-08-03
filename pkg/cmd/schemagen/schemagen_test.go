package schemagen_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/cmd/schemagen"
)

// The upstream repositories a run harvests, and the archive root each one's
// tarball unpacks into.
const (
	coreRepo    = "open-telemetry/opentelemetry-collector"
	coreRoot    = "opentelemetry-collector-0.157.0"
	contribRepo = "open-telemetry/opentelemetry-collector-contrib"
	contribRoot = "opentelemetry-collector-contrib-0.157.0"
)

// run executes the command and returns its exit code and streams. The wiring
// mirrors cmd/schemagen/main.go, so what the tests assert on is what the binary
// does.
func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()

	var stdout, stderr bytes.Buffer

	cmd := schemagen.NewCommand(nil)
	cmd.SetArgs(args)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	err := cmd.Execute()
	if err != nil {
		cmd.PrintErrf("schemagen: %v\n", err)
	}

	return schemagen.ExitCode(err), stdout.String(), stderr.String()
}

func TestExitCode(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		in   error
		want int
	}{
		"a clean run":      {in: nil, want: schemagen.ExitOK},
		"a failed harvest": {in: os.ErrNotExist, want: schemagen.ExitFailure},
		"a bad invocation": {in: schemagen.ErrNoVersions, want: schemagen.ExitUsage},
		"an empty format":  {in: schemagen.ErrNoFormats, want: schemagen.ExitUsage},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, schemagen.ExitCode(tt.in))
		})
	}
}

func TestNoVersion(t *testing.T) {
	t.Parallel()

	code, _, stderr := run(t, "--out", t.TempDir())

	assert.Equal(t, schemagen.ExitUsage, code)
	assert.Contains(t, stderr, schemagen.ErrNoVersions.Error(), "the missing release is not reported")
	assert.Contains(t, stderr, "Usage:", "the usage is not printed")
}

func TestUnknownFlag(t *testing.T) {
	t.Parallel()

	code, _, stderr := run(t, "--nope")

	assert.Equal(t, schemagen.ExitUsage, code)
	assert.Contains(t, stderr, "unknown flag")
}

// TestPositionalArgument covers the likeliest slip: the release written as an
// argument rather than as --version.
func TestPositionalArgument(t *testing.T) {
	t.Parallel()

	code, _, stderr := run(t, "v0.157.0")

	assert.Equal(t, schemagen.ExitUsage, code)
	assert.Contains(t, stderr, "Usage:", "the usage is not printed")
}

// TestUnknownFormat covers --formats: a name nothing can be written in is
// refused before the first download, rather than writing YAML under it.
func TestUnknownFormat(t *testing.T) {
	t.Parallel()

	out := t.TempDir()

	code, _, stderr := run(t, "--version", "v0.157.0", "--formats", "toml",
		"--out", out, "--cache", t.TempDir(), "--overlays", t.TempDir())

	assert.Equal(t, schemagen.ExitUsage, code)
	assert.Contains(t, stderr, `unknown schema format "toml"`)

	entries, err := os.ReadDir(out)
	require.NoError(t, err)
	assert.Empty(t, entries, "a refused format still wrote to the registry")
}

// TestRunWithoutPrepare covers composing the options directly: the run falls
// back to the defaults instead of writing to a nil stream.
func TestRunWithoutPrepare(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	opts := &schemagen.Options{}
	cmd := schemagen.NewCommand(opts)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	require.ErrorIs(t, opts.Run(cmd), schemagen.ErrNoVersions)
}

func TestIncompleteOverlay(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "batch.yaml"), "fields:\n  type: map\n")

	code, _, stderr := run(t, "--version", "v0.157.0", "--overlays", dir, "--out", t.TempDir())

	assert.Equal(t, schemagen.ExitFailure, code)
	assert.Contains(t, stderr, "kind and type are required")
}

// TestGenerate runs the whole pipeline against archives planted in the cache,
// so a full generation is exercised without reaching the network.
func TestGenerate(t *testing.T) {
	t.Parallel()

	cache, out, overlays := t.TempDir(), t.TempDir(), t.TempDir()

	archive(t, cache, coreRepo, map[string]string{
		coreRoot + "/receiver/otlpreceiver/metadata.yaml": `
type: otlp
status:
  class: receiver
  stability:
    beta: [traces, metrics, logs]
  distributions: [core, contrib]
`,
		// The field schema is read from the component's own Config struct.
		coreRoot + "/receiver/otlpreceiver/config.go": "package otlpreceiver\n\n" +
			"type Config struct {\n" +
			"\t// Endpoint is where the receiver listens.\n" +
			"\tEndpoint string `mapstructure:\"endpoint\"`\n" +
			"\tTimeout  int    `mapstructure:\"timeout\"`\n" +
			"}\n",
		// Sample components under cmd/ are mdatagen's own fixtures, not
		// something a config can declare.
		coreRoot + "/cmd/mdatagen/internal/samplereceiver/metadata.yaml": `
type: sample
status:
  class: receiver
  stability:
    development: [traces]
`,
	})
	archive(t, cache, contribRepo, map[string]string{
		contribRoot + "/processor/spanprocessor/metadata.yaml": `
type: span
status:
  class: processor
  stability:
    alpha: [traces]
  distributions: [contrib]
`,
	})

	writeFile(t, filepath.Join(overlays, "span.yaml"), `
kind: processor
type: span
fields:
  type: map
  children:
    from_attributes: {type: list}
`)

	code, stdout, stderr := run(t, "--version", "0.157.0",
		"--out", out, "--cache", cache, "--overlays", overlays)

	require.Equal(t, schemagen.ExitOK, code, "run failed: %s", stderr)
	assert.Contains(t, stdout, "loaded 1 field overlay(s)")

	// The version was given without its "v", which the registry layout adds.
	core := readFile(t, filepath.Join(out, "core", "v0.157.0.yaml"))
	assert.Contains(t, core, "otlp:", "the core schema is missing the otlp receiver")
	assert.Contains(t, core, "endpoint:", "the otlp receiver took no fields from its Config struct")
	assert.NotContains(t, core, "sample:", "the core schema declared an mdatagen fixture")
	// The span processor ships in contrib only, so it must not leak into the
	// distribution the otlp receiver was filtered into.
	assert.NotContains(t, core, "span:", "the core schema holds a contrib-only component")

	contrib := readFile(t, filepath.Join(out, "contrib", "v0.157.0.json"))
	assert.Contains(t, contrib, `"span"`)
	assert.Contains(t, contrib, `"otlp"`)
	assert.Contains(t, contrib, `"from_attributes"`, "the span processor did not take the overlay's fields")

	index := readFile(t, filepath.Join(out, "index.json"))
	for _, want := range []string{"core", "contrib", "v0.157.0"} {
		assert.Contains(t, index, want)
	}
}

// TestSkipsMissingRelease covers a release one repository never tagged: the
// run reports it and carries on rather than failing.
func TestSkipsMissingRelease(t *testing.T) {
	t.Parallel()

	out := t.TempDir()

	// The cache is empty and the timeout expires immediately, so the first
	// fetch fails without waiting on the network.
	code, stdout, stderr := run(t, "--version", "v0.1.0", "--timeout", "1ns",
		"--out", out, "--cache", t.TempDir(), "--overlays", t.TempDir())

	require.Equal(t, schemagen.ExitOK, code, "run failed: %s", stderr)
	assert.Contains(t, stdout, "v0.1.0: skipped")
	assert.Contains(t, stdout, "skipped 1 of 1 releases")

	// The index is still written, listing nothing.
	index := readFile(t, filepath.Join(out, "index.json"))
	assert.Contains(t, index, "distributions")
}

// archive writes a gzipped tar into the cache directory under the name a
// download would have been saved as, so the run finds it already fetched.
func archive(t *testing.T, cacheDir, repo string, files map[string]string) {
	t.Helper()

	var buf bytes.Buffer

	zipped := gzip.NewWriter(&buf)
	tarred := tar.NewWriter(zipped)

	for name, body := range files {
		//nolint:exhaustruct // a regular file needs no ownership or timestamps
		err := tarred.WriteHeader(&tar.Header{
			Name:     name,
			Typeflag: tar.TypeReg,
			Mode:     0o600,
			Size:     int64(len(body)),
		})
		require.NoError(t, err, "write header for %s", name)

		_, err = tarred.Write([]byte(body))
		require.NoError(t, err, "write %s", name)
	}

	require.NoError(t, tarred.Close())
	require.NoError(t, zipped.Close())

	name := strings.ReplaceAll(repo, "/", "_") + "-v0.157.0.tar.gz"
	writeFile(t, filepath.Join(cacheDir, name), buf.String())
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()

	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	return string(raw)
}
