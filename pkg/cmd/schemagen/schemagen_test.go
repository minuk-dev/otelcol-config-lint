package schemagen_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/cmd/schemagen"
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
		"a bad invocation": {in: schemagen.ErrNoManifests, want: schemagen.ExitUsage},
		"an empty format":  {in: schemagen.ErrNoFormats, want: schemagen.ExitUsage},
		"two destinations": {in: schemagen.ErrTwoOutputs, want: schemagen.ExitUsage},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, schemagen.ExitCode(tt.in))
		})
	}
}

func TestNoManifest(t *testing.T) {
	t.Parallel()

	code, _, stderr := run(t, "--registry", t.TempDir())

	assert.Equal(t, schemagen.ExitUsage, code)
	assert.Contains(t, stderr, schemagen.ErrNoManifests.Error(), "the missing manifest is not reported")
	assert.Contains(t, stderr, "Usage:", "the usage is not printed")
}

func TestUnknownFlag(t *testing.T) {
	t.Parallel()

	code, _, stderr := run(t, "--nope")

	assert.Equal(t, schemagen.ExitUsage, code)
	assert.Contains(t, stderr, "unknown flag")
}

// TestPositionalArgument covers the likeliest slip: the manifest written as an
// argument rather than as --builder.
func TestPositionalArgument(t *testing.T) {
	t.Parallel()

	code, _, stderr := run(t, "manifest.yaml")

	assert.Equal(t, schemagen.ExitUsage, code)
	assert.Contains(t, stderr, "Usage:", "the usage is not printed")
}

// TestUnknownFormat covers --formats: a name nothing can be written in is
// refused before a single module is resolved, rather than writing YAML under it.
func TestUnknownFormat(t *testing.T) {
	t.Parallel()

	out := t.TempDir()

	code, _, stderr := run(t, "--builder", "manifest.yaml", "--formats", "toml",
		"--registry", out, "--cache", t.TempDir(), "--overlays", t.TempDir())

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

	require.ErrorIs(t, opts.Run(cmd), schemagen.ErrNoManifests)
}

// TestIncompleteManifest covers the manifest checks: a file that names no
// distribution describes nothing to write.
func TestIncompleteManifest(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yaml")
	writeFile(t, path, "dist:\n  otelcol_version: 0.157.0\n")

	code, _, stderr := run(t, "--builder", path,
		"--registry", t.TempDir(), "--cache", t.TempDir(), "--overlays", t.TempDir())

	// One bad manifest is skipped, not fatal, so the run still ends cleanly.
	require.Equal(t, schemagen.ExitOK, code, "run ended badly: %s", stderr)
	assert.Contains(t, stderr, "dist.name is required")
	assert.Contains(t, stderr, "skipped 1 of 1 manifests")
}

// TestGenerate runs the whole pipeline against modules on disk: the manifest
// replaces every component with a local checkout, which is both what a
// distribution under development does and what keeps this test off the network.
func TestGenerate(t *testing.T) {
	t.Parallel()

	modules, out, overlays := t.TempDir(), t.TempDir(), t.TempDir()

	otlp := module(t, modules, "example.com/collector/receiver/otlpreceiver", map[string]string{
		"metadata.yaml": `
type: otlp
status:
  class: receiver
  stability:
    beta: [traces, metrics, logs]
`,
		// The field schema is read from the component's own Config struct.
		"config.go": "package otlpreceiver\n\n" +
			"type Config struct {\n" +
			"\t// Endpoint is where the receiver listens.\n" +
			"\tEndpoint string `mapstructure:\"endpoint\"`\n" +
			"\tTimeout  int    `mapstructure:\"timeout\"`\n" +
			"}\n",
		// mdatagen's sample components live under cmd/ and are not declarable.
		"cmd/mdatagen/metadata.yaml": "type: sample\nstatus:\n  class: receiver\n",
	})

	// A component with no metadata.yaml at all, which is what a private one
	// usually looks like: it is still recorded, from what the manifest says.
	private := module(t, modules, "example.com/private/exporter/vendorexporter", map[string]string{
		"config.go": "package vendorexporter\n\ntype Config struct {\n" +
			"\tToken string `mapstructure:\"token\"`\n}\n",
	})

	manifest := filepath.Join(modules, "manifest.yaml")
	writeFile(t, manifest, fmt.Sprintf(`
dist:
  module: example.com/collector/distribution
  name: otelcol-custom
  otelcol_version: 0.157.0
receivers:
  - gomod: example.com/collector/receiver/otlpreceiver v0.0.0
exporters:
  - gomod: example.com/private/exporter/vendorexporter v0.0.0
replaces:
  - example.com/collector/receiver/otlpreceiver => %s
  - example.com/private/exporter/vendorexporter => %s
`, otlp, private))

	writeFile(t, filepath.Join(overlays, "otlp.yaml"), `
kind: receiver
type: otlp
fields:
  type: map
  children:
    protocols: {type: map}
`)

	code, stdout, stderr := run(t, "--builder", "custom="+manifest,
		"--registry", out, "--cache", t.TempDir(), "--overlays", overlays)

	require.Equal(t, schemagen.ExitOK, code, "run failed: %s", stderr)
	assert.Contains(t, stderr, "loaded 1 field overlay(s)")
	assert.Empty(t, stdout, "the registry form has nothing to put on stdout")

	// The release comes from the manifest; the distribution is what --builder
	// filed it under, which is "custom" and not the manifest's own name.
	written := readFile(t, filepath.Join(out, "custom", "v0.157.0.yaml"))
	assert.Contains(t, written, "distribution: custom")
	assert.NotContains(t, written, "distribution: otelcol-custom")
	assert.Contains(t, written, "collectorVersion: v0.157.0")

	assert.Contains(t, written, "otlp:", "the schema is missing the declared receiver")
	assert.Contains(t, written, "protocols:", "the otlp receiver did not take the overlay's fields")
	assert.NotContains(t, written, "sample:", "the schema declared an mdatagen fixture")

	// The private component has no metadata, so its type comes from the module
	// name and its fields from its Config struct.
	assert.Contains(t, written, "vendor:", "the private exporter was dropped for having no metadata")
	assert.Contains(t, written, "token:", "the private exporter took no fields from its Config struct")

	index := readFile(t, filepath.Join(out, "index.json"))
	assert.Contains(t, index, "custom")
	assert.Contains(t, index, "v0.157.0")
}

// TestWritesOneFile covers the single-manifest form: the schema goes where
// --out names, and to stdout when it names nothing.
func TestWritesOneFile(t *testing.T) {
	t.Parallel()

	modules := t.TempDir()
	manifest := singleComponentManifest(t, modules)
	dest := filepath.Join(t.TempDir(), "my-collector.json")

	code, _, stderr := run(t, "--builder", manifest, "--out", dest,
		"--cache", t.TempDir(), "--overlays", t.TempDir())

	require.Equal(t, schemagen.ExitOK, code, "run failed: %s", stderr)

	// The name says JSON, so JSON is what it holds.
	written := readFile(t, dest)
	assert.Contains(t, written, `"otlp"`)
	assert.Contains(t, written, `"collectorVersion": "v0.157.0"`)

	// With no --out at all the schema is a stream, the way a filter writes, so
	// stdout holds the schema and nothing else.
	code, stdout, stderr := run(t, "--builder", manifest,
		"--cache", t.TempDir(), "--overlays", t.TempDir())

	require.Equal(t, schemagen.ExitOK, code, "run failed: %s", stderr)
	assert.True(t, strings.HasPrefix(stdout, "collectorVersion: v0.157.0"),
		"stdout is not the schema alone:\n%s", stdout)
}

// TestTwoDestinations covers --out and --registry given together, which name
// two different places for the same schema.
func TestTwoDestinations(t *testing.T) {
	t.Parallel()

	code, _, stderr := run(t, "--builder", "manifest.yaml",
		"--out", filepath.Join(t.TempDir(), "schema.yaml"), "--registry", t.TempDir())

	assert.Equal(t, schemagen.ExitUsage, code)
	assert.Contains(t, stderr, schemagen.ErrTwoOutputs.Error())
}

// TestManyManifestsNeedRegistry covers several manifests written to one file,
// which cannot hold them all.
func TestManyManifestsNeedRegistry(t *testing.T) {
	t.Parallel()

	code, _, stderr := run(t, "--builder", "a.yaml", "--builder", "b.yaml",
		"--out", filepath.Join(t.TempDir(), "schema.yaml"))

	assert.Equal(t, schemagen.ExitUsage, code)
	assert.Contains(t, stderr, schemagen.ErrManyToOneFile.Error())
}

// singleComponentManifest writes a manifest with one locally replaced
// component, which is all a destination test needs.
func singleComponentManifest(t *testing.T, root string) string {
	t.Helper()

	dir := module(t, root, "example.com/collector/receiver/otlpreceiver", map[string]string{
		"metadata.yaml": "type: otlp\nstatus:\n  class: receiver\n" +
			"  stability:\n    beta: [traces]\n",
	})

	path := filepath.Join(root, "manifest.yaml")
	writeFile(t, path, fmt.Sprintf(`
dist:
  name: custom
  otelcol_version: 0.157.0
receivers:
  - gomod: example.com/collector/receiver/otlpreceiver v0.0.0
replaces:
  - example.com/collector/receiver/otlpreceiver => %s
`, dir))

	return path
}

// module writes a self-contained Go module under root and returns its
// directory, so a manifest can replace the module path with it.
func module(t *testing.T, root, path string, files map[string]string) string {
	t.Helper()

	dir := filepath.Join(root, strings.ReplaceAll(path, "/", "_"))
	require.NoError(t, os.MkdirAll(dir, 0o750))

	writeFile(t, filepath.Join(dir, "go.mod"), "module "+path+"\n\ngo 1.24\n")

	for name, body := range files {
		dest := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(dest), 0o750))
		writeFile(t, dest, body)
	}

	return dir
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
