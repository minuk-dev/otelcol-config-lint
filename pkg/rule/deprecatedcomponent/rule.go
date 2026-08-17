// Package deprecatedcomponent reports a component upstream has marked as
// deprecated or unmaintained.
package deprecatedcomponent

import (
	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// New builds the rule.
func New() rule.Rule {
	return deprecatedComponent{rule.NewBase("deprecated-component",
		"a component upstream has marked as deprecated or unmaintained", diag.Warning)}
}

type deprecatedComponent struct{ rule.Base }

func (r deprecatedComponent) Check(ctx *rule.Context) {
	if !ctx.SchemaReady() {
		return
	}

	rule.EachUsedComponent(ctx, func(c config.Component, comp *schema.Component) {
		path := c.Kind.Section() + "." + c.ID.String()
		if comp.Deprecated != "" {
			ctx.Report(rule.Finding{
				Node: c.KeyNode, Path: path,
				Message: string(c.Kind) + " " + rule.Quote(c.ID.Type) + " is deprecated",
				Hint:    comp.Deprecated,
			})

			return
		}

		for _, level := range comp.Stability {
			if level == schema.Deprecated || level == schema.Unmaintained {
				ctx.Report(rule.Finding{
					Node: c.KeyNode, Path: path,
					Message: string(c.Kind) + " " + rule.Quote(c.ID.Type) + " is marked " +
						string(level) + " upstream",
					Hint: "plan a migration; it may be removed in a future release",
				})

				return
			}
		}
	})
}
