// Package unknownfield reports a setting the component does not accept.
package unknownfield

import (
	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// New builds the rule.
func New() rule.Rule {
	return unknownField{rule.NewBase("unknown-field",
		"a setting the component does not accept; the collector rejects these", diag.Warning)}
}

type unknownField struct{ rule.Base }

func (r unknownField) Check(ctx *rule.Context) {
	sev := diag.Warning
	if ctx.Strict {
		sev = diag.Error
	}

	rule.FieldWalker{
		Ctx: ctx,
		OnUnknown: func(key string, node *yaml.Node, path string, known []string) {
			ctx.Report(rule.Finding{
				Node: node, Path: path, Severity: sev,
				Message: "unknown setting " + rule.Quote(key) + " for " + rule.ComponentOf(path),
				Hint:    "accepted settings: " + rule.List(known) + rule.Suggest(key, known),
			})
		},
		OnRequired:   nil,
		OnInvalid:    nil,
		OnDeprecated: nil,
	}.WalkComponents()
}
