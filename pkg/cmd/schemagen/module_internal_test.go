package schemagen

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkspaceRejectsInvalidIdentity(t *testing.T) {
	t.Parallel()

	for _, field := range []string{"name", "version"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()

			var man manifest

			man.Dist.Name = "custom"
			man.Dist.Version = "0.157.0"

			if field == "name" {
				man.Dist.Name = "../../victim"
			} else {
				man.Dist.Version = "../victim"
			}

			//nolint:exhaustruct // only the cache is used by workspace
			opts := &options{cacheDir: filepath.Join(t.TempDir(), "cache")}
			_, err := opts.workspace(&man)
			require.Error(t, err)
			require.NoDirExists(t, opts.cacheDir)
		})
	}
}

func TestWorkspaceRejectsEscapingSymlinks(t *testing.T) {
	t.Parallel()

	for _, target := range []string{
		"workspace", "workspace/custom", "workspace/custom/go.mod", "workspace/custom/components.go",
	} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			victim := filepath.Join(root, "victim")
			require.NoError(t, os.Mkdir(victim, dirPerm))

			for _, name := range []string{"go.mod", "components.go"} {
				require.NoError(t, os.WriteFile(filepath.Join(victim, name), []byte("sentinel"), filePerm))
			}

			cache := filepath.Join(root, "cache")
			link := filepath.Join(cache, filepath.FromSlash(target))
			require.NoError(t, os.MkdirAll(filepath.Dir(link), dirPerm))

			dest := victim
			if filepath.Ext(link) != "" {
				dest = filepath.Join(victim, filepath.Base(link))
			}

			rel, err := filepath.Rel(filepath.Dir(link), dest)
			require.NoError(t, err)
			require.NoError(t, os.Symlink(rel, link))

			var man manifest

			man.Dist.Name = "custom"
			man.Dist.Version = "0.157.0"
			//nolint:exhaustruct // only the cache is used by workspace
			opts := &options{cacheDir: cache}
			_, err = opts.workspace(&man)
			require.Error(t, err)

			for _, name := range []string{"go.mod", "components.go"} {
				raw, readErr := os.ReadFile(filepath.Join(victim, name))
				require.NoError(t, readErr)
				require.Equal(t, "sentinel", string(raw))
			}
		})
	}
}
