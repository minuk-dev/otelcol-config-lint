package schemagen

import (
	"path/filepath"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

func TestScanModuleUsesInjectedFS(t *testing.T) {
	t.Parallel()

	fsys := afero.NewMemMapFs()
	dir := filepath.Join(t.TempDir(), "module")

	for name, body := range map[string]string{
		"metadata.yaml":               "type: custom\n",
		"internal/config.schema.yaml": "type: object\n",
		"config.go":                   "package custom\ntype Config struct { Endpoint string `mapstructure:\"endpoint\"` }\n",
		"testdata/metadata.yaml":      "type: ignored\n",
	} {
		path := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, fsys.MkdirAll(filepath.Dir(path), dirPerm))
		require.NoError(t, afero.WriteFile(fsys, path, []byte(body), filePerm))
	}

	opts := &options{fsys: fsys}
	set, index, metas := newSchemaSet(), newGoIndex(), map[string]metadata{}
	mod := resolvedModule{Path: "example.com/receiver", Version: "v0.0.0", Dir: dir, Main: false, Error: nil}
	opts.scanModule(mod, set, index, metas)
	require.Equal(t, "custom", metas[mod.Path].Type)
	require.Len(t, metas, 1)
	require.Equal(t, 1, set.count())

	cat := &schema.Schema{Components: map[config.Kind]map[string]*schema.Component{
		config.KindReceiver: {"custom": {Type: "custom", Module: mod.Path}},
	}}
	require.Equal(t, 1, attachSourceFields(cat, index))
	require.Contains(t, cat.Components[config.KindReceiver]["custom"].Fields.Children, "endpoint")

	_, ok := opts.readLimited(filepath.Join(dir, "metadata.yaml"), 1)
	require.False(t, ok)
	_, ok = opts.readLimited(filepath.Join(dir, "missing.yaml"), maxMetadataBytes)
	require.False(t, ok)
}
