// Package nopersistentqueue reports an exporter whose sending queue lives in
// memory.
package nopersistentqueue

import (
	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// New builds the rule.
//
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
func New() rule.Rule {
	return noPersistentQueue{rule.NewBase("no-persistent-queue",
		"a sending queue held in memory, which a restart drops along with everything in it", diag.Info)}
}

type noPersistentQueue struct{ rule.Base }

func (r noPersistentQueue) Check(ctx *rule.Context) {
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

		if queue.disabled || queue.persisted || queue.unresolved ||
			!rule.AcceptsQueue(ctx, c.ID.Type, queue.written) {
			continue
		}

		path := config.KindExporter.Section() + "." + c.ID.String()

		ctx.Report(rule.Finding{
			Node: rule.NodeOr(queue.node, c.KeyNode), Path: rule.JoinPath(path, rule.SendingQueueKey),
			Message: "exporter " + rule.Quote(c.ID.String()) + " " + queue.describe() +
				"; a restart drops whatever is still queued",
			Hint: "persistence takes three steps: declare a storage extension such as file_storage, " +
				"list it in service.extensions so the collector starts it, and name it here as " +
				rule.SendingQueueKey + "." + rule.StorageKey,
			Docs: rule.ExporterQueueDocs,
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
		return "has a " + rule.SendingQueueKey + " with no " + rule.StorageKey +
			", so the queue is held in memory"
	}

	return "takes the default " + rule.SendingQueueKey + ", which is held in memory"
}

// readSendingQueue reads an exporter's queue settings. Only the top level is
// read, which is where the queue sits and where the field schema describes it:
// an exporter that wraps another, such as loadbalancing, carries a queue per
// protocol whose fix is written somewhere else again.
func readSendingQueue(c config.Component) sendingQueue {
	queue := sendingQueue{node: nil, written: false, disabled: false, persisted: false, unresolved: false}

	settings := rule.ResolveAlias(c.ValueNode)
	if settings == nil || settings.Kind != yaml.MappingNode {
		return queue
	}

	var block *yaml.Node

	for _, e := range rule.MapEntries(settings, "") {
		switch e.Key {
		case rule.SendingQueueKey:
			queue.node, queue.written, block = e.Node, true, rule.ResolveAlias(e.Node)
		case rule.MergeKey:
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
	for _, e := range rule.MapEntries(block, "") {
		val := rule.ResolveAlias(e.Node)
		if val == nil {
			continue
		}

		switch e.Key {
		case "enabled":
			// An expansion is read the same way here as under storage: as a
			// value this rule cannot see rather than one nobody wrote. A
			// variable that resolves to false is an exporter with no queue,
			// and a finding about the queue it lost would be about a config
			// the collector never runs.
			if val.Kind == yaml.ScalarNode && rule.HasExpansion(val.Value) {
				queue.unresolved = true
			}

			queue.disabled = val.Tag == rule.BoolTag && val.Value == "false"
		case rule.StorageKey:
			// Anything but an empty or null value is the setting written.
			// Whether what was written is a name the collector can resolve is
			// invalid-value's and undefined-extension-reference's to say.
			queue.persisted = !rule.IsNull(val) && (val.Kind != yaml.ScalarNode || val.Value != "")
		case rule.MergeKey:
			queue.unresolved = true
		default:
			// Every other queue setting is the field schema's business.
		}
	}
}
