//go:build integration

package schemagen_test

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/cmd/schemagen"
	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/ruletest"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/unknownfield"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// These releases straddle the HTTP keepalive migration, including its private
// warning storage. Resolve their real module graphs rather than imitating tags.
func TestHTTPReleaseSchemas(t *testing.T) {
	t.Parallel()

	for _, version := range []string{"v0.159.0", "v0.160.0"} {
		t.Run(version, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			manifest := filepath.Join(root, "manifest.yaml")
			writeFile(t, manifest, fmt.Sprintf(`dist:
  name: http-regression
receivers:
  - gomod: go.opentelemetry.io/collector/receiver/otlpreceiver %[1]s
exporters:
  - gomod: go.opentelemetry.io/collector/exporter/otlphttpexporter %[1]s
  - gomod: go.opentelemetry.io/collector/exporter/otlpexporter %[1]s
`, version))

			registry := filepath.Join(root, "registry")
			code, _, stderr := run(t, "--builder", manifest, "--registry", registry,
				"--cache", filepath.Join(root, "cache"))
			require.Equal(t, schemagen.ExitOK, code, "%s", stderr)

			for _, format := range []string{"json", "yaml"} {
				t.Run(format, func(t *testing.T) {
					t.Parallel()

					cat, err := schema.ReadFile(filepath.Join(registry, "http-regression", version+"."+format))
					require.NoError(t, err)
					assert.Equal(t, version, cat.CollectorVersion)
					http := cat.Components[config.KindExporter]["otlphttp"].Fields
					require.NotNil(t, http)
					assert.False(t, http.Open)

					grpc := cat.Components[config.KindExporter]["otlp"].Fields
					require.NotNil(t, grpc)
					assert.False(t, grpc.Open, "configgrpc needs no HTTP bookkeeping exemption")

					inputs := map[string]string{
						"deprecated keys": `
receivers:
  otlp:
    protocols:
      http: {idle_timeout: 30s, keep_alives_enabled: false, typo: true}
exporters:
  otlphttp:
    endpoint: http://localhost:4318
    idle_conn_timeout: 30s
    max_idle_conns: 10
    max_idle_conns_per_host: 2
    disable_keep_alives: false
    typo: true
`,
					}
					if version == "v0.160.0" {
						inputs["keepalive keys"] = `
receivers:
  otlp:
    protocols:
      http: {keepalive: {enabled: false, idle_timeout: 30s}, typo: true}
exporters:
  otlphttp:
    endpoint: http://localhost:4318
    keepalive: {enabled: true, idle_conn_timeout: 30s, max_idle_conns: 10, max_idle_conns_per_host: 2}
    typo: true
`
					} else {
						assert.NotContains(t, http.Children, "keepalive", "new keys must not leak into older releases")
					}

					for name, input := range inputs {
						t.Run(name, func(t *testing.T) {
							t.Parallel()

							findings, err := ruletest.RunWith(unknownfield.New(), input,
								ruletest.Options{Strict: true, Schema: cat})
							require.NoError(t, err)

							paths := make([]string, 0, len(findings))
							for _, finding := range findings {
								paths = append(paths, finding.Path)
							}

							assert.ElementsMatch(t, []string{
								"receivers.otlp.protocols.http.typo", "exporters.otlphttp.typo",
							}, paths, "accept valid keys while rejecting HTTP typos")
						})
					}
				})
			}
		})
	}
}
