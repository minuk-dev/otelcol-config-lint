package schemagen

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
)

func TestRootFSConfinement(t *testing.T) {
	t.Parallel()

	for name, fsys := range map[string]afero.Fs{"default": nil, "osfs": afero.NewOsFs()} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			base := t.TempDir()
			victim := filepath.Join(base, "victim")
			require.NoError(t, os.WriteFile(victim, []byte("sentinel"), filePerm))

			opts := &options{fsys: fsys}
			root, closeRoot, err := opts.createRoot(filepath.Join(base, "root"))
			require.NoError(t, err)

			defer closeRoot()

			require.NoError(t, afero.WriteFile(root, "inside", []byte("schema"), filePerm))
			raw, err := afero.ReadFile(root, "inside")
			require.NoError(t, err)
			require.Equal(t, "schema", string(raw))

			require.NoError(t, os.Symlink("../victim", filepath.Join(base, "root", "escape")))

			_, err = root.Create("escape")
			require.Error(t, err)
			_, err = root.Open("escape")
			require.Error(t, err)
			require.Error(t, afero.WriteFile(root, "escape", []byte("overwrite"), filePerm))

			raw, err = os.ReadFile(victim)
			require.NoError(t, err)
			require.Equal(t, "sentinel", string(raw))
		})
	}
}
