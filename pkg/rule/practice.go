package rule

import (
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
		insecureTLS{base{"insecure-tls",
			"TLS verification disabled on a component that talks over the network", diag.Warning}},
		noPersistentQueue{base{"no-persistent-queue",
			"a sending queue held in memory, which a restart drops along with everything in it", diag.Info}},
	}
}

// memoryLimiter and batch are matched on type, so instances such as
// "memory_limiter/aggressive" are recognised too.
const (
	memoryLimiterType = "memory_limiter"
	batchType         = "batch"
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
					Node: ref.Node, Path: "service." + ref.Path,
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
					Node: ref.Node, Path: "service." + ref.Path,
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

type insecureTLS struct{ base }

func (r insecureTLS) Check(ctx *Context) {
	for _, kind := range config.Kinds() {
		sec := ctx.File.Sections[kind]
		if sec == nil {
			continue
		}

		for _, c := range sec.Components {
			for _, hit := range findInsecure(c.ValueNode, kind.Section()+"."+c.ID.String()) {
				ctx.Report(Finding{
					Node: hit.node, Path: hit.path,
					Message: quote(shortPath(hit.path)) + " disables TLS verification for " +
						string(kind) + " " + quote(c.ID.String()),
					Hint: "supply ca_file/cert_file instead of skipping verification outside local testing",
					Docs: tlsDocs,
				})
			}
		}
	}
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

		if queue.disabled || queue.persisted || queue.merged || !acceptsQueue(ctx, c.ID.Type, queue.written) {
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
	// merged reports a YAML merge key among the settings the queue is read
	// from. A merge may be what supplies storage, and reporting a queue the
	// document does not fully spell out would name a setting that is there.
	merged bool
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
	queue := sendingQueue{node: nil, written: false, disabled: false, persisted: false, merged: false}

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
			queue.merged = true
		default:
			// Every other setting is another rule's business.
		}
	}

	if block == nil || block.Kind != yaml.MappingNode {
		return queue
	}

	// A merge outside the block cannot reach into one the exporter writes
	// itself: a key present locally replaces the merged value outright. Only
	// what is inside the block can still supply storage.
	queue.merged = false
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
			queue.disabled = val.Tag == boolTag && val.Value == "false"
		case storageKey:
			// Anything but an empty or null value is the setting written.
			// Whether what was written is a name the collector can resolve is
			// invalid-value's and undefined-extension-reference's to say.
			queue.persisted = !isNull(val) && (val.Kind != yaml.ScalarNode || val.Value != "")
		case "<<":
			queue.merged = true
		default:
			// Every other queue setting is the field schema's business.
		}
	}
}

// acceptsQueue reports whether the exporter type has a sending queue at all.
// Plenty do not -- debug, nop and every exporter that writes its own client --
// and telling them they lose a queue they never had is worse than saying
// nothing.
//
// The field schema is the authority wherever it describes the type's settings
// completely. Where it does not -- no schema was resolved, the type is not in
// it, or its settings are left open because upstream hands them to a
// third-party config -- a queue the config writes is the only evidence there is,
// and a block the component turns out not to accept is unknown-field's to
// report.
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

type tlsHit struct {
	node *yaml.Node
	path string
}

// findInsecure walks a component's settings for tls.insecure or
// tls.insecure_skip_verify set to true, at any nesting depth. Exporters bury
// these under sub-blocks such as auth or queue settings.
func findInsecure(n *yaml.Node, path string) []tlsHit {
	if n == nil {
		return nil
	}

	var out []tlsHit

	switch n.Kind {
	case yaml.MappingNode:
		for _, e := range mapEntries(n, path) {
			switch e.key {
			case "insecure", "insecure_skip_verify":
				if e.node.Tag == boolTag && e.node.Value == "true" {
					out = append(out, tlsHit{node: e.node, path: e.path})
				}
			default:
				out = append(out, findInsecure(e.node, e.path)...)
			}
		}
	case yaml.SequenceNode:
		for i, item := range n.Content {
			out = append(out, findInsecure(item, indexPath(path, i))...)
		}
	default:
		// Scalars and aliases hold no settings of their own.
	}

	return out
}
