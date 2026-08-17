// Package requiredfield reports a setting the component cannot start without.
package requiredfield

import (
	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// New builds the rule.
func New() rule.Rule {
	return requiredField{rule.NewBase("required-field",
		"a setting the component cannot start without", diag.Error)}
}

type requiredField struct{ rule.Base }

func (r requiredField) Check(ctx *rule.Context) {
	rule.FieldWalker{
		Ctx: ctx,
		OnRequired: func(key string, node *yaml.Node, path string) {
			ctx.Report(rule.Finding{
				Node: node, Path: path,
				Message: "missing required setting " + rule.Quote(key) + " for " + rule.ComponentOf(path),
			})
		},
		OnUnknown:    nil,
		OnInvalid:    nil,
		OnDeprecated: nil,
	}.WalkComponents()
}
