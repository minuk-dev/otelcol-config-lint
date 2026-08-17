// Package unusedcomponent reports a declaration nothing instantiates.
package unusedcomponent

import (
	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// New builds the rule.
func New() rule.Rule {
	return unusedComponent{rule.NewBase("unused-component",
		"a declared component that no pipeline references is never instantiated", diag.Warning)}
}

type unusedComponent struct{ rule.Base }

func (r unusedComponent) Check(ctx *rule.Context) {
	// Without a service block every component is unused; service-required
	// already says so, and repeating it per component is only noise.
	if ctx.File.Service.Node == nil {
		return
	}

	for _, kind := range config.Kinds() {
		sec := ctx.File.Sections[kind]
		if sec == nil {
			continue
		}

		for _, c := range sec.Components {
			if ctx.Index.Used(kind, c.ID) {
				continue
			}

			// An extension is wired up by being listed, not by being
			// referenced from a pipeline, so it needs its own wording.
			missing := "referenced by no pipeline"
			if kind == config.KindExtension {
				missing = "not listed in service.extensions"
			}

			ctx.Report(rule.Finding{
				Node: c.KeyNode, Path: kind.Section() + "." + c.ID.String(),
				Message: string(kind) + " " + rule.Quote(c.ID.String()) + " is declared but " + missing,
				Hint:    "remove it, or reference it so it actually runs",
			})
		}
	}
}
