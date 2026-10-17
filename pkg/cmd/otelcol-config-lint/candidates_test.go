package otelcolconfiglint_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDirectoryCandidatesExcludeCertificates(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "nested"), 0o750))

	for _, name := range []string{"collector.yaml", "collector.yml", "nested/collector.YAML"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("receivers: {}"), 0o600))
	}

	for _, name := range []string{"beta-kafka-ca.pem", "nested/root.crt", "notes.txt", "config.json"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("not a config"), 0o600))
	}

	code, out, errOut := lint(t, "", "--no-config", "--default=none", "--verbose", "--summary", dir)
	require.Equal(t, 0, code, errOut)
	assert.Contains(t, out, "3 file(s) checked")
	assert.NotContains(t, out, "beta-kafka-ca.pem")
	assert.NotContains(t, out, "root.crt")
	code, out, errOut = lint(t, "", "--no-config", "--default=none", filepath.Join(dir, "beta-kafka-ca.pem"))
	assert.Equal(t, 1, code, errOut)
	assert.Contains(t, out, "beta-kafka-ca.pem")
	assert.Contains(t, out, "config root must be a mapping")
}
