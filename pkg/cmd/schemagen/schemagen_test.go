package schemagen_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/cmd/schemagen"
	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// run executes the command and returns its exit code and streams. The wiring
// mirrors cmd/schemagen/main.go, so what the tests assert on is what the binary
// does.
func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()

	// Everything that generates goes through the subcommand that generates.
	return runCommand(t, append([]string{"generate"}, args...)...)
}

// runCommand executes the root with exactly the arguments given, for the tests
// that are about the root itself rather than about generating.
func runCommand(t *testing.T, args ...string) (int, string, string) {
	t.Helper()

	var stdout, stderr bytes.Buffer

	cmd := schemagen.NewCommand()
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
		"nothing written":  {in: schemagen.ErrManyToOneFile, want: schemagen.ExitUsage},
		"nothing to diff":  {in: schemagen.ErrNoPrevious, want: schemagen.ExitUsage},
		"nothing to prune": {in: schemagen.ErrNothingToPrune, want: schemagen.ExitUsage},
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
		"--registry", out, "--cache", t.TempDir())

	assert.Equal(t, schemagen.ExitUsage, code)
	assert.Contains(t, stderr, `unknown schema format "toml"`)

	entries, err := os.ReadDir(out)
	require.NoError(t, err)
	assert.Empty(t, entries, "a refused format still wrote to the registry")
}

// TestIncompleteManifest covers the manifest checks, and what a run that
// produced nothing reports: skipping is what keeps one bad manifest from
// discarding the others, not a way to succeed at nothing.
func TestIncompleteManifest(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yaml")
	writeFile(t, path, "dist:\n  otelcol_version: 0.157.0\n")

	code, _, stderr := run(t, "--builder", path,
		"--registry", t.TempDir(), "--cache", t.TempDir())

	assert.Equal(t, schemagen.ExitFailure, code)
	assert.Contains(t, stderr, "dist.name is required")
	assert.Contains(t, stderr, "1 of 1 manifests could not be generated")
}

// TestSkipsOneOfSeveral covers the other half: a manifest that cannot be read
// does not discard the distributions that could, and the run still fails, so a
// registry is never published a distribution short.
func TestSkipsOneOfSeveral(t *testing.T) {
	t.Parallel()

	modules, registry := t.TempDir(), t.TempDir()
	good := singleComponentManifest(t, modules)
	bad := filepath.Join(t.TempDir(), "broken.yaml")
	writeFile(t, bad, "dist:\n  name: broken\n")

	code, _, stderr := run(t, "--builder", bad, "--builder", good,
		"--registry", registry, "--cache", t.TempDir())

	assert.Equal(t, schemagen.ExitFailure, code)
	assert.Contains(t, stderr, "1 of 2 manifests could not be generated")

	// The one that could be read is written all the same, and the index lists
	// it, so the failure costs only the distribution that failed.
	assert.FileExists(t, filepath.Join(registry, "custom", "v0.157.0.yaml"))
	assert.Contains(t, readFile(t, filepath.Join(registry, "index.json")), "custom")
}

// TestRelativeReplacement covers a manifest whose replacement is a path rather
// than a module, which is how a distribution under development points at a
// checkout. The workspace the go command runs in is somewhere else entirely, so
// the path has to be carried over rather than copied.
//
// What it is carried over from is the builder's output directory, not the
// manifest: upstream's own contrib manifest reaches its replacement with
// "../../../internal/obi-src", which is three levels up from
// "distributions/otelcol-contrib/_build" and only two from the manifest itself.
func TestRelativeReplacement(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		outputPath string
		// depth is how far back up the manifest has to reach to name the
		// module directory, counted from wherever the base turns out to be.
		depth string
	}{
		"against the manifest":  {outputPath: "", depth: "."},
		"against output_path":   {outputPath: "./_build", depth: ".."},
		"against a nested path": {outputPath: "./build/dist", depth: "../.."},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			dir := module(t, root, "example.com/collector/receiver/otlpreceiver", map[string]string{
				"metadata.yaml": "type: otlp\nstatus:\n  class: receiver\n" +
					"  stability:\n    beta: [traces]\n",
			})

			output := ""
			if tt.outputPath != "" {
				output = "\n  output_path: " + tt.outputPath
			}

			path := filepath.Join(root, "manifest.yaml")
			writeFile(t, path, fmt.Sprintf(`
dist:
  name: custom
  otelcol_version: 0.157.0%s
receivers:
  - gomod: example.com/collector/receiver/otlpreceiver v0.0.0
replaces:
  - example.com/collector/receiver/otlpreceiver => %s/%s
`, output, tt.depth, filepath.Base(dir)))

			code, stdout, stderr := run(t, "--builder", path, "--cache", t.TempDir())

			require.Equal(t, schemagen.ExitOK, code, "run failed: %s", stderr)
			assert.Contains(t, stdout, "otlp:")
		})
	}
}

// TestCollectorVersion covers where the release a schema is filed under comes
// from. Upstream's own release manifests carry a dist.version that lags the tag
// -- 0.149.0 at the v0.150.0 tag -- so what the collector's components are
// pinned at decides, and dist.otelcol_version overrides everything when set.
func TestCollectorVersion(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		dist string
		want string
	}{
		"pinned components win over a lagging dist.version": {
			dist: "  name: custom\n  version: 0.149.0",
			want: "collectorVersion: v0.150.0",
		},
		"otelcol_version wins over both": {
			dist: "  name: custom\n  version: 0.149.0\n  otelcol_version: 0.157.0",
			want: "collectorVersion: v0.157.0",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			dir := module(t, root, "go.opentelemetry.io/collector/receiver/otlpreceiver", map[string]string{
				"metadata.yaml": "type: otlp\nstatus:\n  class: receiver\n" +
					"  stability:\n    beta: [traces]\n",
			})

			path := filepath.Join(root, "manifest.yaml")
			writeFile(t, path, fmt.Sprintf(`
dist:
%s
receivers:
  - gomod: go.opentelemetry.io/collector/receiver/otlpreceiver v0.150.0
replaces:
  - go.opentelemetry.io/collector/receiver/otlpreceiver => %s
`, tt.dist, dir))

			code, stdout, stderr := run(t, "--builder", path, "--cache", t.TempDir())

			require.Equal(t, schemagen.ExitOK, code, "run failed: %s", stderr)
			assert.Contains(t, stdout, tt.want)
		})
	}
}

// TestTextualType covers a setting whose Go type is a number but whose config
// spelling is a word: configtelemetry.Level is an int32 with an UnmarshalText
// method, and the debug exporter's "verbosity: detailed" is not an integer.
func TestTextualType(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	level := module(t, root, "example.com/collector/config/configtelemetry", map[string]string{
		"level.go": "package configtelemetry\n\ntype Level int32\n\n" +
			"func (l *Level) UnmarshalText(text []byte) error { return nil }\n",
	})
	exporter := module(t, root, "example.com/collector/exporter/debugexporter", map[string]string{
		"metadata.yaml": "type: debug\nstatus:\n  class: exporter\n" +
			"  stability:\n    alpha: [traces]\n",
		"config.go": "package debugexporter\n\n" +
			"import \"example.com/collector/config/configtelemetry\"\n\n" +
			"type Config struct {\n" +
			"\tVerbosity configtelemetry.Level `mapstructure:\"verbosity\"`\n}\n",
	})

	path := filepath.Join(root, "manifest.yaml")
	writeFile(t, path, fmt.Sprintf(`
dist:
  name: custom
  otelcol_version: 0.157.0
exporters:
  - gomod: example.com/collector/exporter/debugexporter v0.0.0
replaces:
  - example.com/collector/exporter/debugexporter => %s
  - example.com/collector/config/configtelemetry => %s
`, exporter, level))

	code, stdout, stderr := run(t, "--builder", path, "--cache", t.TempDir())

	require.Equal(t, schemagen.ExitOK, code, "run failed: %s", stderr)
	assert.Contains(t, stdout, "verbosity:")
	assert.NotContains(t, stdout, "type: int", "a text-decoded type was read as its underlying number")
}

// A setting that decodes into a component.ID names an extension, and that is
// the only thing that says so: a storage id and a directory path are both
// strings by the time they reach the schema. Marking it here is what lets
// undefined-extension-reference find a reference without carrying a list of
// the settings that hold one.
func TestExtensionReferencesAreMarked(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	component := module(t, root, "go.opentelemetry.io/collector/component", map[string]string{
		"identifiable.go": "package component\n\ntype ID struct{}\n\n" +
			"func (id *ID) UnmarshalText(text []byte) error { return nil }\n",
	})
	exporter := module(t, root, "example.com/collector/exporter/otlpexporter", map[string]string{
		"metadata.yaml": "type: otlp\nstatus:\n  class: exporter\n" +
			"  stability:\n    beta: [traces]\n",
		"config.go": "package otlpexporter\n\n" +
			"import \"go.opentelemetry.io/collector/component\"\n\n" +
			"type Config struct {\n" +
			"\tEndpoint string `mapstructure:\"endpoint\"`\n" +
			"\tQueue QueueConfig `mapstructure:\"sending_queue\"`\n" +
			"\tAuth AuthConfig `mapstructure:\"auth\"`\n}\n\n" +
			"type QueueConfig struct {\n" +
			"\tStorageID *component.ID `mapstructure:\"storage\"`\n}\n\n" +
			"type AuthConfig struct {\n" +
			"\tAuthenticatorID component.ID `mapstructure:\"authenticator\"`\n" +
			"\tWatcherID component.ID `mapstructure:\"watcher\"`\n}\n",
	})

	path := filepath.Join(root, "manifest.yaml")
	writeFile(t, path, fmt.Sprintf(`
dist:
  name: custom
  otelcol_version: 0.157.0
exporters:
  - gomod: example.com/collector/exporter/otlpexporter v0.0.0
replaces:
  - example.com/collector/exporter/otlpexporter => %s
  - go.opentelemetry.io/collector/component => %s
`, exporter, component))

	code, stdout, stderr := run(t, "--builder", path, "--cache", t.TempDir())

	require.Equal(t, schemagen.ExitOK, code, "run failed: %s", stderr)
	assert.Contains(t, stdout, "extensionRef: storage")
	assert.Contains(t, stdout, "extensionRef: auth")
	// A key the generator has no name for is still a reference. Saying so is
	// what keeps a setting upstream adds from going unchecked until someone
	// here notices it.
	assert.Contains(t, stdout, "extensionRef: extension")
	assert.Equal(t, 3, strings.Count(stdout, "extensionRef:"),
		"a plain string setting was marked as an extension reference")
}

// TestGenerate runs the whole pipeline against modules on disk: the manifest
// replaces every component with a local checkout, which is both what a
// distribution under development does and what keeps this test off the network.
func TestGenerate(t *testing.T) {
	t.Parallel()

	modules, out := t.TempDir(), t.TempDir()

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

	code, stdout, stderr := run(t, "--builder", "custom="+manifest,
		"--registry", out, "--cache", t.TempDir())

	require.Equal(t, schemagen.ExitOK, code, "run failed: %s", stderr)
	assert.Empty(t, stdout, "the registry form has nothing to put on stdout")

	// The release comes from the manifest; the distribution is what --builder
	// filed it under, which is "custom" and not the manifest's own name.
	written := readFile(t, filepath.Join(out, "custom", "v0.157.0.yaml"))
	assert.Contains(t, written, "distribution: custom")
	assert.NotContains(t, written, "distribution: otelcol-custom")
	assert.Contains(t, written, "collectorVersion: v0.157.0")

	assert.Contains(t, written, "otlp:", "the schema is missing the declared receiver")
	assert.Contains(t, written, "endpoint:", "the otlp receiver took no fields from its Config struct")
	assert.NotContains(t, written, "sample:", "the schema declared an mdatagen fixture")

	// The private component has no metadata, so its type comes from the module
	// name and its fields from its Config struct.
	assert.Contains(t, written, "vendor:", "the private exporter was dropped for having no metadata")
	assert.Contains(t, written, "token:", "the private exporter took no fields from its Config struct")

	index := readFile(t, filepath.Join(out, "index.json"))
	assert.Contains(t, index, "custom")
	assert.Contains(t, index, "v0.157.0")
	// Both forms are written, so the readable one is what a fetch should ask
	// for, and the index says so rather than leaving it to be probed.
	assert.Contains(t, index, `"extensions"`)
	assert.Contains(t, index, `".yaml"`)
}

// TestIndexNamesTheExtensionPublished covers the request the index saves: a
// registry publishing one form says which, so reading a schema from it over
// the network costs one request instead of a probe through three.
func TestIndexNamesTheExtensionPublished(t *testing.T) {
	t.Parallel()

	modules, out := t.TempDir(), t.TempDir()
	manifest := singleComponentManifest(t, modules)

	code, _, stderr := run(t, "--builder", "custom="+manifest, "--registry", out,
		"--formats", "json", "--cache", t.TempDir())
	require.Equal(t, schemagen.ExitOK, code, "run failed: %s", stderr)

	var idx schema.Index

	require.NoError(t, json.Unmarshal([]byte(readFile(t, filepath.Join(out, "index.json"))), &idx))

	ext, named := idx.Extension("custom")
	assert.True(t, named, "the index named no extension for a distribution published as JSON alone")
	assert.Equal(t, ".json", ext)

	// A release published in another form leaves the distribution with no
	// single answer, so the index names none and the probe stays the fallback.
	writeFile(t, filepath.Join(out, "custom", "v0.150.0.yaml"),
		"collectorVersion: v0.150.0\ndistribution: custom\ncomponents: {}\n")

	code, _, stderr = run(t, "--builder", "custom="+manifest, "--registry", out,
		"--formats", "json", "--cache", t.TempDir())
	require.Equal(t, schemagen.ExitOK, code, "run failed: %s", stderr)

	// Decoded into a fresh index: an absent "extensions" key leaves whatever
	// the previous decode put there, which is the value under test.
	var regenerated schema.Index

	require.NoError(t, json.Unmarshal([]byte(readFile(t, filepath.Join(out, "index.json"))), &regenerated))

	_, named = regenerated.Extension("custom")
	assert.False(t, named, "releases that disagree on a form should name none")
}

// TestPublishesComponentAvailability covers the document that answers "exists
// in v0.110.0 but not in v0.157.0". Without it the only way to know is to read
// every schema the registry serves, which against the published one is a
// multi-megabyte download per release to produce a single hint.
func TestPublishesComponentAvailability(t *testing.T) {
	t.Parallel()

	modules, out := t.TempDir(), t.TempDir()
	manifest := singleComponentManifest(t, modules)

	// An older release the registry already held, carrying a component this
	// one no longer ships.
	require.NoError(t, os.MkdirAll(filepath.Join(out, "custom"), 0o750))
	writeFile(t, filepath.Join(out, "custom", "v0.150.0.yaml"),
		"collectorVersion: v0.150.0\ndistribution: custom\ncomponents:\n"+
			"  receiver:\n    otlp:\n      type: otlp\n    logging:\n      type: logging\n")

	code, _, stderr := run(t, "--builder", "custom="+manifest, "--registry", out,
		"--formats", "yaml", "--cache", t.TempDir())
	require.Equal(t, schemagen.ExitOK, code, "run failed: %s", stderr)

	var comps schema.Components

	require.NoError(t, json.Unmarshal(
		[]byte(readFile(t, filepath.Join(out, schema.ComponentsFile))), &comps))

	spans := comps.Distributions["custom"][config.KindReceiver]

	// A component the newest release still ships ends its span open, so adding
	// a release does not rewrite the entry of everything it left alone.
	assert.Equal(t, []schema.Span{{From: "v0.150.0", To: ""}}, spans["otlp"])
	assert.Equal(t, []schema.Span{{From: "v0.150.0", To: "v0.150.0"}}, spans["logging"],
		"a dropped component should be named as ending where it ended")

	// Read back the way a linter reads it: the releases each component is in,
	// oldest first.
	avail := comps.Expand("custom", []string{"v0.157.0", "v0.150.0"})
	assert.Equal(t, []string{"v0.150.0", "v0.157.0"}, avail[config.KindReceiver]["otlp"])
	assert.Equal(t, []string{"v0.150.0"}, avail[config.KindReceiver]["logging"])
}

// TestAvailabilityLeavesOutWhatItCouldNotRead pins that a distribution is
// described from every release it serves or not at all: a release missing from
// the walk would read as a component dropped and added back, and a reader that
// finds no entry falls back to the schemas instead.
func TestAvailabilityLeavesOutWhatItCouldNotRead(t *testing.T) {
	t.Parallel()

	modules, out := t.TempDir(), t.TempDir()
	manifest := singleComponentManifest(t, modules)

	// A release the index will list and no schema can be read from.
	require.NoError(t, os.MkdirAll(filepath.Join(out, "custom"), 0o750))
	writeFile(t, filepath.Join(out, "custom", "v0.150.0.yaml"), "collectorVersion: v0.150.0\ncomponents: [\n")

	code, _, stderr := run(t, "--builder", "custom="+manifest, "--registry", out,
		"--formats", "yaml", "--cache", t.TempDir())
	require.Equal(t, schemagen.ExitOK, code, "run failed: %s", stderr)
	assert.Contains(t, stderr, "no availability published",
		"a distribution left out of the document should say so")

	var comps schema.Components

	require.NoError(t, json.Unmarshal(
		[]byte(readFile(t, filepath.Join(out, schema.ComponentsFile))), &comps))
	assert.False(t, comps.Has("custom"), "a half-read distribution should not be described")
}

// TestWritesOneFile covers the single-manifest form: the schema goes where
// --out names, and to stdout when it names nothing.
func TestWritesOneFile(t *testing.T) {
	t.Parallel()

	modules := t.TempDir()
	manifest := singleComponentManifest(t, modules)
	dest := filepath.Join(t.TempDir(), "my-collector.json")

	code, _, stderr := run(t, "--builder", manifest, "--out", dest,
		"--cache", t.TempDir())

	require.Equal(t, schemagen.ExitOK, code, "run failed: %s", stderr)

	// The name says JSON, so JSON is what it holds.
	written := readFile(t, dest)
	assert.Contains(t, written, `"otlp"`)
	assert.Contains(t, written, `"collectorVersion": "v0.157.0"`)

	// With no --out at all the schema is a stream, the way a filter writes, so
	// stdout holds the schema and nothing else.
	code, stdout, stderr := run(t, "--builder", manifest,
		"--cache", t.TempDir())

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

// TestPrintVersion covers --print-version, which is what a scheduled run asks
// so that it does not confuse a release tag with the release a manifest
// resolves to. Upstream's own manifests make the two differ.
func TestPrintVersion(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Shaped after the v0.146.0 tag, where every number in the manifest is a
	// different one: it calls itself 0.145.0 while pinning its components at
	// v0.146.1. What the components are pinned at is the release.
	path := filepath.Join(dir, "manifest.yaml")
	writeFile(t, path, "dist:\n  name: otelcol-contrib\n  version: 0.145.0\n"+
		"receivers:\n"+
		"  - gomod: go.opentelemetry.io/collector/receiver/otlpreceiver v0.146.1\n"+
		"  - gomod: go.opentelemetry.io/collector/receiver/nopreceiver v0.146.1\n")

	code, stdout, _ := runCommand(t, "print-version", "--builder", "contrib="+path)

	assert.Equal(t, schemagen.ExitOK, code)
	assert.Equal(t, "contrib\tv0.146.1\n", stdout)
}

// TestPrintVersionNeedsNoDestination covers the other half of why it is asked
// early: it writes no schema, so the flags describing where one would go are
// not required, and --registry is not one of them.
func TestPrintVersionNeedsNoDestination(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yaml")
	writeFile(t, path, "dist:\n  name: otelcol\n  otelcol_version: 0.158.0\n"+
		"receivers:\n  - gomod: go.opentelemetry.io/collector/receiver/otlpreceiver v0.158.0\n")

	code, stdout, stderr := runCommand(t, "print-version", "--builder", path)

	assert.Equal(t, schemagen.ExitOK, code)
	assert.Equal(t, "otelcol\tv0.158.0\n", stdout)
	assert.Empty(t, stderr)
}

// TestRootListsCommands covers the shape of the root: it does no work of its
// own, so a bare invocation prints the help that names the modes rather than
// trying to generate from nothing.
func TestRootListsCommands(t *testing.T) {
	t.Parallel()

	code, stdout, _ := runCommand(t)

	assert.Equal(t, schemagen.ExitOK, code)
	assert.Contains(t, stdout, "generate")
	assert.Contains(t, stdout, "print-version")
}

// TestPrintVersionNeedsAManifest covers print-version reporting the missing
// input itself: it does not share generate's flags, so it cannot share its
// checks either.
func TestPrintVersionNeedsAManifest(t *testing.T) {
	t.Parallel()

	code, _, stderr := runCommand(t, "print-version")

	assert.Equal(t, schemagen.ExitUsage, code)
	assert.Contains(t, stderr, schemagen.ErrNoManifests.Error())
	assert.Contains(t, stderr, "Usage:")
}
