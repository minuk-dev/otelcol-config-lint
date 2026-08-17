package rule

import (
	"net"
	"strings"

	"github.com/samber/mo"
	"gopkg.in/yaml.v3"
)

// Exposure says how far an endpoint reaches.
type Exposure int

const (
	// ExposureUnknown is an address this linter will not place: a hostname
	// other than localhost. What a name resolves to is a property of the
	// network the collector runs on, not of the file, so nothing is reported.
	ExposureUnknown Exposure = iota
	// ExposureLoopback reaches no further than the host itself.
	ExposureLoopback
	// ExposureUnspecified is 0.0.0.0, [::] or a bare ":port": every interface
	// the host has, including the ones it did not mean to serve on.
	ExposureUnspecified
	// ExposureRoutable is one specific address that is not loopback. Someone
	// chose it, so the finding is a note rather than a correction.
	ExposureRoutable
)

// expansionMask stands in for a confmap expansion while an endpoint is split
// into host and port. An expansion carries colons of its own -- ${env:PORT} --
// which SplitHostPort cannot see past; one opaque token in its place splits
// where the finished endpoint will. It is a byte no endpoint can contain.
const expansionMask = "\x00"

// DebugExtension is an extension that serves the collector's own internals,
// together with the phrase a finding uses to say what it hands out. Being
// specific is the point: "exposed" says nothing, "heap profiles" says why it
// matters.
type DebugExtension struct {
	// Type is the component type, matched without the instance name.
	Type string
	// Serves names what the extension hands to whoever reaches it.
	Serves string
}

// DebugExtensions lists the extensions that exist to reveal the process. Both
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
func DebugExtensions() []DebugExtension {
	return []DebugExtension{
		{Type: "pprof", Serves: "heap profiles, goroutine dumps and the collector's command line"},
		{Type: "zpages", Serves: "live traces and the collector's pipeline internals"},
	}
}

// EndpointScalar takes an endpoint's value node when it holds text to read.
//
// Unlike an extension name, an endpoint that carries a confmap expansion is
// still worth reading: "0.0.0.0:${env:PPROF_PORT}" parameterises the port and
// leaves the address written plainly, and the address is the whole question
// these rules ask. ScalarChild is therefore the wrong reader for it.
func EndpointScalar(n *yaml.Node) mo.Option[*yaml.Node] {
	n = ResolveAlias(n)
	if n == nil || n.Kind != yaml.ScalarNode || n.Value == "" {
		return mo.None[*yaml.Node]()
	}

	return mo.Some(n)
}

// ClassifyEndpoint places the host part of an endpoint. The port does not
// matter here; who can open a connection to it does.
func ClassifyEndpoint(endpoint string) Exposure {
	host, known := EndpointHost(endpoint).Get()
	if !known {
		return ExposureUnknown
	}

	if host == "" {
		return ExposureUnspecified // a bare ":1777" listens everywhere
	}

	if ip := net.ParseIP(host); ip != nil {
		switch {
		case ip.IsUnspecified():
			return ExposureUnspecified
		case ip.IsLoopback():
			return ExposureLoopback
		default:
			return ExposureRoutable
		}
	}

	// The one hostname that means the same thing on every machine.
	if strings.EqualFold(host, "localhost") {
		return ExposureLoopback
	}

	return ExposureUnknown
}

// EndpointHost returns the address an endpoint listens on, without its port.
// It returns nothing when the address is only known once the collector starts,
// which is not the same as the port being: "0.0.0.0:${env:PPROF_PORT}" says
// exactly who can reach it, and only the port is left open.
func EndpointHost(endpoint string) mo.Option[string] {
	masked := MaskExpansions(bracketed(endpoint), expansionMask)

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

// EndpointPort returns the port an endpoint names, so a suggestion can keep it
// while changing the address in front of it. It returns nothing when the
// endpoint names no port, or leaves it to an expansion: the port only ever ends
// up in a hint, so one that cannot be read costs a little precision there and
// nothing anywhere else.
func EndpointPort(endpoint string) mo.Option[string] {
	masked := MaskExpansions(bracketed(endpoint), expansionMask)

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
