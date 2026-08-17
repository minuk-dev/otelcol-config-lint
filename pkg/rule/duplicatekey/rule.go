// Package duplicatekey reports a mapping key written twice.
package duplicatekey

import (
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// New builds the rule.
func New() rule.Rule {
	return duplicateKey{rule.NewBase("duplicate-key",
		"a mapping key declared twice silently discards the first value", diag.Error)}
}

type duplicateKey struct{ rule.Base }

func (r duplicateKey) Check(ctx *rule.Context) {
	for _, e := range ctx.File.DuplicateKeys {
		ctx.Report(rule.Finding{
			Node: e.KeyNode, Path: e.Path,
			Message: "duplicate key " + rule.Quote(e.Key) + "; the earlier value is discarded",
		})
	}
}
