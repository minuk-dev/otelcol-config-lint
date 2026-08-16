package rule

import (
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// practiceRules check for configurations that load but behave badly.
func practiceRules() []Rule {
	return []Rule{
		processorOrder{base{"processor-order",
			"memory_limiter must run first so backpressure is applied before any work", diag.Warning}},
		missingMemoryLimiter{base{"missing-memory-limiter",
			"a pipeline without memory_limiter can be OOM-killed under load", diag.Info}},
		missingBatch{base{"missing-batch",
			"exporting without batching costs throughput and export requests", diag.Info}},
		noPersistentQueue{base{"no-persistent-queue",
			"a sending queue held in memory, which a restart drops along with everything in it", diag.Info}},
		debugExporterVerbosity{base{"debug-exporter-verbosity",
			"a debug exporter left in a pipeline, writing the telemetry it receives to the log", diag.Warning}},
	}
}

// memoryLimiter, batch and debug are matched on type, so instances such as
// "memory_limiter/aggressive" are recognised too.
const (
	memoryLimiterType = "memory_limiter"
	batchType         = "batch"
	debugType         = "debug"
)

// verbosityKey is the debug exporter's setting for how much of every record it
// writes, and detailedLevel the value that writes all of it.
const (
	verbosityKey  = "verbosity"
	detailedLevel = "detailed"
)

// sendingQueueKey is the exporter setting holding the queue, and storageKey the
// field inside it that makes the queue persistent.
const (
	sendingQueueKey = "sending_queue"
	storageKey      = "storage"
)

type processorOrder struct{ base }

func (r processorOrder) Check(ctx *Context) {
	for _, p := range ctx.File.Service.Pipelines {
		path := "service.pipelines." + p.Key + ".processors"
		for i, ref := range p.Processors {
			if ref.ID.Type == memoryLimiterType && i != 0 {
				ctx.Report(Finding{
					Node: ref.Node, Path: ref.Path,
					Message: "memory_limiter is processor " + itoa(i+1) + " in pipeline " + quote(p.Key) +
						"; it must be first",
					Hint: "move " + quote(ref.ID.String()) + " to the front of " + path,
					Docs: memoryLimiterDocs,
				})
			}

			if ref.ID.Type == batchType && i < len(p.Processors)-1 {
				if next := p.Processors[i+1]; next.ID.Type == memoryLimiterType {
					continue // reported above
				}

				ctx.Report(Finding{
					Node: ref.Node, Path: ref.Path,
					Message:  "batch runs before other processors in pipeline " + quote(p.Key),
					Hint:     "put batch last so upstream processors see individual items and cannot drop whole batches",
					Docs:     batchDocs,
					Severity: diag.Info,
				})
			}
		}
	}
}

type missingMemoryLimiter struct{ base }

func (r missingMemoryLimiter) Check(ctx *Context) {
	for _, p := range ctx.File.Service.Pipelines {
		if hasProcessorType(p, memoryLimiterType) || len(p.Receivers) == 0 {
			continue
		}

		ctx.Report(Finding{
			Node: nodeOr(p.ProcessorsNode, p.KeyNode), Path: "service.pipelines." + p.Key + ".processors",
			Message: "pipeline " + quote(p.Key) + " has no memory_limiter processor",
			Hint:    "add memory_limiter as the first processor to bound the collector's memory use",
			Docs:    memoryLimiterDocs,
		})
	}
}

type missingBatch struct{ base }

func (r missingBatch) Check(ctx *Context) {
	for _, p := range ctx.File.Service.Pipelines {
		if hasProcessorType(p, batchType) || len(p.Exporters) == 0 {
			continue
		}

		ctx.Report(Finding{
			Node: nodeOr(p.ProcessorsNode, p.KeyNode), Path: "service.pipelines." + p.Key + ".processors",
			Message: "pipeline " + quote(p.Key) + " has no batch processor",
			Hint:    "add batch before the exporters to reduce the number of outgoing requests",
			Docs:    batchDocs,
		})
	}
}

func hasProcessorType(p config.Pipeline, typ string) bool {
	for _, ref := range p.Processors {
		if ref.ID.Type == typ {
			return true
		}
	}

	return false
}

// noPersistentQueue reports an exporter whose sending queue lives in memory.
// The queue is on by default, so nothing in the config says the data in it is
// held only in the process: an exporter with a storage extension and one
// without look identical, right up until the restart that empties one of them.
//
// It reports once per exporter rather than once per config. The fix is written
// per exporter -- each queue names its own storage -- and a finding without a
// position cannot say which of eight exporters to edit. That the count of
// findings then matches the count of edits is the point; a config with eight
// exporters and no persistence really does have eight of these.
//
// The severity is the other half of that. This is a design note, not a defect:
// a sidecar next to an application that can re-send, or an agent whose source
// keeps its own buffer, is right not to want a writable volume. diag.Info is
// the ceiling, so a run at --min-severity warning -- the usual way to run --
// never sees it, and --disable no-persistent-queue turns it off for good.
type noPersistentQueue struct{ base }

func (r noPersistentQueue) Check(ctx *Context) {
	sec := ctx.File.Sections[config.KindExporter]
	if sec == nil {
		return
	}

	for _, c := range sec.Components {
		// An exporter no pipeline reaches is never instantiated, so it has no
		// queue to lose; unused-component is what has something to say about it.
		if !ctx.Index.Used(config.KindExporter, c.ID) {
			continue
		}

		queue := readSendingQueue(c)

		if queue.disabled || queue.persisted || queue.unresolved || !acceptsQueue(ctx, c.ID.Type, queue.written) {
			continue
		}

		path := config.KindExporter.Section() + "." + c.ID.String()

		ctx.Report(Finding{
			Node: nodeOr(queue.node, c.KeyNode), Path: joinPath(path, sendingQueueKey),
			Message: "exporter " + quote(c.ID.String()) + " " + queue.describe() +
				"; a restart drops whatever is still queued",
			Hint: "persistence takes three steps: declare a storage extension such as file_storage, " +
				"list it in service.extensions so the collector starts it, and name it here as " +
				sendingQueueKey + "." + storageKey,
			Docs: exporterQueueDocs,
		})
	}
}

// sendingQueue is what an exporter's settings say about its queue.
type sendingQueue struct {
	// node is the sending_queue block, or nil when the key was not written.
	node *yaml.Node
	// written reports that the exporter configures a queue at all. The queue
	// runs either way; this only decides how the finding reads.
	written bool
	// disabled reports enabled: false, which is an exporter saying it wants no
	// queue. There is then nothing to lose on a restart.
	disabled bool
	// persisted reports that storage names something. A confmap expansion
	// counts: it resolves to an extension the linter cannot see, not to
	// nothing. Only an empty or null value is nobody naming anything.
	persisted bool
	// unresolved reports that the document does not settle the question here: a
	// merge key that may be what supplies storage, or an enabled the collector
	// only learns once an expansion resolves. Either way the queue read here is
	// not necessarily the queue that runs, and a finding would be about a
	// config nobody wrote.
	unresolved bool
}

// describe renders what the exporter did, so a written queue missing one field
// does not read like a queue nobody configured.
func (q sendingQueue) describe() string {
	if q.written {
		return "has a " + sendingQueueKey + " with no " + storageKey +
			", so the queue is held in memory"
	}

	return "takes the default " + sendingQueueKey + ", which is held in memory"
}

// readSendingQueue reads an exporter's queue settings. Only the top level is
// read, which is where the queue sits and where the field schema describes it:
// an exporter that wraps another, such as loadbalancing, carries a queue per
// protocol whose fix is written somewhere else again.
func readSendingQueue(c config.Component) sendingQueue {
	queue := sendingQueue{node: nil, written: false, disabled: false, persisted: false, unresolved: false}

	settings := resolveAlias(c.ValueNode)
	if settings == nil || settings.Kind != yaml.MappingNode {
		return queue
	}

	var block *yaml.Node

	for _, e := range mapEntries(settings, "") {
		switch e.key {
		case sendingQueueKey:
			queue.node, queue.written, block = e.node, true, resolveAlias(e.node)
		case "<<":
			// The merge may be what supplies the whole block.
			queue.unresolved = true
		default:
			// Every other setting is another rule's business.
		}
	}

	if block == nil || block.Kind != yaml.MappingNode {
		return queue
	}

	// A merge outside the block cannot reach into one the exporter writes
	// itself: a key present locally replaces the merged value outright. Only
	// what is inside the block can still leave the queue unsettled.
	queue.unresolved = false
	readQueueBlock(block, &queue)

	return queue
}

// readQueueBlock reads the two settings that decide whether there is a queue at
// all, and whether it survives a restart.
func readQueueBlock(block *yaml.Node, queue *sendingQueue) {
	for _, e := range mapEntries(block, "") {
		val := resolveAlias(e.node)
		if val == nil {
			continue
		}

		switch e.key {
		case "enabled":
			// An expansion is read the same way here as under storage: as a
			// value this rule cannot see rather than one nobody wrote. A
			// variable that resolves to false is an exporter with no queue,
			// and a finding about the queue it lost would be about a config
			// the collector never runs.
			if val.Kind == yaml.ScalarNode && hasExpansion(val.Value) {
				queue.unresolved = true
			}

			queue.disabled = val.Tag == boolTag && val.Value == "false"
		case storageKey:
			// Anything but an empty or null value is the setting written.
			// Whether what was written is a name the collector can resolve is
			// invalid-value's and undefined-extension-reference's to say.
			queue.persisted = !isNull(val) && (val.Kind != yaml.ScalarNode || val.Value != "")
		case "<<":
			queue.unresolved = true
		default:
			// Every other queue setting is the field schema's business.
		}
	}
}

// acceptsQueue reports whether the exporter type has a sending queue at all.
// Plenty do not -- debug and nop among them -- and telling them they lose a
// queue they never had is worse than saying nothing.
//
// The field schema is the authority wherever it describes the type's settings
// completely, which is what keeps debug quiet. Where it does not, a queue the
// config writes is the only evidence there is. That covers three shapes:
// no schema was resolved at all, the type is not in it, and the type is in it
// with no fields -- which reads as "nothing could be resolved for this
// component" rather than "this component has no settings", since the datadog
// exporter, which has a queue and a great many other settings, sits in that
// bucket next to nop. A queue written under an exporter that really has none
// is then reported here as well as by unknown-field, which is the price of not
// staying silent about datadog.
func acceptsQueue(ctx *Context, typ string, written bool) bool {
	fields := exporterFields(ctx, typ)
	if fields == nil || fields.Open || len(fields.Children) == 0 {
		return written
	}

	_, held := fields.Children[sendingQueueKey]

	return held
}

// exporterFields returns the targeted release's field schema for an exporter
// type, or nil when there is none to consult.
func exporterFields(ctx *Context, typ string) *schema.Field {
	if !ctx.schemaReady() {
		return nil
	}

	comp, ok := ctx.Schema.Lookup(config.KindExporter, typ)
	if !ok {
		return nil
	}

	return comp.Fields
}

// debugExporterVerbosity reports a debug exporter a pipeline still references.
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
type debugExporterVerbosity struct{ base }

func (r debugExporterVerbosity) Check(ctx *Context) {
	for _, p := range ctx.File.Service.Pipelines {
		for _, ref := range p.Exporters {
			if ref.ID.Type != debugType {
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
func (r debugExporterVerbosity) finding(p config.Pipeline, ref config.Ref, detailed bool) Finding {
	exporters := "service.pipelines." + p.Key + ".exporters"

	if !detailed {
		return Finding{
			Node: ref.Node, Path: ref.Path, Severity: diag.Info,
			Message: "exporter " + quote(ref.ID.String()) + " writes the telemetry of pipeline " +
				quote(p.Key) + " to the collector's log",
			Hint: "the exporter is for diagnosing a pipeline rather than for running one; " +
				"remove it from " + exporters + " once the diagnosis is done, and do not parse what it " +
				"prints, whose format upstream does not keep stable",
			Docs: debugDocs,
		}
	}

	return Finding{
		Node: ref.Node, Path: ref.Path,
		Message: "exporter " + quote(ref.ID.String()) + " runs at " + verbosityKey + ": " + detailedLevel +
			" in pipeline " + quote(p.Key) + ", logging every record it receives",
		Hint: "set " + verbosityKey + ": basic, or remove the exporter from " + exporters + "; " +
			"sampling_initial and sampling_thereafter bound the rate if it has to stay at " + detailedLevel,
		Docs: debugDocs,
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
	settings := resolveAlias(c.ValueNode)
	if settings == nil || settings.Kind != yaml.MappingNode {
		return false
	}

	for _, e := range mapEntries(settings, "") {
		if e.key != verbosityKey {
			continue
		}

		val := resolveAlias(e.node)
		if val == nil || val.Kind != yaml.ScalarNode {
			return false
		}

		return strings.EqualFold(val.Value, detailedLevel)
	}

	return false
}
