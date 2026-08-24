// Package undefinedextensionreference reports an extension a component's own
// settings name, that nothing declares or starts.
package undefinedextensionreference

import (
	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// New builds the rule.
//
// It checks the references a component writes into its own settings, which
// undefined-reference never sees: that rule walks the service block, and these
// sit several levels down inside a component. Getting one wrong is a startup
// failure, and the collector's own error does not say which of the three places
// is missing the name.
func New() rule.Rule {
	return undefinedExtensionReference{rule.NewBase("undefined-extension-reference",
		"an extension a component's own settings name, that nothing declares or starts", diag.Error)}
}

type undefinedExtensionReference struct{ rule.Base }

func (r undefinedExtensionReference) Check(ctx *rule.Context) {
	for _, ref := range ctx.Index.ExtensionRefs() {
		subject := string(ref.Component.Kind) + " " + rule.Quote(ref.Component.ID.String()) +
			" references " + ref.Subject() + " " + rule.Quote(ref.ID.String())

		if _, declared := ctx.Index.Declared(config.KindExtension, ref.ID); !declared {
			// A name declared under another section lands here too; the hint
			// is what tells that apart from a name nothing declares at all.
			ctx.Report(rule.Finding{
				Node: ref.Node, Path: ref.Path,
				Message: subject + " which is not declared under extensions",
				Hint:    rule.MisplacedHint(ctx, ref.ID, config.KindExtension),
				Docs:    ref.Docs,
			})

			continue
		}

		// Without a service block nothing is enabled at all, and
		// service-required already says so.
		if ctx.File.Service.Node == nil || ctx.Index.Enabled(ref.ID) {
			continue
		}

		ctx.Report(rule.Finding{
			Node: ref.Node, Path: ref.Path,
			Message: subject + " which is declared but missing from service.extensions, " +
				"so the collector never starts it",
			Hint: "add " + rule.Quote(ref.ID.String()) + " to service.extensions",
			Docs: ref.Docs,
		})
	}
}
