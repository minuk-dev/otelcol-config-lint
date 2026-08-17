package hardcodedsecret_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/hardcodedsecret"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/ruletest"
)

// check runs the rule over src, which every test in this package starts from.
func check(t *testing.T, src string) diag.Diagnostics {
	t.Helper()

	found, err := ruletest.Run(hardcodedsecret.New(), src)
	require.NoError(t, err, "parse")

	return found
}

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

			found := check(t, secreting(tt.settings))
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

			found := check(t, secreting("    token: "+value))
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

			found := check(t, secreting(tt.settings))
			assert.Len(t, found, map[bool]int{true: 1, false: 0}[tt.want])
		})
	}
}

// The finding has to say where the credential is without saying what it is:
// printing the value would copy the secret into the CI log.
func TestHardcodedSecretNeverPrintsTheValue(t *testing.T) {
	t.Parallel()

	const value = "abc123def456"

	found := check(t, secreting("    headers:\n      authorization: Bearer "+value))
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

	found := check(t, src)
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

	found := check(t, src)
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

	found := check(t, src)
	require.Len(t, found, 1)
	assert.Equal(t, "exporters.otlp.headers.authorization", found[0].Path)
}
