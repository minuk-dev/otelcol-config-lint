//go:build integration

package smoke_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCLISmoke(t *testing.T) {
	t.Parallel()

	binary, image := os.Getenv("OTELCOL_LINT_BINARY"), os.Getenv("OTELCOL_LINT_IMAGE")
	require.NotEqual(t, binary == "", image == "", "set exactly one of OTELCOL_LINT_BINARY or OTELCOL_LINT_IMAGE")

	root := filepath.Join(t.TempDir(), "project with spaces")
	workdir := filepath.Join(root, "config files")
	//nolint:gosec // the published image's nonroot user must traverse the mounted fixture
	require.NoError(t, os.MkdirAll(filepath.Join(workdir, "nested configs"), 0o755))
	require.NoError(t, os.CopyFS(filepath.Join(root, "local schemas"), os.DirFS("../../testdata/schemas")))

	for dest, source := range map[string]string{
		"valid agent.yaml":                 "valid/agent.yaml",
		"nested configs/skip invalid.yaml": "invalid/pipeline.yaml",
	} {
		content, err := os.ReadFile(filepath.Join("../../testdata", source))
		require.NoError(t, err)
		//nolint:gosec // fixtures must be readable by the published image's nonroot user
		require.NoError(t, os.WriteFile(filepath.Join(workdir, dest), content, 0o644))
	}

	policy := `version: "1"
run:
  collectorVersion: v0.157.0
  distribution: contrib
  schemaLocations: ["../local schemas"]
  exclude: ["*skip*.yaml"]
issues:
  minSeverity: error
output:
  format: json
`
	//nolint:gosec // the read-only container mount needs a world-readable policy
	require.NoError(t, os.WriteFile(filepath.Join(root, ".otelcol-config-lint.yaml"), []byte(policy), 0o644))

	var prefix []string

	if image != "" {
		require.NotEmpty(t, os.Getenv("OTELCOL_LINT_VERSION"), "image smoke tests must check the exact release version")

		binary = "docker"
		prefix = []string{
			"run", "--rm", "--pull=never", "--platform=linux/amd64", "--network=none", "--read-only",
			"--mount", "type=bind,src=" + root + ",dst=/workspace,readonly",
			"--workdir", "/workspace/config files", image,
		}
	} else {
		var err error

		binary, err = filepath.Abs(binary)
		require.NoError(t, err)
	}

	run := func(t *testing.T, code int, args ...string) (string, string) {
		t.Helper()

		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()

		//nolint:gosec // the test runner explicitly selects the binary or image
		cmd := exec.CommandContext(ctx, binary, append(append([]string{}, prefix...), args...)...)
		cmd.Dir = workdir

		var stdout, stderr bytes.Buffer

		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		t.Logf("args=%q err=%v\nstdout:\n%s\nstderr:\n%s", args, err, &stdout, &stderr)
		require.NoError(t, ctx.Err(), "CLI timed out")

		if err != nil {
			var exitErr *exec.ExitError

			require.ErrorAs(t, err, &exitErr, "CLI could not start")
		}

		require.Equal(t, code, cmd.ProcessState.ExitCode(), "stdout=%s\nstderr=%s", &stdout, &stderr)

		return stdout.String(), stderr.String()
	}

	t.Run("version", func(t *testing.T) {
		t.Parallel()

		stdout, stderr := run(t, 0, "version")
		assert.Empty(t, stderr)
		require.True(t, strings.HasPrefix(stdout, "otelcol-config-lint "), "stdout=%s", stdout)
		assert.NotEqual(t, "otelcol-config-lint devel", strings.TrimSpace(stdout))

		if version := os.Getenv("OTELCOL_LINT_VERSION"); version != "" {
			assert.Equal(t, "otelcol-config-lint "+version, strings.TrimSpace(stdout))
		}
	})

	for _, tt := range []struct {
		name, filename, status string
		code                   int
		args                   []string
	}{
		{
			name: "directory walk and exclusion", filename: "valid agent.yaml", status: "valid", code: 0,
			args: []string{"run", "--verbose", "."},
		},
		{
			name: "explicit path with spaces", filename: "valid agent.yaml", status: "valid", code: 0,
			args: []string{"run", "--verbose", "valid agent.yaml"},
		},
		{
			name:     "invalid explicit path overrides exclusion",
			filename: filepath.Join("nested configs", "skip invalid.yaml"), status: "invalid", code: 1,
			args: []string{"run", "--verbose", filepath.Join("nested configs", "skip invalid.yaml")},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stdout, stderr := run(t, tt.code, tt.args...)
			assert.Contains(t, stderr, "settings read from ")
			assert.Contains(t, stderr, ".otelcol-config-lint.yaml")

			var report struct {
				Files []struct {
					Filename string `json:"filename"`
					Status   string `json:"status"`
				} `json:"files"`
				Summary struct {
					Valid   int `json:"valid"`
					Invalid int `json:"invalid"`
					Errors  int `json:"errors"`
					Skipped int `json:"skipped"`
				} `json:"summary"`
			}

			require.NoError(t, json.Unmarshal([]byte(stdout), &report), "stdout must contain only the JSON report")
			require.Len(t, report.Files, 1)
			assert.Equal(t, tt.filename, report.Files[0].Filename)
			assert.Equal(t, tt.status, report.Files[0].Status)
			assert.Equal(t, tt.code, report.Summary.Invalid)
			assert.Equal(t, 1-tt.code, report.Summary.Valid)
			assert.Zero(t, report.Summary.Errors)
			assert.Zero(t, report.Summary.Skipped)
		})
	}

	t.Run("usage error goes to stderr", func(t *testing.T) {
		t.Parallel()

		stdout, stderr := run(t, 2, "run", "--output", "xml", ".")
		assert.Empty(t, stdout)
		assert.Contains(t, stderr, "unknown output format")
	})
}
