// Package insecuretls reports TLS verification disabled on a component that
// talks over the network.
package insecuretls

import (
	"strings"

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
			for _, hit := range findInsecure(ctx, c.ValueNode, kind.Section()+"."+c.ID.String()) {
				finding := rule.Finding{
					Node: hit.node, Path: hit.path,
					Message: rule.Quote(rule.ShortPath(hit.path)) + " disables TLS verification for " +
						string(kind) + " " + rule.Quote(c.ID.String()),
					Hint: "configure trusted CA certificates and enable verification outside local testing",
					Docs: rule.TLSDocs,
				}
				if hit.hasCA {
					finding.Message += " despite a configured ca_file"
					finding.Hint = "remove insecure_skip_verify or set it to false to verify the server " +
						"using the configured CA; the CA file is still loaded while verification is skipped"
				}

				ctx.Report(finding)
			}
		}
	}
}

type tlsHit struct {
	node  *yaml.Node
	path  string
	hasCA bool
}

// findInsecure walks a component's settings for tls.insecure or
// tls.insecure_skip_verify set to true, at any nesting depth. Exporters bury
// these under sub-blocks such as auth or queue settings.
func findInsecure(ctx *rule.Context, n *yaml.Node, path string) []tlsHit {
	if n == nil {
		return nil
	}

	var out []tlsHit

	switch n.Kind {
	case yaml.MappingNode:
		for _, e := range rule.MapEntries(n, path) {
			switch e.Key {
			case "insecure", "insecure_skip_verify":
				var insecure bool

				val := ctx.DecodeScalar(e.Node, "bool")

				if val.Tag == rule.BoolTag && val.Decode(&insecure) == nil && insecure {
					out = append(out, tlsHit{node: e.Node, path: e.Path, hasCA: e.Key == "insecure_skip_verify" && configuredCA(n)})
				}
			default:
				out = append(out, findInsecure(ctx, e.Node, e.Path)...)
			}
		}
	case yaml.SequenceNode:
		for i, item := range n.Content {
			out = append(out, findInsecure(ctx, item, rule.IndexPath(path, i))...)
		}
	default:
		// Scalars and aliases hold no settings of their own.
	}

	return out
}

func configuredCA(node *yaml.Node) bool {
	ca, _ := rule.ChildNode(node, "ca_file")
	ca = rule.ResolveAlias(ca)

	return ca != nil && ca.Kind == yaml.ScalarNode && ca.Tag == "!!str" &&
		strings.TrimSpace(ca.Value) != "" && !rule.HasExpansion(ca.Value)
}
