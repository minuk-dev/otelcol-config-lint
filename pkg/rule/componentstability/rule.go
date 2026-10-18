// Package componentstability reports a component below beta stability.
package componentstability

import (
	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
	"github.com/minuk-dev/otelcol-config-lint/pkg/sets"
)

// New builds the rule.
func New() rule.Rule {
	return componentStability{Base: rule.NewBase("component-stability",
		"components below beta stability can change or break without notice", diag.Info), reportAlpha: true}
}

type componentStability struct {
	rule.Base

	reportAlpha bool
}

func (r componentStability) Check(ctx *rule.Context) {
	if !ctx.SchemaReady() {
		return
	}

	rule.EachUsedComponent(ctx, func(c config.Component, comp *schema.Component) {
		r.report(ctx, c, comp)
	})
}

// report says one thing about one component, for the first signal it is wired
// into that has a stability worth mentioning.
func (r componentStability) report(ctx *rule.Context, c config.Component, comp *schema.Component) {
	for _, s := range usedSignals(ctx, c) {
		level, ok := comp.StabilityFor(s)
		if !ok {
			continue
		}

		subject := string(c.Kind) + " " + rule.Quote(c.ID.Type) + forSignal(s)
		path := c.Kind.Section() + "." + c.ID.String()

		switch level {
		case schema.Development:
			ctx.Report(rule.Finding{
				Node: c.KeyNode, Path: path,
				Message:  subject + " is in development and may change without notice",
				Severity: diag.Warning,
			})
		case schema.Alpha:
			if !r.reportAlpha {
				continue
			}

			ctx.Report(rule.Finding{
				Node: c.KeyNode, Path: path,
				Message: subject + " is alpha; its configuration can change between releases",
			})
		default:
			// Beta and above are what a config should be built on, and
			// deprecated-component covers the other end.
			continue
		}

		return // one finding per component is enough
	}
}

// usedSignals returns the signals a component is wired into, so stability can
// be reported for the signals that matter. Extensions have none, so they get a
// single empty signal to keep callers simple.
func usedSignals(ctx *rule.Context, c config.Component) []config.Signal {
	if c.Kind == config.KindExtension {
		return []config.Signal{""}
	}

	var out []config.Signal

	seen := sets.New[config.Signal]()

	for _, p := range ctx.File.Service.Pipelines {
		for _, slot := range []config.Kind{config.KindReceiver, config.KindProcessor, config.KindExporter} {
			for _, ref := range p.Refs(slot) {
				if ref.ID == c.ID && !seen.Has(p.Signal) {
					seen.Insert(p.Signal)
					out = append(out, p.Signal)
				}
			}
		}
	}

	return out
}

// forSignal renders the " for traces" clause, which extensions do not have.
func forSignal(s config.Signal) string {
	if s == "" {
		return ""
	}

	return " for " + string(s)
}
