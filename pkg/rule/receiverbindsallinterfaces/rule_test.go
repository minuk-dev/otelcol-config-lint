package receiverbindsallinterfaces_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/receiverbindsallinterfaces"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/ruletest"
)

// check runs the rule over src, which every test in this package starts from.
func check(t *testing.T, src string) diag.Diagnostics {
	t.Helper()

	found, err := ruletest.Run(receiverbindsallinterfaces.New(), src)
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

// receiving wraps otlp receiver protocol settings in a config that is otherwise
// clean. The settings are written already indented by six spaces.
func receiving(settings string) string {
	return `
receivers:
  otlp:
    protocols:
` + settings + `
exporters: {debug: }
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [debug]}
`
}

func TestReceiverBindsAllInterfaces(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		endpoint string
		want     bool // whether the endpoint should be reported
	}{
		"the IPv4 unspecified address": {endpoint: "0.0.0.0:4317", want: true},
		"the IPv6 unspecified address": {endpoint: `"[::]:4317"`, want: true},
		// What netstat prints for an IPv6 listener, and what gets copied from
		// there. Go refuses the spelling outright, so the collector does not
		// start; binding a specific interface fixes that as well.
		"the IPv6 unspecified address without brackets": {endpoint: `":::4317"`, want: true},
		// net.Listen reads a bare port as every interface too.
		"a bare port":                   {endpoint: `":4317"`, want: true},
		"the unspecified address alone": {endpoint: "0.0.0.0", want: true},
		// The address says plainly who can reach it; only the port is left open.
		"a port resolved at runtime": {endpoint: "0.0.0.0:${env:OTLP_PORT}", want: true},

		"loopback":                        {endpoint: "localhost:4317", want: false},
		"the loopback address":            {endpoint: "127.0.0.1:4317", want: false},
		"the IPv6 loopback address":       {endpoint: `"[::1]:4317"`, want: false},
		"one specific address":            {endpoint: "10.0.4.7:4317", want: false},
		"the pod IP the docs name":        {endpoint: "${env:MY_POD_IP}:4317", want: false},
		"an endpoint resolved at runtime": {endpoint: "${env:OTLP_ENDPOINT}", want: false},
		// What a name resolves to is a property of the network, not of the file.
		"a hostname this linter will not place": {endpoint: "collector.internal:4317", want: false},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			found := check(t, receiving("      grpc:\n        endpoint: "+tt.endpoint))

			if !tt.want {
				assert.Empty(t, found)

				return
			}

			require.Len(t, found, 1)
			assert.Equal(t, diag.Warning, found[0].Severity)
			assert.Equal(t, "receivers.otlp.protocols.grpc.endpoint", found[0].Path)
		})
	}
}

// The finding has to say which of a component's endpoints is meant, and the
// hint has to carry the fix upstream documents rather than only "do not".
func TestReceiverBindsAllInterfacesReadsAsASentence(t *testing.T) {
	t.Parallel()

	found := check(t, receiving("      grpc:\n        endpoint: 0.0.0.0:4317"))
	require.Len(t, found, 1)

	assert.Equal(t, `"protocols.grpc.endpoint" binds "0.0.0.0:4317", every interface the host has, `+
		`for receiver "otlp"`, found[0].Message)
	assert.Equal(t, "bind the interface you meant, e.g. 127.0.0.1:4317 when every client is local; "+
		"in Kubernetes write ${env:MY_POD_IP}:4317, the pod IP the downward API supplies",
		found[0].Hint)
	assert.Contains(t, found[0].Docs, "config-best-practices")

	// A port nothing can read before the collector starts leaves the address it
	// belongs to out of the suggestion rather than guessing at one.
	runtimePort := check(t, receiving("      grpc:\n        endpoint: 0.0.0.0:${env:OTLP_PORT}"))
	require.Len(t, runtimePort, 1)
	assert.Equal(t, "bind the interface you meant, e.g. 127.0.0.1 when every client is local; "+
		"in Kubernetes write ${env:MY_POD_IP}, the pod IP the downward API supplies",
		runtimePort[0].Hint)
}

// Every endpoint is reported, since each is a separate line to edit.
func TestReceiverBindsAllInterfacesReportsEveryProtocol(t *testing.T) {
	t.Parallel()

	found := check(t, receiving("      grpc:\n        endpoint: 0.0.0.0:4317\n"+
		"      http:\n        endpoint: 0.0.0.0:4318"))

	require.Len(t, found, 2)
	assert.Equal(t, "receivers.otlp.protocols.grpc.endpoint", found[0].Path)
	assert.Equal(t, "receivers.otlp.protocols.http.endpoint", found[1].Path)
}

// An extension listens on its endpoint as much as a receiver does.
func TestReceiverBindsAllInterfacesCoversExtensions(t *testing.T) {
	t.Parallel()

	found := check(t, extending("basicauth", "    endpoint: 0.0.0.0:8080"))

	require.Len(t, found, 1)
	assert.Equal(t, `"endpoint" binds "0.0.0.0:8080", every interface the host has, `+
		`for extension "basicauth"`, found[0].Message)
}

func TestReceiverBindsAllInterfacesStaysQuiet(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		// An exporter's endpoint is a destination, not a bind address. 0.0.0.0
		// there is a different mistake with a different fix.
		"an exporter's endpoint": `
receivers: {otlp: }
exporters:
  otlp:
    endpoint: 0.0.0.0:4317
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [otlp]}
`,
		// A receiver no pipeline names is never instantiated, so it binds
		// nothing; unused-component owns the declaration.
		"a receiver nothing references": `
receivers:
  otlp: {}
  otlp/unused:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4318
exporters: {debug: }
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [debug]}
`,
		// The extension is declared but never started, so no port is bound.
		"an extension service.extensions leaves out": `
receivers: {otlp: }
exporters: {debug: }
extensions:
  basicauth:
    endpoint: 0.0.0.0:8080
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [debug]}
`,
		// A receiver that writes no endpoint takes its own default, which has
		// been localhost since v0.110.0.
		"an endpoint nobody wrote": `
receivers:
  otlp:
    protocols:
      grpc:
exporters: {debug: }
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [debug]}
`,
	}

	for name, src := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Empty(t, check(t, src))
		})
	}
}

// The stanza-based log receivers write the address they listen on under
// listen_address rather than endpoint, and syslog carries one per transport.
// Reading only endpoint would leave every one of them unreported.
func TestReceiverBindsAllInterfacesReadsListenAddress(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		name     string // the receiver declaration
		settings string
		path     string
	}{
		"tcplog": {
			name:     "tcplog",
			settings: "    listen_address: 0.0.0.0:54525",
			path:     "receivers.tcplog.listen_address",
		},
		"udplog": {
			name:     "udplog",
			settings: `    listen_address: ":54526"`,
			path:     "receivers.udplog.listen_address",
		},
		"syslog under a transport": {
			name:     "syslog",
			settings: "    tcp:\n      listen_address: 0.0.0.0:54527",
			path:     "receivers.syslog.tcp.listen_address",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			src := "receivers:\n  " + tt.name + ":\n" + tt.settings + `
exporters: {debug: }
service:
  pipelines:
    logs: {receivers: [` + tt.name + `], exporters: [debug]}
`

			found := check(t, src)
			require.Len(t, found, 1)
			assert.Equal(t, tt.path, found[0].Path)
		})
	}
}
