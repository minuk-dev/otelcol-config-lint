package debugextensionexposed_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/debugextensionexposed"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/ruletest"
)

// check runs the rule over src, which every test in this package starts from.
func check(t *testing.T, src string) diag.Diagnostics {
	t.Helper()

	found, err := ruletest.Run(debugextensionexposed.New(), src)
	require.NoError(t, err, "parse")

	return found
}

// extending wraps extension settings in a config that is otherwise clean. The
// settings are written already indented by four spaces.
func extending(name, settings string) string {
	return `
receivers: {otlp: }
exporters: {debug: }
extensions:
  ` + name + `:
` + settings + `
service:
  extensions: [` + name + `]
  pipelines:
    traces: {receivers: [otlp], exporters: [debug]}
`
}

func TestDebugExtensionExposed(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		name     string // the extension declaration
		settings string
		want     diag.Severity // diag.Off when nothing should be reported
	}{
		"pprof on every interface": {
			name:     "pprof",
			settings: "    endpoint: 0.0.0.0:1777",
			want:     diag.Warning,
		},
		"zpages on every interface": {
			name:     "zpages",
			settings: "    endpoint: 0.0.0.0:55679",
			want:     diag.Warning,
		},
		"the unspecified IPv6 address": {
			name:     "pprof",
			settings: "    endpoint: \"[::]:1777\"",
			want:     diag.Warning,
		},
		"a bare port": {
			name:     "pprof",
			settings: "    endpoint: \":1777\"",
			want:     diag.Warning,
		},
		"an address with no port at all": {
			name:     "pprof",
			settings: "    endpoint: 0.0.0.0",
			want:     diag.Warning,
		},
		"a named instance is matched on its type": {
			name:     "zpages/internal",
			settings: "    endpoint: 0.0.0.0:55679",
			want:     diag.Warning,
		},
		"a specific address on some other network": {
			name:     "pprof",
			settings: "    endpoint: 10.0.4.7:1777",
			want:     diag.Info,
		},
		"localhost": {
			name:     "pprof",
			settings: "    endpoint: localhost:1777",
			want:     diag.Off,
		},
		"the loopback address": {
			name:     "zpages",
			settings: "    endpoint: 127.0.0.1:55679",
			want:     diag.Off,
		},
		"the IPv6 loopback address": {
			name:     "zpages",
			settings: "    endpoint: \"[::1]:55679\"",
			want:     diag.Off,
		},
		"no endpoint at all takes the extension's default": {
			name:     "pprof",
			settings: "",
			want:     diag.Off,
		},
		"an endpoint resolved at runtime": {
			name:     "pprof",
			settings: "    endpoint: ${env:PPROF_ENDPOINT}",
			want:     diag.Off,
		},
		"an address resolved at runtime": {
			name:     "pprof",
			settings: "    endpoint: ${env:PPROF_HOST}:1777",
			want:     diag.Off,
		},
		// Only the port is left open, and the port is not what decides who can
		// reach the endpoint.
		"a port resolved at runtime": {
			name:     "pprof",
			settings: "    endpoint: 0.0.0.0:${env:PPROF_PORT}",
			want:     diag.Warning,
		},
		"a loopback address with a port resolved at runtime": {
			name:     "pprof",
			settings: "    endpoint: localhost:${env:PPROF_PORT}",
			want:     diag.Off,
		},
		"a hostname this linter will not resolve": {
			name:     "pprof",
			settings: "    endpoint: collector.internal:1777",
			want:     diag.Off,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			found := check(t, extending(tt.name, tt.settings))

			if tt.want == diag.Off {
				assert.Empty(t, found)

				return
			}

			require.Len(t, found, 1)
			assert.Equal(t, tt.want, found[0].Severity)
			assert.Contains(t, found[0].Docs, "docs/security-best-practices.md")
		})
	}
}

// TestDebugExtensionExposedSaysWhatIsServed pins the part of the message that
// makes the finding worth reading: what the endpoint actually hands out.
func TestDebugExtensionExposedSaysWhatIsServed(t *testing.T) {
	t.Parallel()

	found := check(t, extending("pprof", "    endpoint: 0.0.0.0:1777"))

	require.Len(t, found, 1)
	assert.Contains(t, found[0].Message, "heap profiles")
	assert.Contains(t, found[0].Message, "every interface of the host")
	assert.Contains(t, found[0].Hint, "localhost")
	assert.Equal(t, "extensions.pprof.endpoint", found[0].Path)
}

// TestMergedEndpointIsRead covers the endpoint a component does not write
// itself. A "<<" merge is how one debugging block is shared between several
// extensions, and reading only local keys would miss every one of them.
func TestMergedEndpointIsRead(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		src  string
		want bool
	}{
		"a merge supplies the endpoint": {
			src: `
debug: &debug
  endpoint: 0.0.0.0:1777
extensions:
  pprof:
    <<: *debug
`,
			want: true,
		},
		"a merge of several mappings": {
			src: `
ports: &ports
  endpoint: 0.0.0.0:1777
timeouts: &timeouts
  idle_timeout: 30s
extensions:
  pprof:
    <<: [*timeouts, *ports]
`,
			want: true,
		},
		"a local endpoint wins over the merged one": {
			src: `
debug: &debug
  endpoint: 0.0.0.0:1777
extensions:
  pprof:
    <<: *debug
    endpoint: localhost:1777
`,
			want: false,
		},
		"a merge that supplies no endpoint": {
			src: `
debug: &debug
  idle_timeout: 30s
extensions:
  pprof:
    <<: *debug
`,
			want: false,
		},
	}

	const wiring = `
receivers: {otlp: }
exporters: {debug: }
service:
  extensions: [pprof]
  pipelines:
    traces: {receivers: [otlp], exporters: [debug]}
`

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			found := check(t, tt.src+wiring)
			if !tt.want {
				assert.Empty(t, found)

				return
			}

			require.Len(t, found, 1)
			assert.Contains(t, found[0].Message, "0.0.0.0:1777")
		})
	}
}

// TestHealthCheckIsNotReported records the exclusion: the kubelet issues a
// liveness probe from off the container's loopback interface, so a health
// check bound to 0.0.0.0 is a correct deployment and not a finding.
func TestHealthCheckIsNotReported(t *testing.T) {
	t.Parallel()

	for _, typ := range []string{"health_check", "healthcheckv2"} {
		found := check(t, extending(typ, "    endpoint: 0.0.0.0:13133"))
		assert.Empty(t, found, "%s should not be reported", typ)
	}
}
