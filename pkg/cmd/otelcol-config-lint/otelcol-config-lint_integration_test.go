//go:build integration

package otelcolconfiglint_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDynamicServiceReferencesCollectorCompatibility(t *testing.T) {
	t.Parallel()

	binary := os.Getenv("OTELCOL_BINARY")
	require.NotEmpty(t, binary, "set OTELCOL_BINARY to the official otelcol v0.157.0 binary")

	versionCtx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	t.Cleanup(cancel)

	//nolint:gosec // the developer selects the Collector binary for this integration test
	version, err := exec.CommandContext(versionCtx, binary, "--version").CombinedOutput()
	require.NoError(t, err, "%s", version)
	require.Contains(t, string(version), "0.157.0")

	src, err := os.ReadFile(filepath.Join(validConfig, "agent.yaml"))
	require.NoError(t, err)

	dynamic := strings.NewReplacer(
		"receivers: [otlp]", `receivers: ["${env:RECEIVER_ID}"]`,
		"processors: [memory_limiter, batch]", `processors: ["${env:LIMITER_ID}", "${env:BATCH_ID}"]`,
		"exporters: [debug]", `exporters: ["${env:EXPORTER_ID}"]`,
		"extensions: [zpages]", `extensions: ["${env:EXTENSION_ID}"]`,
	).Replace(string(src))
	path := filepath.Join(t.TempDir(), "dynamic.yaml")
	//nolint:gosec // the generated fixture is written only inside t.TempDir
	require.NoError(t, os.WriteFile(path, []byte(dynamic), 0o600))

	for _, tt := range []struct {
		name, receiver string
		valid          bool
	}{
		{name: "resolves to declared components", receiver: "otlp", valid: true},
		{name: "runtime selects an undeclared component", receiver: "missing", valid: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()

			//nolint:gosec // the developer selects the Collector binary for this integration test
			cmd := exec.CommandContext(ctx, binary, "validate", "--config", path)
			cmd.Env = []string{
				"MY_POD_IP=127.0.0.1", "RECEIVER_ID=" + tt.receiver,
				"LIMITER_ID=memory_limiter", "BATCH_ID=batch", "EXPORTER_ID=debug", "EXTENSION_ID=zpages",
			}
			output, err := cmd.CombinedOutput()

			require.NoError(t, ctx.Err(), "collector validation timed out")

			if tt.valid {
				require.NoError(t, err, "%s", output)
			} else {
				require.Error(t, err, "%s", output)
				assert.Contains(t, string(output), tt.receiver)
			}

			// Static linting stays unresolved regardless of the provider's runtime value.
			code, stdout, stderr := lint(t, dynamic,
				"--collector-version", "v0.157.0", "--distribution", "core", "--min-severity", "warning", "-")
			assert.Equal(t, 0, code, "%s\n%s", stdout, stderr)
			assert.Empty(t, stdout)
			assert.Empty(t, stderr)
		})
	}
}
