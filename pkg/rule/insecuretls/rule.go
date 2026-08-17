// Package insecuretls reports TLS verification disabled on a component that
// talks over the network.
package insecuretls

import (
	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// New builds the rule.
func New() rule.Rule {
	return insecureTLS{rule.NewBase("insecure-tls",
		"TLS verification disabled on a component that talks over the network", diag.Warning)}
}

type insecureTLS struct{ rule.Base }

func (r insecureTLS) Check(ctx *rule.Context) {
	for _, kind := range config.Kinds() {
		sec := ctx.File.Sections[kind]
		if sec == nil {
			continue
		}

		for _, c := range sec.Components {
			for _, hit := range findInsecure(c.ValueNode, kind.Section()+"."+c.ID.String()) {
				ctx.Report(rule.Finding{
					Node: hit.node, Path: hit.path,
					Message: rule.Quote(rule.ShortPath(hit.path)) + " disables TLS verification for " +
						string(kind) + " " + rule.Quote(c.ID.String()),
					Hint: "supply ca_file/cert_file instead of skipping verification outside local testing",
					Docs: rule.TLSDocs,
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
		for _, e := range rule.MapEntries(n, path) {
			switch e.Key {
			case "insecure", "insecure_skip_verify":
				if e.Node.Tag == rule.BoolTag && e.Node.Value == "true" {
					out = append(out, tlsHit{node: e.Node, path: e.Path})
				}
			default:
				out = append(out, findInsecure(e.Node, e.Path)...)
			}
		}
	case yaml.SequenceNode:
		for i, item := range n.Content {
			out = append(out, findInsecure(item, rule.IndexPath(path, i))...)
		}
	default:
		// Scalars and aliases hold no settings of their own.
	}

	return out
}
