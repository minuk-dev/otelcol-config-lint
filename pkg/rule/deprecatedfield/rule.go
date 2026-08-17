// Package deprecatedfield reports a setting upstream has replaced.
package deprecatedfield

import (
	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// New builds the rule.
func New() rule.Rule {
	return deprecatedField{rule.NewBase("deprecated-field",
		"a setting upstream has replaced", diag.Warning)}
}

type deprecatedField struct{ rule.Base }

func (r deprecatedField) Check(ctx *rule.Context) {
	rule.FieldWalker{
		Ctx: ctx,
		OnDeprecated: func(key string, node *yaml.Node, path, note string) {
			ctx.Report(rule.Finding{
				Node: node, Path: path,
				Message: "setting " + rule.Quote(key) + " is deprecated",
				Hint:    note,
			})
		},
		OnUnknown:  nil,
		OnRequired: nil,
		OnInvalid:  nil,
	}.WalkComponents()
}
