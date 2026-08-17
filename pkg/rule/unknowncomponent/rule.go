// Package unknowncomponent reports a component type the targeted release does
// not have.
package unknowncomponent

import (
	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// New builds the rule.
func New() rule.Rule {
	return unknownComponent{rule.NewBase("unknown-component",
		"a component type that does not exist in the targeted collector release", diag.Error)}
}

type unknownComponent struct{ rule.Base }

func (r unknownComponent) Check(ctx *rule.Context) {
	if !ctx.SchemaReady() {
		return
	}

	for _, kind := range config.Kinds() {
		sec := ctx.File.Sections[kind]
		if sec == nil {
			continue
		}

		for _, c := range sec.Components {
			if _, ok := ctx.Schema.Lookup(kind, c.ID.Type); ok {
				continue
			}

			ctx.Report(rule.Finding{
				Node: c.KeyNode, Path: kind.Section() + "." + c.ID.String(),
				Message: "unknown " + string(kind) + " type " + rule.Quote(c.ID.Type) + " in " + ctx.Version(),
				Hint:    hint(ctx, kind, c.ID.Type),
			})
		}
	}
}

// hint explains an unknown component: it may be declared under the wrong
// section, ship only in other distributions, exist only in other releases, or
// simply be a typo.
func hint(ctx *rule.Context, kind config.Kind, typ string) string {
	for _, other := range config.Kinds() {
		if other == kind {
			continue
		}

		if _, ok := ctx.Schema.Lookup(other, typ); ok {
			return rule.Quote(typ) + " is " + rule.Article(string(other)) + " " + string(other) +
				"; declare it under " + other.Section()
		}
	}

	// A component the running binary does not ship is a different problem from
	// a typo: the fix is switching distributions, not correcting the name.
	if ctx.Dists != nil && ctx.Schema.Distribution != "" {
		if dists := ctx.Dists.Distributions(kind, typ); len(dists) > 0 {
			return rule.Quote(typ) + " is not in the " + ctx.Schema.Distribution +
				" distribution; it ships in " + rule.List(dists)
		}
	}

	if ctx.Avail != nil {
		if versions := ctx.Avail.Versions(kind, typ); len(versions) > 0 {
			return rule.Quote(typ) + " exists in " + rule.List(versions) +
				" but not in " + ctx.Schema.CollectorVersion
		}
	}

	return "checked against " + ctx.Version() + rule.Suggest(typ, ctx.Schema.Types(kind))
}
