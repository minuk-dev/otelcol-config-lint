package rule_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// secreting wraps exporter settings in a config that is otherwise clean. The
// settings are written already indented by four spaces.
func secreting(settings string) string {
	return `
receivers: {otlp: }
exporters:
  otlp:
    endpoint: backend:4317
` + settings + `
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [otlp]}
`
}

func TestHardcodedSecret(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		settings string
		want     bool // whether the value should be reported
	}{
		"a password": {settings: "    password: hunter2", want: true},
		"a token":    {settings: "    token: abc123def456", want: true},
		"an api key": {settings: "    api_key: abc123def456", want: true},
		"an apikey":  {settings: "    apikey: abc123def456", want: true},
		// The key list is matched case-insensitively, as a substring, so
		// spellings the list never saw are covered too.
		"a shouted key":     {settings: "    PASSWORD: hunter2", want: true},
		"a prefixed key":    {settings: "    sasl_password: hunter2", want: true},
		"a client secret":   {settings: "    client_secret: abc123def456", want: true},
		"a private key":     {settings: "    private_key: abc123def456", want: true},
		"an access key":     {settings: "    access_key: AKIAIOSFODNN7EXAMPLE", want: true},
		"a secret key":      {settings: "    secret_key: abc123def456", want: true},
		"a passphrase":      {settings: "    passphrase: abc123def456", want: true},
		"a nested password": {settings: "    auth:\n      password: hunter2", want: true},
		// A number is a credential written out as surely as a string is.
		"a numeric token": {settings: "    token: 12345678", want: true},
		// A private key written inline is the worst of these to miss, and a
		// bare "key" is not matched, so the pem spelling has to be named.
		"an inline private key": {settings: "    key_pem: BEGIN PRIVATE KEY", want: true},
		// The prometheus receiver's spelling, outside any headers map.
		"prometheus credentials": {settings: "    credentials: abc123def456", want: true},
		// The $VAR shorthand is an expansion only as the whole value; a
		// generated password containing one is still a password.
		"a password containing a dollar": {settings: `    password: pa$sw0rd`, want: true},

		// The public half of a keypair is not a credential.
		"an inline certificate": {settings: "    cert_pem: BEGIN CERTIFICATE", want: false},
		// A setting whose name matches and whose value is a boolean is a
		// switch, not a credential.
		"a credentials switch": {settings: "    use_default_credentials: true", want: false},

		// An expansion is the config doing the right thing, whichever member
		// of the family it uses.
		"a token from the environment": {settings: "    token: ${env:OTLP_TOKEN}", want: false},
		"a token from a file":          {settings: "    token: ${file:/run/secrets/token}", want: false},
		"a token from a shorthand":     {settings: "    token: $OTLP_TOKEN", want: false},
		"a token half expanded":        {settings: "    token: Bearer ${env:OTLP_TOKEN}", want: false},

		// Nothing was written, so nothing was leaked.
		"an empty password": {settings: `    password: ""`, want: false},
		"a null password":   {settings: "    password:", want: false},

		// A key that names where a credential lives is the opposite of the
		// problem: it is a properly configured component.
		"a key file":      {settings: "    private_key_file: /etc/certs/key.pem", want: false},
		"a password file": {settings: "    password_file: /etc/otel/password", want: false},
		"a token url":     {settings: "    token_url: https://issuer/oauth2/token", want: false},
		"a secret name":   {settings: "    secret_name: otel-collector-credentials", want: false},

		// A bare "key" is an ordinary setting across the collector, and
		// matching it would report half of every config.
		"a plain key": {settings: "    key: value", want: false},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			found := checkRule(t, "hardcoded-secret", secreting(tt.settings), rule.Environment{})
			assert.Len(t, found, map[bool]int{true: 1, false: 0}[tt.want])
		})
	}
}

// Placeholders are what a config holds when it has no credential in it yet.
// Reporting them is how a rule at warning becomes noise nobody reads.
func TestHardcodedSecretIgnoresPlaceholders(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"none", "null", "unset", "TODO", "tbd",
		"changeme", "CHANGE_ME", "change-me", "REPLACE_ME", "replaceme",
		"<your-token>", "your-api-key", "YOUR_TOKEN_HERE",
		"placeholder", "example", "dummy", "test", "secret", "xxx",
	} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()

			found := checkRule(t, "hardcoded-secret", secreting("    token: "+value), rule.Environment{})
			assert.Empty(t, found)
		})
	}
}

// The headers map is where credentials are written out most often, and its
// keys are header names rather than settings.
func TestHardcodedSecretInHeaders(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		settings string
		want     bool
	}{
		"a bearer token": {
			settings: "    headers:\n      authorization: Bearer abc123def456",
			want:     true,
		},
		"basic credentials": {
			settings: "    headers:\n      authorization: Basic dXNlcjpwYXNz",
			want:     true,
		},
		// The header name is case-insensitive in the protocol and people
		// spell it both ways.
		"a capitalised header": {
			settings: "    headers:\n      Authorization: abc123def456",
			want:     true,
		},
		// A scheme is a credential whatever the header is called.
		"a scheme under another header": {
			settings: "    headers:\n      x-my-auth: Bearer abc123def456",
			want:     true,
		},
		"an api key header": {
			settings: "    headers:\n      x-api-key: abc123def456",
			want:     true,
		},
		"an expanded header": {
			settings: "    headers:\n      authorization: Bearer ${env:OTLP_TOKEN}",
			want:     false,
		},
		// A header that carries no credential is a routing key, and this rule
		// has nothing to say about it.
		"a tenant header": {
			settings: "    headers:\n      x-scope-orgid: tenant-a",
			want:     false,
		},
		// The scheme is not the credential: what follows it is, and a
		// placeholder written after one is still a placeholder.
		"a placeholder behind a scheme": {
			settings: "    headers:\n      authorization: Bearer <YOUR_TOKEN>",
			want:     false,
		},
		"a changeme behind a scheme": {
			settings: "    headers:\n      authorization: Bearer changeme",
			want:     false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			found := checkRule(t, "hardcoded-secret", secreting(tt.settings), rule.Environment{})
			assert.Len(t, found, map[bool]int{true: 1, false: 0}[tt.want])
		})
	}
}

// The finding has to say where the credential is without saying what it is:
// printing the value would copy the secret into the CI log.
func TestHardcodedSecretNeverPrintsTheValue(t *testing.T) {
	t.Parallel()

	const value = "abc123def456"

	found := checkRule(t, "hardcoded-secret",
		secreting("    headers:\n      authorization: Bearer "+value), rule.Environment{})
	require.Len(t, found, 1)

	assert.Equal(t, `"headers.authorization" is a credential written into the config for exporter "otlp"`,
		found[0].Message)
	assert.Equal(t, "move it to a secret store and reference it here as an expansion such as "+
		"${env:OTLP_TOKEN}, which the collector resolves at startup", found[0].Hint)
	assert.Equal(t, "exporters.otlp.headers.authorization", found[0].Path)
	assert.Equal(t, diag.Warning, found[0].Severity)

	assert.NotContains(t, found[0].Message, value, "the finding prints the secret")
	assert.NotContains(t, found[0].Hint, value, "the finding prints the secret")
}

// A credential is reported wherever it is declared, in any section and at any
// depth, because what is being reported is the value sitting in the repository
// rather than anything the collector does with it.
func TestHardcodedSecretAcrossSections(t *testing.T) {
	t.Parallel()

	src := `
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317
        auth:
          authenticator: basicauth
exporters:
  otlp:
    endpoint: backend:4317
    headers:
      authorization: Bearer abc123def456
  otlp/unused:
    endpoint: other:4317
    api_key: abc123def456
extensions:
  basicauth:
    htpasswd:
      inline: |
        user:password
    client_auth:
      password: hunter2
service:
  extensions: [basicauth]
  pipelines:
    traces: {receivers: [otlp], exporters: [otlp]}
`

	found := checkRule(t, "hardcoded-secret", src, rule.Environment{})
	require.Len(t, found, 3)
	assert.Equal(t, "exporters.otlp.headers.authorization", found[0].Path)
	assert.Equal(t, "exporters.otlp/unused.api_key", found[1].Path)
	assert.Equal(t, "extensions.basicauth.client_auth.password", found[2].Path)
}

// A credential inside a list is still written into the config; the walk has to
// go through sequences as well as maps, and a list of bare scalars is matched
// by the key that named the list.
func TestHardcodedSecretInASequence(t *testing.T) {
	t.Parallel()

	src := `
receivers: {otlp: }
exporters:
  otlp:
    endpoint: backend:4317
    api_keys:
      - abc123def456
    backends:
      - name: primary
        token: abc123def456
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [otlp]}
`

	found := checkRule(t, "hardcoded-secret", src, rule.Environment{})
	require.Len(t, found, 2)
	assert.Equal(t, "exporters.otlp.api_keys[0]", found[0].Path)
	assert.Equal(t, "exporters.otlp.backends[0].token", found[1].Path)
}

// An alias is the anchor it points at, written once and used twice. Following
// it would report the same credential in two places, and only one of them is
// somewhere to edit.
func TestHardcodedSecretReportsAnAnchorOnce(t *testing.T) {
	t.Parallel()

	src := `
receivers: {otlp: }
exporters:
  otlp:
    endpoint: backend:4317
    headers: &creds
      authorization: Bearer abc123def456
  otlp/second:
    endpoint: other:4317
    headers: *creds
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [otlp, otlp/second]}
`

	found := checkRule(t, "hardcoded-secret", src, rule.Environment{})
	require.Len(t, found, 1)
	assert.Equal(t, "exporters.otlp.headers.authorization", found[0].Path)
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

			found := checkRule(t, "debug-extension-exposed", extending(tt.name, tt.settings), rule.Environment{})

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

	found := checkRule(t, "debug-extension-exposed",
		extending("pprof", "    endpoint: 0.0.0.0:1777"), rule.Environment{})

	require.Len(t, found, 1)
	assert.Contains(t, found[0].Message, "heap profiles")
	assert.Contains(t, found[0].Message, "every interface of the host")
	assert.Contains(t, found[0].Hint, "localhost")
	assert.Equal(t, "extensions.pprof.endpoint", found[0].Path)
}

// TestExtensionNobodyStartsIsNotReported pins the other half of the rule's
// premise: service.extensions is what instantiates an extension, so a
// declaration left out of it binds no port for anyone to reach.
func TestExtensionNobodyStartsIsNotReported(t *testing.T) {
	t.Parallel()

	src := `
receivers: {otlp: }
exporters: {debug: }
extensions:
  pprof:
    endpoint: 0.0.0.0:1777
service:
  pipelines:
    traces: {receivers: [otlp], exporters: [debug]}
`

	assert.Empty(t, checkRule(t, "debug-extension-exposed", src, rule.Environment{}))
	// The declaration is not thereby unremarked: another rule owns it.
	assert.NotEmpty(t, checkRule(t, "unused-component", src, rule.Environment{}))
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

			found := checkRule(t, "debug-extension-exposed", tt.src+wiring, rule.Environment{})
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
		found := checkRule(t, "debug-extension-exposed",
			extending(typ, "    endpoint: 0.0.0.0:13133"), rule.Environment{})
		assert.Empty(t, found, "%s should not be reported", typ)
	}
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

			found := checkRule(t, "receiver-binds-all-interfaces",
				receiving("      grpc:\n        endpoint: "+tt.endpoint), rule.Environment{})

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

	found := checkRule(t, "receiver-binds-all-interfaces",
		receiving("      grpc:\n        endpoint: 0.0.0.0:4317"), rule.Environment{})
	require.Len(t, found, 1)

	assert.Equal(t, `"protocols.grpc.endpoint" binds "0.0.0.0:4317", every interface the host has, `+
		`for receiver "otlp"`, found[0].Message)
	assert.Equal(t, "bind the interface you meant, e.g. 127.0.0.1:4317 when every client is local; "+
		"in Kubernetes write ${env:MY_POD_IP}:4317, the pod IP the downward API supplies",
		found[0].Hint)
	assert.Contains(t, found[0].Docs, "config-best-practices")

	// A port nothing can read before the collector starts leaves the address it
	// belongs to out of the suggestion rather than guessing at one.
	runtimePort := checkRule(t, "receiver-binds-all-interfaces",
		receiving("      grpc:\n        endpoint: 0.0.0.0:${env:OTLP_PORT}"), rule.Environment{})
	require.Len(t, runtimePort, 1)
	assert.Equal(t, "bind the interface you meant, e.g. 127.0.0.1 when every client is local; "+
		"in Kubernetes write ${env:MY_POD_IP}, the pod IP the downward API supplies",
		runtimePort[0].Hint)
}

// Every endpoint is reported, since each is a separate line to edit.
func TestReceiverBindsAllInterfacesReportsEveryProtocol(t *testing.T) {
	t.Parallel()

	found := checkRule(t, "receiver-binds-all-interfaces",
		receiving("      grpc:\n        endpoint: 0.0.0.0:4317\n"+
			"      http:\n        endpoint: 0.0.0.0:4318"), rule.Environment{})

	require.Len(t, found, 2)
	assert.Equal(t, "receivers.otlp.protocols.grpc.endpoint", found[0].Path)
	assert.Equal(t, "receivers.otlp.protocols.http.endpoint", found[1].Path)
}

// An extension listens on its endpoint as much as a receiver does.
func TestReceiverBindsAllInterfacesCoversExtensions(t *testing.T) {
	t.Parallel()

	found := checkRule(t, "receiver-binds-all-interfaces",
		extending("basicauth", "    endpoint: 0.0.0.0:8080"), rule.Environment{})

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

			assert.Empty(t, checkRule(t, "receiver-binds-all-interfaces", src, rule.Environment{}))
		})
	}
}

// The debugging extensions are reported once, by the rule that says what they
// hand out; the health checks are not reported at all, since the kubelet
// reaches a liveness probe from off the container's loopback interface.
func TestReceiverBindsAllInterfacesLeavesExtensionsToTheirOwnRules(t *testing.T) {
	t.Parallel()

	for _, typ := range []string{"pprof", "zpages"} {
		src := extending(typ, "    endpoint: 0.0.0.0:1777")
		assert.Empty(t, checkRule(t, "receiver-binds-all-interfaces", src, rule.Environment{}),
			"%s is debug-extension-exposed's to report", typ)
		assert.NotEmpty(t, checkRule(t, "debug-extension-exposed", src, rule.Environment{}),
			"%s should still be reported by the rule that owns it", typ)
	}

	for _, typ := range []string{"health_check", "healthcheckv2"} {
		assert.Empty(t, checkRule(t, "receiver-binds-all-interfaces",
			extending(typ, "    endpoint: 0.0.0.0:13133"), rule.Environment{}),
			"%s bound to every interface is a correct deployment", typ)
	}
}
