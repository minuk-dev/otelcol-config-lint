//go:build integration

package otelcolconfiglint_test

import (
	"cmp"
	"context"
	"crypto/sha256"
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

	binary, version, distribution := collectorTarget(t)

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
	t.Logf("fixture=%s\n%s", path, dynamic)

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
			cmd.Dir = t.TempDir()
			cmd.Env = []string{
				"MY_POD_IP=127.0.0.1", "RECEIVER_ID=" + tt.receiver,
				"LIMITER_ID=memory_limiter", "BATCH_ID=batch", "EXPORTER_ID=debug", "EXTENSION_ID=zpages",
			}
			output, err := cmd.CombinedOutput()
			t.Logf("Collector environment=%v validate: err=%v\n%s", cmd.Env, err, output)

			require.NoError(t, ctx.Err(), "collector validation timed out")

			if tt.valid {
				require.NoError(t, err, "%s", output)
			} else {
				require.Error(t, err, "%s", output)
				assert.Contains(t, string(output), tt.receiver)
			}

			// Static linting stays unresolved regardless of the provider's runtime value.
			t.Log("explained difference: static linting does not resolve environment provider references")
			code, stdout, stderr := lint(t, dynamic,
				"--no-config", "--collector-version", version, "--distribution", distribution,
				"--min-severity", "warning", "-")
			t.Logf("linter: exit=%d\n%s\n%s", code, stdout, stderr)
			assert.Equal(t, 0, code, "%s\n%s", stdout, stderr)
			assert.Empty(t, stdout)
			assert.Empty(t, stderr)
		})
	}
}

// collectorTarget verifies the binary identity and records the exact schema bytes used by both tests.
func collectorTarget(t *testing.T) (string, string, string) {
	t.Helper()

	binary := os.Getenv("OTELCOL_BINARY")
	require.NotEmpty(t, binary, "set OTELCOL_BINARY to an official Collector binary")

	var err error

	binary, err = filepath.Abs(binary)
	require.NoError(t, err)

	version := cmp.Or(os.Getenv("OTELCOL_VERSION"), "v0.157.0")
	distribution := cmp.Or(os.Getenv("OTELCOL_DISTRIBUTION"), "core")

	require.Contains(t, []string{"v0.110.0", "v0.157.0"}, version)
	require.Contains(t, []string{"core", "contrib"}, distribution)

	name := "otelcol"
	if distribution == "contrib" {
		name = "otelcol-contrib"
	}

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	//nolint:gosec // the developer selects the Collector binary for this integration test
	cmd := exec.CommandContext(ctx, binary, "--version")
	cmd.Dir = t.TempDir()
	cmd.Env = []string{}
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", output)
	require.Equal(t, []string{name, "version", strings.TrimPrefix(version, "v")}, strings.Fields(string(output)))

	path := filepath.Join(repoSchemas, distribution, version+".json")
	//nolint:gosec // version and distribution are restricted to the matrix allowlists above
	src, err := os.ReadFile(path)
	require.NoError(t, err)
	t.Logf("binary=%s identity=%s distribution=%s schema=%s sha256=%x",
		binary, strings.TrimSpace(string(output)), distribution, path, sha256.Sum256(src))

	return binary, version, distribution
}

func TestCollectorCompatibility(t *testing.T) {
	t.Parallel()

	binary, version, distribution := collectorTarget(t)

	for _, tt := range []struct {
		name, distribution, rejection string
		collectorCode, lintCode       int
	}{
		{name: "null-defaults", distribution: "", rejection: "", collectorCode: 0, lintCode: 0},
		{name: "named-identifiers", distribution: "", rejection: "", collectorCode: 0, lintCode: 0},
		{name: "custom-verbosity", distribution: "", rejection: "", collectorCode: 0, lintCode: 0},
		{name: "environment-endpoint", distribution: "", rejection: "", collectorCode: 0, lintCode: 0},
		{name: "invalid-identifier", distribution: "", rejection: "part after /", collectorCode: 1, lintCode: 1},
		{name: "undefined-reference", distribution: "", rejection: "missing", collectorCode: 1, lintCode: 1},
		{name: "reference-shape", distribution: "", rejection: "receivers", collectorCode: 1, lintCode: 1},
		{name: "invalid-duration", distribution: "", rejection: "timeout", collectorCode: 1, lintCode: 1},
		{name: "invalid-verbosity", distribution: "", rejection: "verbosity", collectorCode: 1, lintCode: 1},
		{name: "prometheus-custom-config", distribution: "contrib", rejection: "", collectorCode: 0, lintCode: 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.distribution != "" && tt.distribution != distribution {
				t.Skip("fixture requires " + tt.distribution)
			}

			path, err := filepath.Abs(filepath.Join("../../../testdata/compatibility", tt.name+".yaml"))
			require.NoError(t, err)

			src, err := os.ReadFile(path)
			require.NoError(t, err)
			t.Logf("version=%s distribution=%s fixture=%s\n%s", version, distribution, path, src)

			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()

			//nolint:gosec // the developer selects the Collector binary for this integration test
			cmd := exec.CommandContext(ctx, binary, "validate", "--config", path)
			cmd.Dir = t.TempDir()
			cmd.Env = []string{"COMPAT_ENDPOINT=127.0.0.1:4317"}
			output, err := cmd.CombinedOutput()
			t.Logf("Collector environment=%v validate: err=%v\n%s", cmd.Env, err, output)
			require.NoError(t, ctx.Err(), "collector validation timed out")

			collectorCode := 0

			if err != nil {
				var exitErr *exec.ExitError
				require.ErrorAs(t, err, &exitErr, "Collector failed to execute: %v", err)
				collectorCode = exitErr.ExitCode()
			}

			code, stdout, stderr := lint(t, "", "--no-config", "--strict", "--output", "json",
				"--collector-version", version, "--distribution", distribution, "--fail-on", "error", path)
			t.Logf("linter: exit=%d\n%s\n%s", code, stdout, stderr)
			assert.Equal(t, tt.collectorCode, collectorCode, "unexpected Collector acceptance/rejection")
			assert.Equal(t, tt.lintCode, code, "unexpected linter acceptance/rejection")
			assert.Empty(t, stderr)

			if tt.rejection != "" {
				assert.Contains(t, string(output), tt.rejection)
			}

			if tt.name == "null-defaults" {
				assert.Contains(t, stdout, "missing-memory-limiter", "best-practice warnings must not reject a valid config")
			}
		})
	}
}
