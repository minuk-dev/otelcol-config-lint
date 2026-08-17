// Package wrongnodetype reports a section or pipeline slot written in the
// wrong shape.
package wrongnodetype

import (
	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// New builds the rule.
func New() rule.Rule {
	return wrongNodeType{rule.NewBase("wrong-node-type",
		"component sections must be mappings and pipeline slots must be lists", diag.Error)}
}

type wrongNodeType struct{ rule.Base }

func (r wrongNodeType) Check(ctx *rule.Context) {
	r.checkSections(ctx)
	r.checkService(ctx)
}

// checkSections reports a component section written as anything but a mapping
// of component id to settings.
func (r wrongNodeType) checkSections(ctx *rule.Context) {
	for _, kind := range config.Kinds() {
		sec := ctx.File.Sections[kind]
		if sec == nil || rule.IsNull(sec.Node) || sec.Node.Kind == yaml.MappingNode {
			continue
		}

		ctx.Report(rule.Finding{
			Node: sec.Node, Path: kind.Section(),
			Message: kind.Section() + " must be a mapping of component id to settings, got " +
				rule.NodeKind(sec.Node),
		})
	}
}

// checkService reports the lists inside the service block written as something
// else: the extensions it enables, and the three slots of every pipeline.
func (r wrongNodeType) checkService(ctx *rule.Context) {
	svc := ctx.File.Service
	if svc.ExtensionsNode != nil && !rule.IsNull(svc.ExtensionsNode) && svc.ExtensionsNode.Kind != yaml.SequenceNode {
		ctx.Report(rule.Finding{
			Node: svc.ExtensionsNode, Path: "service.extensions",
			Message: "service.extensions must be a list, got " + rule.NodeKind(svc.ExtensionsNode),
		})
	}

	for _, p := range svc.Pipelines {
		for _, slot := range []struct {
			name string
			node *yaml.Node
		}{
			{"receivers", p.ReceiversNode},
			{"processors", p.ProcessorsNode},
			{"exporters", p.ExportersNode},
		} {
			if slot.node == nil || rule.IsNull(slot.node) || slot.node.Kind == yaml.SequenceNode {
				continue
			}

			ctx.Report(rule.Finding{
				Node: slot.node, Path: "service.pipelines." + p.Key + "." + slot.name,
				Message: slot.name + " must be a list, got " + rule.NodeKind(slot.node),
				Hint:    "write it as a YAML sequence, e.g. " + slot.name + ": [otlp]",
			})
		}
	}
}
