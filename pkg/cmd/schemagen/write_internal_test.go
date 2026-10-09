package schemagen

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

func TestWriteRegistryRejectsInvalidIdentity(t *testing.T) {
	t.Parallel()

	for _, field := range []string{"name", "version"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()

			//nolint:exhaustruct // only the identity is needed for rejection
			cat := &schema.Schema{Distribution: "custom", CollectorVersion: "v0.157.0"}
			if field == "name" {
				cat.Distribution = "../victim"
			} else {
				cat.CollectorVersion = "../../victim"
			}

			//nolint:exhaustruct // only the registry is used before rejection
			opts := &options{registryDir: filepath.Join(t.TempDir(), "registry")}
			require.Error(t, opts.writeRegistry(cat, []schema.Format{schema.YAML}))
			require.NoDirExists(t, opts.registryDir)
		})
	}
}

func TestRegistryRejectsEscapingSymlinks(t *testing.T) {
	t.Parallel()

	for _, target := range []string{"custom", "custom/v0.157.0.yaml", schema.IndexFile, schema.ComponentsFile} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			victim := filepath.Join(root, "victim")
			require.NoError(t, os.Mkdir(victim, dirPerm))
			sentinel := filepath.Join(victim, "v0.157.0.yaml")
			require.NoError(t, os.WriteFile(sentinel, []byte("sentinel"), filePerm))

			registry := filepath.Join(root, "registry")
			link := filepath.Join(registry, filepath.FromSlash(target))
			require.NoError(t, os.MkdirAll(filepath.Dir(link), dirPerm))

			dest := sentinel
			if target == "custom" {
				dest = victim
			}

			rel, err := filepath.Rel(filepath.Dir(link), dest)
			require.NoError(t, err)
			require.NoError(t, os.Symlink(rel, link))

			//nolint:exhaustruct // these tests only need the registry and log stream
			opts := &options{registryDir: registry, progress: io.Discard}
			//nolint:exhaustruct // the schema only needs an identity for writing
			cat := &schema.Schema{Distribution: "custom", CollectorVersion: "v0.157.0"}

			switch target {
			case schema.IndexFile:
				_, err = opts.writeIndex()
			case schema.ComponentsFile:
				err = opts.writeComponents(&schema.Index{Distributions: map[string][]string{}, Extensions: map[string]string{}})
			default:
				err = opts.writeRegistry(cat, []schema.Format{schema.YAML})
			}

			require.Error(t, err)
			raw, err := os.ReadFile(sentinel)
			require.NoError(t, err)
			require.Equal(t, "sentinel", string(raw))
		})
	}
}

func TestRegistryUsesInjectedFS(t *testing.T) {
	t.Parallel()

	fsys := afero.NewMemMapFs()
	base := t.TempDir()
	opts := &options{fsys: fsys, registryDir: filepath.Join(base, "registry"),
		summaryFile: filepath.Join(base, "summary.md"), progress: io.Discard, retain: 2}
	cat := &schema.Schema{Distribution: "custom", Components: map[config.Kind]map[string]*schema.Component{
		config.KindReceiver: {"otlp": {Type: "otlp"}},
	}}

	for _, version := range []string{"v0.155.0", "v0.156.0", "v0.157.0"} {
		cat.CollectorVersion = version
		require.NoError(t, opts.writeRegistry(cat, []schema.Format{schema.JSON, schema.YAML}))
	}

	opts.summarise(cat)
	require.NoError(t, opts.writeSummary())
	summary, err := afero.ReadFile(fsys, opts.summaryFile)
	require.NoError(t, err)
	require.Contains(t, string(summary), "v0.157.0` regenerated")
	require.NotNil(t, opts.previousIn(filepath.Join(opts.registryDir, "custom"), "v0.157.0"))
	require.NoError(t, opts.publishRegistry())
	idx, err := schema.ReadIndexFileFS(fsys, filepath.Join(opts.registryDir, schema.IndexFile))
	require.NoError(t, err)
	require.Equal(t, []string{"v0.157.0", "v0.156.0"}, idx.Versions("custom"))
	ext, ok := idx.Extension("custom")
	require.True(t, ok)
	require.Equal(t, ".yaml", ext)

	comps, err := schema.ReadComponentsFileFS(fsys, filepath.Join(opts.registryDir, schema.ComponentsFile))
	require.NoError(t, err)
	require.True(t, comps.Has("custom"))

	for _, ext := range []string{".yaml", ".json"} {
		_, err := fsys.Stat(filepath.Join(opts.registryDir, "custom", "v0.155.0"+ext))
		require.ErrorIs(t, err, os.ErrNotExist)
	}

	opts.outFile = filepath.Join(base, "standalone.json")
	require.NoError(t, opts.writeFile(cat, []schema.Format{schema.JSON}))
	saved, err := schema.ReadFileFS(fsys, opts.outFile)
	require.NoError(t, err)
	require.Equal(t, cat.CollectorVersion, saved.CollectorVersion)
	require.NoFileExists(t, opts.outFile)
	require.NoDirExists(t, opts.registryDir)

	opts.fsys = afero.NewReadOnlyFs(fsys)
	require.ErrorIs(t, opts.writeRegistry(cat, []schema.Format{schema.JSON}), os.ErrPermission)
	require.ErrorIs(t, opts.writeFile(cat, []schema.Format{schema.JSON}), os.ErrPermission)
	require.ErrorIs(t, opts.writeSummary(), os.ErrPermission)
}
