// Package deprecatedtelemetrykey reports a service.telemetry key the collector
// has stopped reading.
package deprecatedtelemetrykey

import (
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// addressKey is the setting this rule is about, and ignoredFrom is the release
// that stopped reading it. Upstream replaced it with readers, and a collector
// from that release on serves its metrics where the readers say -- so a config
// still carrying the key is not refused, it is simply obeyed somewhere else.
const (
	addressKey  = "address"
	ignoredFrom = "v0.123.0"
)

// New builds the rule.
func New() rule.Rule {
	return deprecatedTelemetryKey{rule.NewBase("deprecated-telemetry-key",
		"a service.telemetry key the collector no longer reads", diag.Warning)}
}

type deprecatedTelemetryKey struct{ rule.Base }

func (r deprecatedTelemetryKey) Check(ctx *rule.Context) {
	// On an older release the key still works, and a run that resolved no
	// schema has no release to judge against; both stay quiet rather than
	// reporting a config that is right for what it targets.
	if !ctx.SchemaReady() || schema.Compare(ctx.Schema.CollectorVersion, ignoredFrom) < 0 {
		return
	}

	e, written := ctx.File.Service.Telemetry.Metrics.Setting(addressKey)
	if !written {
		return
	}

	ctx.Report(rule.Finding{
		Node: e.KeyNode, Path: e.Path,
		Message: "setting " + rule.Quote(addressKey) + " in service.telemetry.metrics is ignored from " +
			"collector " + ignoredFrom + " onward",
		Hint: "its host and port move into a pull reader: " +
			"readers: [{pull: {exporter: {prometheus: {host: localhost, port: 8888}}}}]; " +
			"as written, the collector serves its metrics where the readers say and not here",
		Docs: rule.InternalTelemetryDocs,
	})
}
