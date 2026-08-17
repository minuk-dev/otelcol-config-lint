// Package debugexporterverbosity reports a debug exporter a pipeline still
// references.
package debugexporterverbosity

import (
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// verbosityKey is the debug exporter's setting for how much of every record it
// writes, and detailedLevel the value that writes all of it.
const (
	verbosityKey  = "verbosity"
	detailedLevel = "detailed"
)

// New builds the rule.
//
// The exporter prints what it is given to the collector's own log, and at
// verbosity: detailed that is every field of every span, data point and record
// flowing through the pipeline. In production it is a self-inflicted denial of
// service: the collector spends its time formatting log lines, and the log
// backend receives a second copy of all the telemetry the collector was meant
// to be forwarding.
//
// It gets there the ordinary way. Someone adds debug to find out why an export
// is failing, finds the answer, and the exporter stays in the pipeline list.
// That is also why the rule reports at every verbosity, one level down: an
// exporter at the default basic costs a line per batch rather than per record,
// which is cheap enough not to be a warning, but it is still a diagnostic tool
// left running, and its output format is documented as unstable, so nothing
// downstream should be reading it either.
//
// It reports per pipeline that references the exporter, since that is what the
// message names and what the reader removes it from. An exporter no pipeline
// references is never instantiated and prints nothing; unused-component is what
// has something to say about that one.
func New() rule.Rule {
	return debugExporterVerbosity{rule.NewBase("debug-exporter-verbosity",
		"a debug exporter left in a pipeline, writing the telemetry it receives to the log", diag.Warning)}
}

type debugExporterVerbosity struct{ rule.Base }

func (r debugExporterVerbosity) Check(ctx *rule.Context) {
	for _, p := range ctx.File.Service.Pipelines {
		for _, ref := range p.Exporters {
			if ref.ID.Type != rule.DebugType {
				continue
			}

			// A pipeline naming an exporter nobody declared is
			// undefined-reference's to report, and there are no settings to
			// read a verbosity out of.
			c, declared := ctx.File.Component(config.KindExporter, ref.ID)
			if !declared {
				continue
			}

			ctx.Report(r.finding(p, ref, detailedDebug(c)))
		}
	}
}

// finding renders one referenced debug exporter. Both clauses are anchored at
// the reference rather than at the settings block, so a debug exporter shared
// by three pipelines reports in the three places it is wired in rather than
// three times on one line.
func (r debugExporterVerbosity) finding(p config.Pipeline, ref config.Ref, detailed bool) rule.Finding {
	exporters := "service.pipelines." + p.Key + ".exporters"

	if !detailed {
		return rule.Finding{
			Node: ref.Node, Path: ref.Path, Severity: diag.Info,
			Message: "exporter " + rule.Quote(ref.ID.String()) + " writes the telemetry of pipeline " +
				rule.Quote(p.Key) + " to the collector's log",
			Hint: "the exporter is for diagnosing a pipeline rather than for running one; " +
				"remove it from " + exporters + " once the diagnosis is done, and do not parse what it " +
				"prints, whose format upstream does not keep stable",
			Docs: rule.DebugDocs,
		}
	}

	return rule.Finding{
		Node: ref.Node, Path: ref.Path,
		Message: "exporter " + rule.Quote(ref.ID.String()) + " runs at " + verbosityKey + ": " + detailedLevel +
			" in pipeline " + rule.Quote(p.Key) + ", logging every record it receives",
		Hint: "set " + verbosityKey + ": basic, or remove the exporter from " + exporters + "; " +
			"sampling_initial and sampling_thereafter bound the rate if it has to stay at " + detailedLevel,
		Docs: rule.DebugDocs,
	}
}

// detailedDebug reports an exporter that writes every field of every record.
// The value is folded to lower case, as configtelemetry's own decoder folds it:
// "Detailed" is the same level to the collector.
//
// A value only the collector can resolve -- a confmap expansion, or one a merge
// key supplies -- is read as not detailed. The note the rule reports either way
// is true of a debug exporter at any verbosity; a warning quoting a verbosity
// nobody wrote would not be.
func detailedDebug(c config.Component) bool {
	val, written := rule.ChildNode(c.ValueNode, verbosityKey).Get()
	if !written {
		return false
	}

	val = rule.ResolveAlias(val)
	if val == nil || val.Kind != yaml.ScalarNode {
		return false
	}

	return strings.EqualFold(val.Value, detailedLevel)
}
