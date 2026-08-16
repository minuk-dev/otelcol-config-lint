package rule

import (
	"net"
	"strings"

	"github.com/samber/lo"
	"github.com/samber/mo"
	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
)

// securityRules check what a config hands to the network around it. They are
// separate from the practice rules because the cost of getting one wrong is
// not a slower collector but a reachable one.
func securityRules() []Rule {
	return []Rule{
		debugExtensionExposed{base{"debug-extension-exposed",
			"a debugging extension bound where more than this host can reach it", diag.Warning}},
	}
}

// endpointKey is the setting every debugging extension listens on.
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
// The general bind-address checking that issue #45 describes covers receivers.
// Should it ever grow to cover extensions, it must skip the types listed in
// debugExtensions, which this rule owns and reports with a sharper message.
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
	masked := expansionRE.ReplaceAllString(endpoint, expansionMask)

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
