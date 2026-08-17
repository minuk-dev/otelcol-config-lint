// Package hardcodedsecret reports a credential the config carries itself.
package hardcodedsecret

import (
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// headersKey is the map whose keys are HTTP header names rather than settings,
// and where a credential is most often written out in full.
const headersKey = "headers"

// New builds the rule.
//
// Upstream's guidance is to keep sensitive values in a secret store or on an
// encrypted filesystem and pull them in with a confmap expansion; a config in
// git that spells the value out has already handed it to everyone who can read
// the repository, and CI is the last moment before it reaches a remote.
//
// It reports every declared component, wired or not. An exporter no pipeline
// reaches still ships its credential to whoever clones the repository, which is
// the thing being reported -- unlike the runtime rules, nothing here depends on
// the component being instantiated.
//
// Severity is Warning rather than Error because a local test config with a
// dummy credential is legitimate and common, and this rule will meet plenty of
// them. False positives are the whole risk it carries, so the matching below
// gives up a real finding wherever a value could reasonably not be a secret.
func New() rule.Rule {
	return hardcodedSecret{rule.NewBase("hardcoded-secret",
		"a credential written into the config instead of expanded at runtime", diag.Warning)}
}

type hardcodedSecret struct{ rule.Base }

func (r hardcodedSecret) Check(ctx *rule.Context) {
	for _, kind := range config.Kinds() {
		sec := ctx.File.Sections[kind]
		if sec == nil {
			continue
		}

		for _, c := range sec.Components {
			for _, hit := range findSecrets(c.ValueNode, kind.Section()+"."+c.ID.String(), "", false) {
				ctx.Report(rule.Finding{
					Node: hit.node, Path: hit.path,
					// The value is never quoted back. Printing it would copy the
					// secret into the CI log, which is the one thing this rule
					// must not do.
					Message: rule.Quote(rule.ShortPath(hit.path)) +
						" is a credential written into the config for " +
						string(kind) + " " + rule.Quote(c.ID.String()),
					Hint: "move it to a secret store and reference it here as an expansion such as " +
						"${env:OTLP_TOKEN}, which the collector resolves at startup",
					Docs: rule.ConfigSecurityDocs,
				})
			}
		}
	}
}

type secretHit struct {
	node *yaml.Node
	path string
}

// findSecrets walks a component's settings for a scalar whose key names a
// credential and whose value is written out rather than expanded, at any
// nesting depth: an exporter's credentials sit under auth, tls or headers
// blocks as often as at the top level.
//
// key is the setting the node hangs from, which a scalar is matched by and a
// list passes down to its items: api_keys is a list of api keys, and dropping
// the key at the bracket would let a credential through for being written one
// line lower. inHeaders reports that the walk is inside a headers map, whose
// keys are header names rather than settings.
func findSecrets(n *yaml.Node, path, key string, inHeaders bool) []secretHit {
	if n == nil {
		return nil
	}

	var out []secretHit

	switch n.Kind {
	case yaml.MappingNode:
		for _, e := range rule.MapEntries(n, path) {
			out = append(out,
				findSecrets(e.Node, e.Path, e.Key, inHeaders || strings.EqualFold(e.Key, headersKey))...)
		}
	case yaml.SequenceNode:
		for i, item := range n.Content {
			out = append(out, findSecrets(item, rule.IndexPath(path, i), key, inHeaders)...)
		}
	case yaml.ScalarNode:
		if isHardcodedSecret(key, n.Value, inHeaders) {
			out = append(out, secretHit{node: n, path: path})
		}
	default:
		// An alias is a value written once and used twice, and the anchor is
		// reported where it is written whenever that is somewhere this walk
		// reaches. Following it here would report the same credential at every
		// use, and only one of them is somewhere to edit.
	}

	return out
}

// isHardcodedSecret reports whether a setting names a credential and holds one
// in full.
func isHardcodedSecret(key, value string, inHeaders bool) bool {
	// Either the key says the value is a credential, or a header value says so
	// itself by opening with an authentication scheme.
	scheme := authScheme(value)

	named := namesCredential(key, inHeaders) || (inHeaders && scheme != "")
	if !named {
		return false
	}

	// The scheme is not the credential, and what follows it is what has to be
	// judged: "Bearer <YOUR_TOKEN>" is a placeholder, and reading it whole
	// would find neither the angle brackets nor the expansion in
	// "Bearer ${env:OTLP_TOKEN}".
	value = strings.TrimSpace(value[len(scheme):])

	// An expansion is the config doing exactly what it should, and a
	// placeholder is a config with no credential in it yet.
	return !resolvedAtRuntime(value) && !isPlaceholder(value)
}

// resolvedAtRuntime reports whether confmap, rather than the file, supplies the
// value.
//
// rule.HasExpansion is the general test and the right one for a schema check,
// but it also matches the bare $VAR shorthand anywhere inside a string -- and a
// generated password such as pa$sw0rd contains one. Staying silent about a real
// credential is the worse failure of the two, so the shorthand counts only when
// it is the whole value. The ${...} form is unambiguous and counts wherever it
// appears, including in a value that is only partly expanded.
func resolvedAtRuntime(value string) bool {
	return braceExpansionRE.MatchString(value) || shorthandExpansionRE.MatchString(value)
}

var (
	braceExpansionRE     = regexp.MustCompile(`\$\{[^}]*\}`)
	shorthandExpansionRE = regexp.MustCompile(`^\$[A-Za-z_][A-Za-z0-9_]*$`)
)

// namesCredential reports whether a leaf key names a credential.
//
// The match is a case-insensitive substring, so component spellings this list
// never saw -- bearer_token, sasl_password -- are covered too. That breadth is
// paid for by the suffixes below: a key that names where a credential lives
// rather than holding one is the common false positive, and it is a bad one,
// since private_key_file: /etc/certs/key.pem and token_url: https://issuer/oauth2/token
// are both correctly configured components.
func namesCredential(key string, inHeaders bool) bool {
	key = strings.ToLower(key)

	// Inside a headers map the keys are header names, spelled with hyphens
	// where a setting would use underscores. Authorization is the one this
	// rule exists for; x-api-key and x-auth-token fall out of the same list
	// the settings use, one spelling later.
	if inHeaders {
		key = strings.ReplaceAll(key, "-", "_")
		if key == "authorization" || key == "proxy_authorization" {
			return true
		}
	}

	for _, suffix := range []string{"_file", "_path", "_url", "_uri", "_name"} {
		if strings.HasSuffix(key, suffix) {
			return false
		}
	}

	// "secret" subsumes client_secret and secret_key; "password" subsumes
	// sasl_password and the rest. A bare "key" is deliberately absent: key,
	// keys and key_file are ordinary settings across the collector, which is
	// why key_pem -- a private key written inline, and the worst of these to
	// miss -- has to be named. cert_pem and ca_pem are public and are not.
	for _, name := range []string{
		"password", "passphrase", "token", "secret", "credential",
		"apikey", "api_key", "access_key", "private_key", "key_pem",
	} {
		if strings.Contains(key, name) {
			return true
		}
	}

	return false
}

// authScheme returns the HTTP authentication scheme a header value opens with,
// or "" for one that opens with none. A scheme is the shape a credential takes
// even when the header is spelled something other than authorization, and it is
// also a prefix that has to come off before the rest of the value is judged.
// The schemes are case-insensitive in the protocol, so they are matched that
// way here.
func authScheme(value string) string {
	for _, scheme := range []string{"bearer ", "basic "} {
		if len(value) >= len(scheme) && strings.EqualFold(value[:len(scheme)], scheme) {
			return value[:len(scheme)]
		}
	}

	return ""
}

// isPlaceholder reports whether a value is one of the things people write where
// a credential goes when there is no credential yet. Reporting these is how a
// rule at Warning becomes noise nobody reads, so the list is generous.
func isPlaceholder(value string) bool {
	value = strings.TrimSpace(value)

	// An empty value configures nothing, and <your-token> is a README that was
	// copied rather than filled in.
	if value == "" || (strings.HasPrefix(value, "<") && strings.HasSuffix(value, ">")) {
		return true
	}

	// changeme, change-me and CHANGE_ME are one word written three ways.
	norm := strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.ToLower(value))

	switch norm {
	// A boolean is never a credential, whatever the key is called: a setting
	// such as use_default_credentials matches the name list and holds true.
	case "true", "false",
		"none", "null", "nil", "unset", "empty", "todo", "fixme", "tbd",
		"changeme", "changeit", "replaceme", "placeholder", "example", "dummy", "test",
		"secret", "password", "token", "apikey", "xxx":
		return true
	}

	// yourtoken, your-api-key, YOUR_PASSWORD_HERE.
	return strings.HasPrefix(norm, "your")
}
