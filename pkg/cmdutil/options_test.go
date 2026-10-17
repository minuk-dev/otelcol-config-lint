package cmdutil_test

import (
	"path/filepath"
	"testing"

	"github.com/spf13/afero"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/cmdutil"
)

func TestSettingsSchemaLocations(t *testing.T) {
	t.Parallel()

	for _, prefix := range []string{"run:\n  ", ""} {
		t.Run(prefix, func(t *testing.T) {
			t.Parallel()

			fileSystem := afero.NewMemMapFs()
			path := filepath.Join(t.TempDir(), "project", ".otelcol-config-lint.yaml")
			absoluteLocation := filepath.Join(t.TempDir(), "schema.yaml")
			require.NoError(t, fileSystem.MkdirAll(filepath.Dir(path), 0o755))
			require.NoError(t, afero.WriteFile(fileSystem, path, []byte(prefix+`schemaLocations:
    - ../schemas/{{.Distribution}}/{{.Version}}.yaml
    - default
    - https://example.com/{{.Version}}.yaml
    - '`+absoluteLocation+`'
`), 0o600))
			opts := &cmdutil.GlobalOptions{Fs: fileSystem}
			cmd := &cobra.Command{}
			opts.RegisterFlags(cmd)
			require.NoError(t, cmd.Flags().Set("config", path))
			require.NoError(t, opts.Prepare(cmd))

			expected := []string{
				filepath.Join(filepath.Dir(path), "../schemas/{{.Distribution}}/{{.Version}}.yaml"),
				"default", "https://example.com/{{.Version}}.yaml", absoluteLocation,
			}
			assert.Equal(t, expected, opts.Settings().Run.SchemaLocations)
			require.NoError(t, opts.Prepare(cmd))
			assert.Equal(t, expected, opts.Settings().Run.SchemaLocations, "prepare is idempotent")

			var locations []string
			cmd.Flags().StringSliceVar(&locations, "schema-location", nil, "schema")
			require.NoError(t, cmd.Flags().Set("schema-location", "../cli/schema.yaml"))
			opts.Fold(cmd).List("schema-location", &locations, opts.Settings().Run.SchemaLocations)
			assert.Equal(t, []string{"../cli/schema.yaml"}, locations)
		})
	}
}
