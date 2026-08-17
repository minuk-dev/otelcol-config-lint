// Package receiverbindsallinterfaces reports an endpoint bound to every
// interface the host has.
package receiverbindsallinterfaces

import (
	"slices"

	"github.com/samber/lo"
	"github.com/samber/mo"
	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// bindKeys are the settings a component writes the address it listens on in.
// endpoint is what most of them use, and what the field schemas describe a
// server's address as. The stanza-based log receivers are the exception:
// tcplog and udplog spell it listen_address, and syslog carries one under each
// of its tcp and udp blocks. Reading only endpoint would leave every one of
// those unreported, which is why the walk is over two key names rather than
// one.
//
// It returns a fresh slice so callers cannot alter what everyone else sees.
func bindKeys() []string { return []string{rule.EndpointKey, "listen_address"} }

// probeExtensions are the extensions a correct deployment binds to every
// interface on purpose. A Kubernetes liveness probe is issued by the kubelet,
// which reaches the container from off its loopback interface, so a health
// check on localhost is the configuration that fails. This is the same
// exclusion rule.DebugExtensions documents, for the same reason.
func probeExtensions() []string { return []string{"health_check", "healthcheckv2"} }

// New builds the rule.
//
// The unspecified address is 0.0.0.0, [::], or a bare ":4317", all of which
// listen on every interface the host has rather than the one whoever wrote it
// had in mind. Upstream changed the collector's own default to localhost in
// v0.110.0 for exactly this reason, but the example configs the ecosystem
// copies from -- the ones on opentelemetry.io say outright that they use
// 0.0.0.0 "as a convenience" -- still carry it into production.
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
func New() rule.Rule {
	return receiverBindsAllInterfaces{rule.NewBase("receiver-binds-all-interfaces",
		"a component listening on every network interface the host has", diag.Warning)}
}

type receiverBindsAllInterfaces struct{ rule.Base }

func (r receiverBindsAllInterfaces) Check(ctx *rule.Context) {
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
			rule.WalkSettings(c.ValueNode, kind.Section()+"."+c.ID.String(), 0,
				func(m *yaml.Node, path string) {
					r.reportBinds(ctx, kind, c, m, path)
				})
		}
	}
}

// reportBinds reads the bind addresses one mapping writes, wherever in the
// component's settings it sits.
func (r receiverBindsAllInterfaces) reportBinds(
	ctx *rule.Context, kind config.Kind, c config.Component, m *yaml.Node, path string,
) {
	for _, key := range bindKeys() {
		node, written := rule.ChildNode(m, key).Get()
		if !written {
			continue
		}

		if scalar, readable := rule.EndpointScalar(node).Get(); readable {
			r.report(ctx, kind, c, scalar, rule.JoinPath(path, key))
		}
	}
}

// skip reports the components this rule has nothing to say about: the ones
// another rule owns, the ones binding every interface correctly, and the ones
// that never open a listener at all.
func (r receiverBindsAllInterfaces) skip(ctx *rule.Context, kind config.Kind, c config.Component) bool {
	if kind == config.KindExtension {
		if lo.ContainsBy(rule.DebugExtensions(), func(e rule.DebugExtension) bool { return e.Type == c.ID.Type }) {
			return true // debug-extension-exposed says something sharper about these
		}

		if slices.Contains(probeExtensions(), c.ID.Type) {
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
	ctx *rule.Context, kind config.Kind, c config.Component, node *yaml.Node, path string,
) {
	if rule.ClassifyEndpoint(node.Value) != rule.ExposureUnspecified {
		return
	}

	ctx.Report(rule.Finding{
		Node: node, Path: path,
		Message: rule.Quote(rule.ShortPath(path)) + " binds " + rule.Quote(node.Value) +
			", every interface the host has, for " + string(kind) + " " + rule.Quote(c.ID.String()),
		Hint: bindHint(rule.EndpointPort(node.Value)),
		Docs: rule.ConfigSecurityDocs,
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
