// Package telemetrymetricsdisabled reports a config that turns off the
// collector's own metrics.
package telemetrymetricsdisabled

import (
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// levelKey is the setting that decides how much the collector reports about
// itself, and noneLevel is the value that reports nothing.
const (
	levelKey  = "level"
	noneLevel = "none"
)

// New builds the rule.
func New() rule.Rule {
	return telemetryMetricsDisabled{rule.NewBase("telemetry-metrics-disabled",
		"the collector's own metrics are turned off", diag.Info)}
}

type telemetryMetricsDisabled struct{ rule.Base }

func (r telemetryMetricsDisabled) Check(ctx *rule.Context) {
	e, written := ctx.File.Service.Telemetry.Metrics.Setting(levelKey)
	if !written || !disabled(e.Node) {
		return
	}

	ctx.Report(rule.Finding{
		Node: e.Node, Path: e.Path,
		Message: "service.telemetry.metrics." + levelKey + " is " + rule.Quote(noneLevel) +
			", so the collector reports no metrics about itself",
		Hint: "there are reasons to run without them, but they are the metrics that say the " +
			"collector is dropping data -- otelcol_exporter_send_failed_* and the queue sizes among " +
			"them; \"basic\" is the level that keeps those",
		Docs: rule.InternalTelemetryDocs,
	})
}

// disabled reports a level that turns the metrics off. The value is folded to
// lower case, as configtelemetry's own decoder folds it, so "None" is the same
// level to the collector. A value only the collector can resolve -- an
// expansion, or one a merge key supplies -- is read as not disabled: a finding
// quoting a level nobody wrote would be a guess.
func disabled(n *yaml.Node) bool {
	n = rule.ResolveAlias(n)
	if n == nil || n.Kind != yaml.ScalarNode || rule.HasExpansion(n.Value) {
		return false
	}

	return strings.EqualFold(strings.TrimSpace(n.Value), noneLevel)
}
