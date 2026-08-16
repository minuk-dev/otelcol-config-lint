package rule

import (
	"net"
	"strings"

	"github.com/samber/lo"
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

		// An endpoint nobody wrote takes the extension's own default, which
		// upstream already binds to localhost. One built from a confmap
		// expansion is only an address once the collector starts, and
		// scalarChild leaves both of those unread.
		node, written := scalarChild(c.ValueNode, endpointKey).Get()
		if !written {
			continue
		}

		r.report(ctx, c, ext, node)
	}
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
	host := endpointHost(endpoint)
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

// endpointHost returns the address an endpoint listens on, without its port.
// An endpoint written with no port at all is a host on its own, and so is a
// bracketed IPv6 address that SplitHostPort will not take.
func endpointHost(endpoint string) string {
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		return strings.Trim(endpoint, "[]")
	}

	return host
}
