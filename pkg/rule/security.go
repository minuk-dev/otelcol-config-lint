package rule

import (
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
)

// securityRules check for configurations that hand something away.
func securityRules() []Rule {
	return []Rule{
		hardcodedSecret{base{"hardcoded-secret",
			"a credential written into the config instead of expanded at runtime", diag.Warning}},
	}
}

// headersKey is the map whose keys are HTTP header names rather than settings,
// and where a credential is most often written out in full.
const headersKey = "headers"

// hardcodedSecret reports a credential the config carries itself. Upstream's
// guidance is to keep sensitive values in a secret store or on an encrypted
// filesystem and pull them in with a confmap expansion; a config in git that
// spells the value out has already handed it to everyone who can read the
// repository, and CI is the last moment before it reaches a remote.
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
type hardcodedSecret struct{ base }

func (r hardcodedSecret) Check(ctx *Context) {
	for _, kind := range config.Kinds() {
		sec := ctx.File.Sections[kind]
		if sec == nil {
			continue
		}

		for _, c := range sec.Components {
			for _, hit := range findSecrets(c.ValueNode, kind.Section()+"."+c.ID.String(), false) {
				ctx.Report(Finding{
					Node: hit.node, Path: hit.path,
					// The value is never quoted back. Printing it would copy the
					// secret into the CI log, which is the one thing this rule
					// must not do.
					Message: quote(shortPath(hit.path)) + " is a credential written into the config for " +
						string(kind) + " " + quote(c.ID.String()),
					Hint: "move it to a secret store and reference it here as an expansion such as " +
						"${env:OTLP_TOKEN}, which the collector resolves at startup",
					Docs: configSecurityDocs,
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
// inHeaders reports that the walk is inside a headers map, whose keys are
// header names rather than settings and are matched differently.
func findSecrets(n *yaml.Node, path string, inHeaders bool) []secretHit {
	if n == nil {
		return nil
	}

	var out []secretHit

	switch n.Kind {
	case yaml.MappingNode:
		for _, e := range mapEntries(n, path) {
			if e.node.Kind == yaml.ScalarNode {
				if isHardcodedSecret(e.key, e.node.Value, inHeaders) {
					out = append(out, secretHit{node: e.node, path: e.path})
				}

				continue
			}

			out = append(out, findSecrets(e.node, e.path, inHeaders || strings.EqualFold(e.key, headersKey))...)
		}
	case yaml.SequenceNode:
		for i, item := range n.Content {
			out = append(out, findSecrets(item, indexPath(path, i), inHeaders)...)
		}
	default:
		// Scalars are matched by their key, above. An alias is the anchor it
		// points at, which is walked where it is written, so following it here
		// would report the same value twice.
	}

	return out
}

// isHardcodedSecret reports whether a setting names a credential and holds one
// in full.
func isHardcodedSecret(key, value string, inHeaders bool) bool {
	// Either the key says the value is a credential, or a header value says so
	// itself by opening with an authentication scheme.
	named := namesCredential(key, inHeaders) || (inHeaders && carriesAuthScheme(value))
	if !named {
		return false
	}

	// An expansion -- ${env:TOKEN}, ${file:/run/secrets/token} and the rest of
	// the family -- is the config doing exactly what it should, and a
	// placeholder is a config with no credential in it yet.
	return !hasExpansion(value) && !isPlaceholder(value)
}

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
	// keys and key_file are ordinary settings across the collector.
	for _, name := range []string{
		"password", "passphrase", "token", "secret",
		"apikey", "api_key", "access_key", "private_key",
	} {
		if strings.Contains(key, name) {
			return true
		}
	}

	return false
}

// carriesAuthScheme reports whether a header value opens with an HTTP
// authentication scheme, which is the shape a credential takes even when the
// header is spelled something other than authorization. The schemes are
// case-insensitive in the protocol, so they are matched that way here.
func carriesAuthScheme(value string) bool {
	for _, scheme := range []string{"bearer ", "basic "} {
		if len(value) >= len(scheme) && strings.EqualFold(value[:len(scheme)], scheme) {
			return true
		}
	}

	return false
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
	case "none", "null", "nil", "unset", "empty", "todo", "fixme", "tbd",
		"changeme", "changeit", "replaceme", "placeholder", "example", "dummy", "test",
		"secret", "password", "token", "apikey", "xxx":
		return true
	}

	// yourtoken, your-api-key, YOUR_PASSWORD_HERE.
	return strings.HasPrefix(norm, "your")
}
