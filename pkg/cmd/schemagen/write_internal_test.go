package schemagen

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

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
