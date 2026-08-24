// Package debugextensionexposed reports a pprof or zpages endpoint reachable
// from off the host.
package debugextensionexposed

import (
	"github.com/samber/lo"
	"github.com/samber/mo"
	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// New builds the rule.
//
// Upstream's security guidance is to avoid exposing health or telemetry data
// outside the collector by default, and these two extensions are the ones that
// do it: a 0.0.0.0 OTLP receiver accepts data it should not, while a 0.0.0.0
// pprof endpoint hands out process internals.
//
// receiver-binds-all-interfaces is the general bind-address check, and it skips
// the types rule.DebugExtensions lists, which this rule owns and reports with a
// sharper message.
func New() rule.Rule {
	return debugExtensionExposed{rule.NewBase("debug-extension-exposed",
		"a debugging extension bound where more than this host can reach it", diag.Warning)}
}

type debugExtensionExposed struct{ rule.Base }

func (r debugExtensionExposed) Check(ctx *rule.Context) {
	sec := ctx.File.Sections[config.KindExtension]
	if sec == nil {
		return
	}

	for _, c := range sec.Components {
		ext, isDebug := lo.Find(rule.DebugExtensions(), func(e rule.DebugExtension) bool {
			return e.Type == c.ID.Type
		})
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

func (r debugExtensionExposed) report(
	ctx *rule.Context, c config.Component, ext rule.DebugExtension, node *yaml.Node,
) {
	path := rule.JoinPath(config.KindExtension.Section()+"."+c.ID.String(), rule.EndpointKey)
	subject := "extension " + rule.Quote(c.ID.String()) + " binds " + rule.Quote(node.Value) +
		", serving " + ext.Serves

	switch rule.ClassifyEndpoint(node.Value) {
	case rule.ExposureUnspecified:
		ctx.Report(rule.Finding{
			Node: node, Path: path,
			Message: subject + " on every interface of the host",
			Hint: "bind it to localhost, and reach it through a port-forward when you need it; upstream's advice " +
				"is not to expose telemetry or debugging data outside the collector by default",
			Docs: rule.SecurityDocs,
		})
	case rule.ExposureRoutable:
		ctx.Report(rule.Finding{
			Node: node, Path: path, Severity: diag.Info,
			Message: subject + " to that network",
			Hint: "binding a specific address is deliberate, but every host that can route to it reads the " +
				"collector's internals; localhost is the safe default",
			Docs: rule.SecurityDocs,
		})
	case rule.ExposureLoopback, rule.ExposureUnknown:
		// Loopback is the answer this rule is asking for, and a hostname it
		// cannot place is not evidence of anything.
	}
}

// endpointNode returns the scalar holding a debugging extension's endpoint.
func endpointNode(settings *yaml.Node) mo.Option[*yaml.Node] {
	settings = rule.ResolveAlias(settings)
	if settings == nil || settings.Kind != yaml.MappingNode {
		return mo.None[*yaml.Node]()
	}

	node, held := rule.ChildNode(settings, rule.EndpointKey).Get()
	if !held {
		return mo.None[*yaml.Node]()
	}

	return rule.EndpointScalar(node)
}
