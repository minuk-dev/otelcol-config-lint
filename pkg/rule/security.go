package rule

import (
	"net"
	"regexp"
	"strings"

	"github.com/samber/lo"
	"github.com/samber/mo"
	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
)

// securityRules check what a config hands to the world around it: the network
// it sits on, and the repository it lives in. They are separate from the
// practice rules because the cost of getting one wrong is not a slower
// collector but a reachable one, or a credential somebody else now has.
func securityRules() []Rule {
	return []Rule{
		insecureTLS{base{"insecure-tls",
			"TLS verification disabled on a component that talks over the network", diag.Warning}},
		debugExtensionExposed{base{"debug-extension-exposed",
			"a debugging extension bound where more than this host can reach it", diag.Warning}},
		receiverBindsAllInterfaces{base{"receiver-binds-all-interfaces",
			"a component listening on every network interface the host has", diag.Warning}},
		hardcodedSecret{base{"hardcoded-secret",
			"a credential written into the config instead of expanded at runtime", diag.Warning}},
	}
}

// headersKey is the map whose keys are HTTP header names rather than settings,
// and where a credential is most often written out in full.
const headersKey = "headers"

type insecureTLS struct{ base }

func (r insecureTLS) Check(ctx *Context) {
	for _, kind := range config.Kinds() {
		sec := ctx.File.Sections[kind]
		if sec == nil {
			continue
		}

		for _, c := range sec.Components {
			for _, hit := range findInsecure(c.ValueNode, kind.Section()+"."+c.ID.String()) {
				ctx.Report(Finding{
					Node: hit.node, Path: hit.path,
					Message: quote(shortPath(hit.path)) + " disables TLS verification for " +
						string(kind) + " " + quote(c.ID.String()),
					Hint: "supply ca_file/cert_file instead of skipping verification outside local testing",
					Docs: tlsDocs,
				})
			}
		}
	}
}

type tlsHit struct {
	node *yaml.Node
	path string
}

// findInsecure walks a component's settings for tls.insecure or
// tls.insecure_skip_verify set to true, at any nesting depth. Exporters bury
// these under sub-blocks such as auth or queue settings.
func findInsecure(n *yaml.Node, path string) []tlsHit {
	if n == nil {
		return nil
	}

	var out []tlsHit

	switch n.Kind {
	case yaml.MappingNode:
		for _, e := range mapEntries(n, path) {
			switch e.key {
			case "insecure", "insecure_skip_verify":
				if e.node.Tag == boolTag && e.node.Value == "true" {
					out = append(out, tlsHit{node: e.node, path: e.path})
				}
			default:
				out = append(out, findInsecure(e.node, e.path)...)
			}
		}
	case yaml.SequenceNode:
		for i, item := range n.Content {
			out = append(out, findInsecure(item, indexPath(path, i))...)
		}
	default:
		// Scalars and aliases hold no settings of their own.
	}

	return out
}

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
			for _, hit := range findSecrets(c.ValueNode, kind.Section()+"."+c.ID.String(), "", false) {
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
		for _, e := range mapEntries(n, path) {
			out = append(out,
				findSecrets(e.node, e.path, e.key, inHeaders || strings.EqualFold(e.key, headersKey))...)
		}
	case yaml.SequenceNode:
		for i, item := range n.Content {
			out = append(out, findSecrets(item, indexPath(path, i), key, inHeaders)...)
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
// hasExpansion is the general test and the right one for a schema check, but it
// also matches the bare $VAR shorthand anywhere inside a string -- and a
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

// endpointKey is the setting a component that listens says its address in.
const endpointKey = "endpoint"

// debugExtension is an extension that serves the collector's own internals,
// together with the phrase a finding uses to say what it hands out. Being
// specific is the point: "exposed" says nothing, "heap profiles" says why it
// matters.
type debugExtension struct {
	typ    string
	serves string
}

// debugExtensions lists the extensions that exist to reveal the process. Both
// are matched on type, so "zpages/internal" is covered too.
//
// health_check and healthcheckv2 are deliberately absent. A Kubernetes
// liveness probe is issued by the kubelet, which reaches the container from
// off its loopback interface, so a health check bound to 0.0.0.0 is what a
// correct deployment looks like rather than a mistake. A rule that fires on
// every correct config is a rule people disable, and a disabled rule reports
// nothing about pprof either.
//
// It returns a fresh slice so callers cannot alter what everyone else sees.
func debugExtensions() []debugExtension {
	return []debugExtension{
		{typ: "pprof", serves: "heap profiles, goroutine dumps and the collector's command line"},
		{typ: "zpages", serves: "live traces and the collector's pipeline internals"},
	}
}

// exposure says how far an endpoint reaches.
type exposure int

const (
	// exposureUnknown is an address this linter will not place: a hostname
	// other than localhost. What a name resolves to is a property of the
	// network the collector runs on, not of the file, so nothing is reported.
	exposureUnknown exposure = iota
	// exposureLoopback reaches no further than the host itself.
	exposureLoopback
	// exposureUnspecified is 0.0.0.0, [::] or a bare ":port": every interface
	// the host has, including the ones it did not mean to serve on.
	exposureUnspecified
	// exposureRoutable is one specific address that is not loopback. Someone
	// chose it, so the finding is a note rather than a correction.
	exposureRoutable
)

// debugExtensionExposed reports a pprof or zpages endpoint reachable from off
// the host. Upstream's security guidance is to avoid exposing health or
// telemetry data outside the collector by default, and these two extensions
// are the ones that do it: a 0.0.0.0 OTLP receiver accepts data it should not,
// while a 0.0.0.0 pprof endpoint hands out process internals.
//
// receiver-binds-all-interfaces is the general bind-address check, and it skips
// the types listed in debugExtensions, which this rule owns and reports with a
// sharper message.
type debugExtensionExposed struct{ base }

func (r debugExtensionExposed) Check(ctx *Context) {
	sec := ctx.File.Sections[config.KindExtension]
	if sec == nil {
		return
	}

	for _, c := range sec.Components {
		ext, isDebug := lo.Find(debugExtensions(), func(e debugExtension) bool { return e.typ == c.ID.Type })
		if !isDebug {
			continue
		}

		// service.extensions is the only thing that starts an extension, so a
		// declaration left out of it opens no listener at all. Reporting the
		// address anyway would be a confident claim about a port nothing
		// binds, and unused-component already has something to say about the
		// declaration itself.
		if !ctx.Index.Enabled(c.ID) {
			continue
		}

		// An endpoint nobody wrote takes the extension's own default. Both of
		// these have defaulted to localhost since upstream made
		// UseLocalHostAsDefaultHost the default in v0.110, so silence is the
		// right answer for any release worth targeting; against a release
		// older than that, where the default was 0.0.0.0, this rule is quiet
		// about an endpoint that is in fact exposed.
		node, written := endpointNode(c.ValueNode).Get()
		if !written {
			continue
		}

		r.report(ctx, c, ext, node)
	}
}

// endpointNode returns the scalar holding a debugging extension's endpoint.
//
// Unlike an extension name, an endpoint that carries a confmap expansion is
// still worth reading: "0.0.0.0:${env:PPROF_PORT}" parameterises the port and
// leaves the address written plainly, and the address is the whole question
// here. scalarChild is therefore the wrong reader for it.
func endpointNode(settings *yaml.Node) mo.Option[*yaml.Node] {
	settings = resolveAlias(settings)
	if settings == nil || settings.Kind != yaml.MappingNode {
		return mo.None[*yaml.Node]()
	}

	var merges []*yaml.Node

	for _, e := range mapEntries(settings, "") {
		switch e.key {
		case endpointKey:
			// A key the component writes itself replaces a merged one
			// outright, so there is nothing further to look at.
			return endpointScalar(e.node)
		case "<<":
			merges = append(merges, e.node)
		default:
			// Every other setting is another rule's business.
		}
	}

	return mergedEndpoint(merges)
}

// mergedEndpoint reads the endpoint a "<<" merge supplies to a component that
// wrote none of its own. The value of a merge is one mapping or a list of
// them, and the first that holds the key wins, which is what the merge does at
// load time.
func mergedEndpoint(merges []*yaml.Node) mo.Option[*yaml.Node] {
	for _, merge := range merges {
		merge = resolveAlias(merge)
		if merge == nil {
			continue
		}

		targets := []*yaml.Node{merge}
		if merge.Kind == yaml.SequenceNode {
			targets = merge.Content
		}

		for _, target := range targets {
			if node, held := childNode(target, endpointKey).Get(); held {
				return endpointScalar(node)
			}
		}
	}

	return mo.None[*yaml.Node]()
}

// endpointScalar takes an endpoint's value node when it holds text to read.
func endpointScalar(n *yaml.Node) mo.Option[*yaml.Node] {
	n = resolveAlias(n)
	if n == nil || n.Kind != yaml.ScalarNode || n.Value == "" {
		return mo.None[*yaml.Node]()
	}

	return mo.Some(n)
}

func (r debugExtensionExposed) report(ctx *Context, c config.Component, ext debugExtension, node *yaml.Node) {
	path := joinPath(config.KindExtension.Section()+"."+c.ID.String(), endpointKey)
	subject := "extension " + quote(c.ID.String()) + " binds " + quote(node.Value) + ", serving " + ext.serves

	switch classifyEndpoint(node.Value) {
	case exposureUnspecified:
		ctx.Report(Finding{
			Node: node, Path: path,
			Message: subject + " on every interface of the host",
			Hint: "bind it to localhost, and reach it through a port-forward when you need it; upstream's advice " +
				"is not to expose telemetry or debugging data outside the collector by default",
			Docs: securityDocs,
		})
	case exposureRoutable:
		ctx.Report(Finding{
			Node: node, Path: path, Severity: diag.Info,
			Message: subject + " to that network",
			Hint: "binding a specific address is deliberate, but every host that can route to it reads the " +
				"collector's internals; localhost is the safe default",
			Docs: securityDocs,
		})
	case exposureLoopback, exposureUnknown:
		// Loopback is the answer this rule is asking for, and a hostname it
		// cannot place is not evidence of anything.
	}
}

// classifyEndpoint places the host part of an endpoint. The port does not
// matter here; who can open a connection to it does.
func classifyEndpoint(endpoint string) exposure {
	host, known := endpointHost(endpoint).Get()
	if !known {
		return exposureUnknown
	}

	if host == "" {
		return exposureUnspecified // a bare ":1777" listens everywhere
	}

	if ip := net.ParseIP(host); ip != nil {
		switch {
		case ip.IsUnspecified():
			return exposureUnspecified
		case ip.IsLoopback():
			return exposureLoopback
		default:
			return exposureRoutable
		}
	}

	// The one hostname that means the same thing on every machine.
	if strings.EqualFold(host, "localhost") {
		return exposureLoopback
	}

	return exposureUnknown
}

// expansionMask stands in for a confmap expansion while an endpoint is split
// into host and port. An expansion carries colons of its own -- ${env:PORT} --
// which SplitHostPort cannot see past; one opaque token in its place splits
// where the finished endpoint will. It is a byte no endpoint can contain.
const expansionMask = "\x00"

// endpointHost returns the address an endpoint listens on, without its port.
// It returns nothing when the address is only known once the collector starts,
// which is not the same as the port being: "0.0.0.0:${env:PPROF_PORT}" says
// exactly who can reach it, and only the port is left open.
func endpointHost(endpoint string) mo.Option[string] {
	masked := expansionRE.ReplaceAllString(bracketed(endpoint), expansionMask)

	host, _, err := net.SplitHostPort(masked)
	if err != nil {
		// An endpoint written with no port at all is a host on its own, and so
		// is a bracketed IPv6 address that SplitHostPort will not take.
		host = strings.Trim(masked, "[]")
	}

	if strings.Contains(host, expansionMask) {
		return mo.None[string]()
	}

	return mo.Some(host)
}

// endpointPort returns the port an endpoint names, so a suggestion can keep it
// while changing the address in front of it. It returns nothing when the
// endpoint names no port, or leaves it to an expansion: the port only ever ends
// up in a hint, so one that cannot be read costs a little precision there and
// nothing anywhere else.
func endpointPort(endpoint string) mo.Option[string] {
	masked := expansionRE.ReplaceAllString(bracketed(endpoint), expansionMask)

	_, port, err := net.SplitHostPort(masked)
	if err != nil || strings.Contains(port, expansionMask) {
		return mo.None[string]()
	}

	return mo.Some(port)
}

// bracketed rewrites the one endpoint spelling net.SplitHostPort refuses:
// ":::4317", the IPv6 unspecified address written without the brackets that
// tell its colons from the port's. It is what netstat prints for an IPv6
// listener, so it gets copied into configs from there.
//
// Go refuses it outright -- "too many colons in address" -- so a collector
// handed one does not start at all, and reading it as "[::]:4317" keeps the
// finding about the address whoever wrote it meant. Binding a specific
// interface is the fix for both halves of that at once.
func bracketed(endpoint string) string {
	if rest, found := strings.CutPrefix(endpoint, ":::"); found {
		return "[::]:" + rest
	}

	return endpoint
}

// bindKeys are the settings a component writes the address it listens on in.
// endpoint is what most of them use, and what the field schemas describe a
// server's address as. The stanza-based log receivers are the exception:
// tcplog and udplog spell it listen_address, and syslog carries one under each
// of its tcp and udp blocks. Reading only endpoint would leave every one of
// those unreported, which is why the walk is over two key names rather than
// one.
//
// It returns a fresh slice so callers cannot alter what everyone else sees.
func bindKeys() []string { return []string{endpointKey, "listen_address"} }

// probeExtensions are the extensions a correct deployment binds to every
// interface on purpose. A Kubernetes liveness probe is issued by the kubelet,
// which reaches the container from off its loopback interface, so a health
// check on localhost is the configuration that fails. This is the same
// exclusion debugExtensions documents, for the same reason.
func probeExtensions() []string { return []string{"health_check", "healthcheckv2"} }

// receiverBindsAllInterfaces reports an endpoint bound to the unspecified
// address: 0.0.0.0, [::], or a bare ":4317", all of which listen on every
// interface the host has rather than the one whoever wrote it had in mind.
// Upstream changed the collector's own default to localhost in v0.110.0 for
// exactly this reason, but the example configs the ecosystem copies from -- the
// ones on opentelemetry.io say outright that they use 0.0.0.0 "as a
// convenience" -- still carry it into production.
//
// Only server-side endpoints are read, and the split is by kind: a receiver or
// an extension listens on its endpoint, while an exporter's or a connector's
// names somewhere to send to. 0.0.0.0 as a destination is also wrong, but it is
// a different mistake with a different fix, and reporting it here would put a
// bind-address hint on a line about a backend.
//
// The severity is Warning rather than Error. A gateway behind a service mesh
// with its own network policy binds every interface deliberately, and so does
// anything that has to be reachable from another pod without the downward API
// to hand.
type receiverBindsAllInterfaces struct{ base }

func (r receiverBindsAllInterfaces) Check(ctx *Context) {
	for _, kind := range []config.Kind{config.KindReceiver, config.KindExtension} {
		sec := ctx.File.Sections[kind]
		if sec == nil {
			continue
		}

		for _, c := range sec.Components {
			if r.skip(ctx, kind, c) {
				continue
			}

			// The nesting varies: otlp writes its endpoints under
			// protocols.grpc and protocols.http, syslog under tcp and udp, and
			// most other components write one at the top. Walking for the key
			// covers all of them without a table of where every component keeps
			// it, at the cost of matching an address nested under something
			// else -- which, in a section holding only things that listen, is a
			// price worth paying.
			walkSettings(c.ValueNode, kind.Section()+"."+c.ID.String(), 0, func(m *yaml.Node, path string) {
				for _, key := range bindKeys() {
					node, written := childNode(m, key).Get()
					if !written {
						continue
					}

					if scalar, readable := endpointScalar(node).Get(); readable {
						r.report(ctx, kind, c, scalar, joinPath(path, key))
					}
				}
			})
		}
	}
}

// skip reports the components this rule has nothing to say about: the ones
// another rule owns, the ones binding every interface correctly, and the ones
// that never open a listener at all.
func (r receiverBindsAllInterfaces) skip(ctx *Context, kind config.Kind, c config.Component) bool {
	if kind == config.KindExtension {
		if lo.ContainsBy(debugExtensions(), func(e debugExtension) bool { return e.typ == c.ID.Type }) {
			return true // debug-extension-exposed says something sharper about these
		}

		if contains(probeExtensions(), c.ID.Type) {
			return true
		}

		// service.extensions is the only thing that starts an extension, so a
		// declaration left out of it opens no listener; unused-component has
		// something to say about the declaration itself.
		return !ctx.Index.Enabled(c.ID)
	}

	// A receiver no pipeline names is never instantiated, and binds nothing.
	return !ctx.Index.Used(kind, c.ID)
}

func (r receiverBindsAllInterfaces) report(
	ctx *Context, kind config.Kind, c config.Component, node *yaml.Node, path string,
) {
	if classifyEndpoint(node.Value) != exposureUnspecified {
		return
	}

	ctx.Report(Finding{
		Node: node, Path: path,
		Message: quote(shortPath(path)) + " binds " + quote(node.Value) +
			", every interface the host has, for " + string(kind) + " " + quote(c.ID.String()),
		Hint: bindHint(endpointPort(node.Value)),
		Docs: configSecurityDocs,
	})
}

// bindHint names the fix upstream documents rather than only saying not to do
// it: an interface chosen on purpose, and in Kubernetes the pod's own IP, which
// the downward API supplies and which reaches other pods without also reaching
// everything else the node is on.
func bindHint(port mo.Option[string]) string {
	loopback, pod := "127.0.0.1", "${env:MY_POD_IP}"
	if p, known := port.Get(); known {
		loopback, pod = loopback+":"+p, pod+":"+p
	}

	return "bind the interface you meant, e.g. " + loopback + " when every client is local; " +
		"in Kubernetes write " + pod + ", the pod IP the downward API supplies"
}
