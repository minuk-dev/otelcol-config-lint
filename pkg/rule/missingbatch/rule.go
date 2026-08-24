// Package missingbatch reports a pipeline that batches nothing on its way out.
package missingbatch

import (
	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// New builds the rule.
//
// The batch processor is one way to batch and no longer the only one:
// exporterhelper batches inside the sending queue, under sending_queue.batch,
// which sits behind the queue and the retry logic rather than in front of them.
// That is the difference the hint leads with. The processor drops the data in a
// batch that fails to send, upstream issue 12443, and the queue batcher does
// not; upstream is steering towards the latter and taking the processor out of
// its own documentation.
//
// It is not steering people away from the processor -- that has explicitly not
// been decided -- so a pipeline that uses it is still a pipeline that batches
// and nothing here says otherwise.
//
// A pipeline whose exporters all batch in their own queues gets no finding: it
// batches, and a batch processor added on top of it would be a second layer
// with a flush timing of its own. A pipeline where only some of them do is
// under-batched on the legs that do not, so the finding names the exporter
// rather than the pipeline.
//
// What the document writes decides whether there is a finding; what the field
// schema describes decides only which fix the hint may name. The two are read
// apart on purpose. An exporter the schema says nothing about -- datadog and
// nop are both in that bucket -- still has a queue batcher that is off until
// something writes it, so the finding stands; it is the hint that has to fall
// back to the processor, because nothing there says sending_queue.batch is a
// setting the release would accept.
func New() rule.Rule {
	return missingBatch{rule.NewBase("missing-batch",
		"exporting without batching costs throughput and export requests", diag.Info)}
}

type missingBatch struct{ rule.Base }

func (r missingBatch) Check(ctx *rule.Context) {
	for _, p := range ctx.File.Service.Pipelines {
		if rule.HasProcessorType(p, rule.BatchType) || len(p.Exporters) == 0 {
			continue
		}

		legs, unbatched := exportLegs(ctx, p)
		if len(unbatched) == 0 {
			continue
		}

		// Only when every leg is one this rule can read, and none of them
		// batches, is "the pipeline batches nothing" a claim the file supports.
		if len(unbatched) == legs {
			r.reportPipeline(ctx, p, unbatched)

			continue
		}

		r.reportLegs(ctx, p, unbatched)
	}
}

// reportPipeline says it once about a pipeline where nothing batches at all.
func (r missingBatch) reportPipeline(ctx *rule.Context, p config.Pipeline, unbatched []unbatchedLeg) {
	fix := pipelineFix(unbatched)

	ctx.Report(rule.Finding{
		Node: rule.NodeOr(p.ProcessorsNode, p.KeyNode),
		Path: "service.pipelines." + p.Key + ".processors",

		Message: "pipeline " + rule.Quote(p.Key) + " has no batch processor, and none of its exporters " +
			"batches in " + rule.SendingQueueKey,
		Hint: batchHint("the exporters", "these exporters have", fix),
		Docs: batchDocsFor(fix),
	})
}

// reportLegs names the exporters that do not batch, where the pipeline's others
// do.
func (r missingBatch) reportLegs(ctx *rule.Context, p config.Pipeline, unbatched []unbatchedLeg) {
	for _, leg := range unbatched {
		fix := exporterFix(leg.queueBatch)

		ctx.Report(rule.Finding{
			Node: leg.ref.Node, Path: leg.ref.Path,
			Message: "exporter " + rule.Quote(leg.ref.ID.String()) + " sends unbatched in pipeline " +
				rule.Quote(p.Key) + ", which has no batch processor",
			Hint: batchHint("this exporter", "this exporter has", fix),
			Docs: batchDocsFor(fix),
		})
	}
}

// batchFix is the batching a finding can tell the reader to write.
type batchFix int

const (
	// queueFix is sending_queue.batch, which every exporter the finding covers
	// has in the targeted release.
	queueFix batchFix = iota
	// processorFix is the batch processor, named where none of them has a queue
	// batcher, so the hint can say why it is the only batching on offer.
	processorFix
	// splitFix is the batch processor named where the exporters disagree: some
	// have a queue batcher and some do not, so the reason processorFix gives
	// would be wrong about half of them. It says that much instead, which is
	// what tells the reader the other fix is still open to some of these legs.
	splitFix
)

// exporterFix classifies a finding that names one exporter.
func exporterFix(queueBatch bool) batchFix {
	if queueBatch {
		return queueFix
	}

	return processorFix
}

// pipelineFix classifies a finding that covers a pipeline's legs at once. It
// takes the queue batcher only when every leg has one: a hint naming a setting
// half the exporters do not accept is a hint half of them cannot follow.
func pipelineFix(legs []unbatchedLeg) batchFix {
	queue, processor := 0, 0

	for _, leg := range legs {
		if leg.queueBatch {
			queue++
		} else {
			processor++
		}
	}

	switch {
	case processor == 0:
		return queueFix
	case queue == 0:
		return processorFix
	default:
		return splitFix
	}
}

// batchProcessorHint is the fix that works whatever the exporters accept.
const batchProcessorHint = "add batch before the exporters to reduce the number of outgoing requests"

// batchHint renders the fix. It leads with the queue batcher, which is where
// upstream is steering and which sits behind the retry logic rather than in
// front of it -- unless the exporters have nowhere to write it, and then the
// processor is the only batching on offer. "target" names what to configure
// and "subject" agrees with the verb after it.
func batchHint(target, subject string, fix batchFix) string {
	switch fix {
	case queueFix:
		return "configure " + rule.SendingQueueKey + "." + rule.BatchKey + " on " + target +
			", which batches behind the retry queue; the batch processor also works but drops data " +
			"that fails to send"
	case processorFix:
		return batchProcessorHint + "; " + subject + " no " + rule.SendingQueueKey + "." + rule.BatchKey +
			" to batch in"
	default:
		// splitFix, where the exporters disagree. The processor is still the one
		// fix all of them can take, but saying none of them has a queue batcher
		// would be false of half of them, so the clause says which half.
		return batchProcessorHint + "; only some of them can take a " +
			rule.SendingQueueKey + "." + rule.BatchKey + " of their own"
	}
}

// batchDocsFor points at the page the hint rests on: the queue batcher's
// settings, or the processor's where the hint names the processor.
func batchDocsFor(fix batchFix) string {
	if fix == queueFix {
		return rule.ExporterQueueDocs
	}

	return rule.BatchDocs
}

// unbatchedLeg is an exporter reference whose exporter batches nothing, and
// whether the targeted release lets that exporter write sending_queue.batch.
// One that cannot -- debug and nop among them, and every exporter on a release
// before upstream put a batcher in the queue -- can only be batched in front
// of, and a hint naming a setting it does not have would be no fix.
type unbatchedLeg struct {
	ref        config.Ref
	queueBatch bool
}

// exportLegs reads what each of a pipeline's exporters does about batching. It
// returns how many legs leave the collector here -- a connector is not one, it
// feeds another pipeline that has exporters of its own -- and the ones whose
// exporter demonstrably batches nothing.
func exportLegs(ctx *rule.Context, p config.Pipeline) (int, []unbatchedLeg) {
	legs, unbatched := 0, []unbatchedLeg(nil)

	for _, ref := range p.Exporters {
		c, declared := ctx.Index.Resolve(config.KindExporter, ref.ID)
		if declared && c.Kind == config.KindConnector {
			continue
		}

		legs++

		// An exporter nobody declared has no settings to read, and the collector
		// does not start at all; undefined-reference is what reports it.
		if !declared {
			continue
		}

		if readQueueBatching(c) == batchNone {
			unbatched = append(unbatched, unbatchedLeg{ref: ref, queueBatch: acceptsQueueBatch(ctx, c.ID.Type)})
		}
	}

	return legs, unbatched
}

// batching is what a config settles about an exporter's own batching.
type batching int

const (
	// batchUnknown is an exporter whose settings this rule cannot read: one a
	// merge key or an expansion supplies. Neither a finding against it nor one
	// that counts it as batching would rest on anything.
	batchUnknown batching = iota
	// batchNone is an exporter that batches nothing before it sends.
	batchNone
	// batchQueue is an exporter batching inside its sending queue.
	batchQueue
)

// readQueueBatching reads an exporter's queue as the document writes it. Only
// the top level is read, which is where the queue sits.
//
// The queue batcher is off by default, so an exporter whose settings do not
// write it is an exporter that does not batch there. That reading is the
// document's alone: what the field schema knows about the type decides which
// fix the hint can name, not whether a batcher nobody wrote is running.
func readQueueBatching(c config.Component) batching {
	var block *yaml.Node

	settings := rule.ResolveAlias(c.ValueNode)
	if settings != nil && settings.Kind == yaml.MappingNode {
		for _, e := range rule.MapEntries(settings, "") {
			if e.Key == rule.SendingQueueKey {
				block = rule.ResolveAlias(e.Node)
			}
		}
	}

	switch {
	case block != nil && block.Kind == yaml.MappingNode:
		return readBatchBlock(block)
	case block != nil && !rule.IsNull(block):
		// A queue the collector builds from an expansion may well be one that
		// batches, and nothing here can see what it holds.
		return batchUnknown
	}

	// An empty queue, or none written at all, takes the defaults, and the
	// batcher is not one of them.
	return batchNone
}

// readBatchBlock reads the one queue setting that decides whether the exporter
// batches. Writing the key is what turns the batcher on: the block is optional
// upstream, and an empty one takes flush_timeout, min_size and max_size at
// their defaults. Whether what is written inside it is a batcher the collector
// accepts is the field rules' business, and so is the queue's own enabled flag:
// an exporter that names a batcher has said what it wants, and telling it to
// batch would be advising the setting it already writes.
func readBatchBlock(block *yaml.Node) batching {
	for _, e := range rule.MapEntries(block, "") {
		if e.Key == rule.BatchKey {
			return batchQueue
		}
	}

	return batchNone
}

// acceptsQueueBatch reports whether the targeted release lets this exporter
// write sending_queue.batch, which is what decides whether a hint may name it.
//
// The queue batcher is younger than the queue: a release before upstream added
// it has a sending_queue with no batch under it, and telling a config targeting
// that release to write one names a setting the collector rejects on startup --
// which unknown-field, reading the same schema, would report in the same run.
// So the schema is asked for the field itself rather than for the queue holding
// it, and where it cannot answer -- an exporter it does not describe, or a queue
// it leaves open, which may hold settings it does not list -- the answer is what
// keeps the hint honest: no for the first, since nothing says the field is
// there, and yes for the second, since nothing says it is not.
func acceptsQueueBatch(ctx *rule.Context, typ string) bool {
	if !rule.DescribedExporter(ctx, typ) {
		return false
	}

	queue, held := rule.ExporterFields(ctx, typ).Children[rule.SendingQueueKey]
	if !held || queue == nil {
		return false
	}

	if queue.Open {
		return true
	}

	_, batches := queue.Children[rule.BatchKey]

	return batches
}
