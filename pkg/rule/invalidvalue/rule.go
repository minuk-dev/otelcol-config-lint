// Package invalidvalue reports a setting whose value has the wrong type or is
// outside the allowed set.
package invalidvalue

import (
	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// New builds the rule.
func New() rule.Rule {
	return invalidValue{rule.NewBase("invalid-value",
		"a setting whose value has the wrong type or is outside the allowed set", diag.Error)}
}

type invalidValue struct{ rule.Base }

func (r invalidValue) Check(ctx *rule.Context) {
	rule.FieldWalker{
		Ctx: ctx,
		OnInvalid: func(node *yaml.Node, path, want string) {
			ctx.Report(rule.Finding{
				Node: node, Path: path,
				Message: rule.Quote(rule.ShortPath(path)) + " must be " + want,
			})
		},
		OnUnknown:    nil,
		OnRequired:   nil,
		OnDeprecated: nil,
	}.WalkComponents()
}
