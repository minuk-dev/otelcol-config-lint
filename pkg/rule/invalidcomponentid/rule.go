// Package invalidcomponentid reports malformed component declarations and references.
package invalidcomponentid

import (
	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// New builds the rule.
func New() rule.Rule {
	return invalidComponentID{rule.NewBase("invalid-component-id",
		"component identifiers must be <type> or <type>/<nonempty-name>", diag.Error)}
}

type invalidComponentID struct{ rule.Base }

func (r invalidComponentID) Check(ctx *rule.Context) {
	for _, kind := range config.Kinds() {
		if sec := ctx.File.Sections[kind]; sec != nil {
			for _, c := range sec.Components {
				check(ctx, c.KeyNode, kind.Section()+"."+c.KeyNode.Value)
			}
		}
	}

	for _, ref := range ctx.File.Service.Extensions {
		check(ctx, ref.Node, ref.Path)
	}

	for _, p := range ctx.File.Service.Pipelines {
		for _, kind := range []config.Kind{config.KindReceiver, config.KindProcessor, config.KindExporter} {
			for _, ref := range p.Refs(kind) {
				check(ctx, ref.Node, ref.Path)
			}
		}
	}

	for _, ref := range ctx.Index.ExtensionRefs() {
		check(ctx, ref.Node, ref.Path)
	}
}

func check(ctx *rule.Context, node *yaml.Node, path string) {
	if reason := rule.IdentifierError(node.Value); reason != "" {
		ctx.Report(rule.Finding{
			Node: node, Path: path,
			Message: "invalid component identifier " + rule.Quote(node.Value) + ": " + reason,
		})
	}
}
