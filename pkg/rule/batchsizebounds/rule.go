// Package batchsizebounds reports a batch processor the collector will refuse
// to start.
//
// It checks what a field schema cannot state: comparisons between one field
// and another, a range the published schema flattens away, and a list entry
// written twice under a case-insensitive comparison.
package batchsizebounds

import (
	"math"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

// The numbers upstream defaults to, in the batchprocessor's factory.
const (
	// defaultSendBatchSize is the number of items a batch is sent at when
	// send_batch_size is left out. It is the figure that makes the bounds
	// check worth having: a cap picked to look reasonable, say 1000, sits
	// below it without anyone writing 8192 anywhere.
	defaultSendBatchSize = 8192
	// defaultBatchTimeout is how long a batch waits before it is sent
	// regardless of size, when timeout is left out.
	defaultBatchTimeout = 200 * time.Millisecond
	// maxBatchSize is the largest value the two size fields can hold: upstream
	// types both as uint32. The published field schemas flatten that to "int",
	// so nothing else in the linter knows the range.
	maxBatchSize = math.MaxUint32
)

// batchProcessor is one declared batch instance and the settings the collector
// validates before it starts.
type batchProcessor struct {
	id config.ID
	// node anchors findings about the instance as a whole.
	node *yaml.Node
	// path is the dotted path of the instance, e.g. "processors.batch".
	path string

	sendBatchSize    rule.Setting
	sendBatchMaxSize rule.Setting
	timeout          rule.Setting
	// metadataKeys is the metadata_keys sequence, or nil when the key was not
	// written. The entries are read where the duplicates are reported, since
	// each finding names the entry it found.
	metadataKeys *yaml.Node
	// merged reports a YAML merge key among the settings. The document is read
	// as written, so a key the merge supplies looks absent here, and a check
	// that would otherwise fill in a default has to say nothing instead.
	merged bool
}

// New builds the rule.
func New() rule.Rule {
	return batchSizeBounds{rule.NewBase("batch-size-bounds",
		"a batch processor the collector will refuse to start", diag.Error)}
}

// name renders the instance for a message, so a file with more than one batch
// processor says which is meant.
func (b batchProcessor) name() string { return "processor " + rule.Quote(b.id.String()) }

// at returns the value node of one of the instance's settings, falling back to
// the instance itself when the key is absent.
func (b batchProcessor) at(s rule.Setting) *yaml.Node { return rule.NodeOr(s.Node, b.node) }

// batchProcessors returns every declared batch processor, matched on type so
// "batch/traces" is covered too.
func batchProcessors(f *config.File) []batchProcessor {
	return rule.ProcessorsOfType(f, rule.BatchType, readBatch)
}

func readBatch(c config.Component) batchProcessor {
	proc := batchProcessor{
		id:               c.ID,
		node:             rule.NodeOr(c.KeyNode, c.ValueNode),
		path:             config.KindProcessor.Section() + "." + c.ID.String(),
		sendBatchSize:    rule.Absent(),
		sendBatchMaxSize: rule.Absent(),
		timeout:          rule.Absent(),
		metadataKeys:     nil,
		merged:           false,
	}

	if c.ValueNode == nil || c.ValueNode.Kind != yaml.MappingNode {
		return proc
	}

	for _, e := range rule.MapEntries(c.ValueNode, proc.path) {
		switch e.Key {
		case "send_batch_size":
			proc.sendBatchSize = rule.ReadInt(e.Node)
		case "send_batch_max_size":
			proc.sendBatchMaxSize = rule.ReadInt(e.Node)
		case "timeout":
			proc.timeout = rule.ReadDuration(e.Node)
		case "metadata_keys":
			proc.metadataKeys = e.Node
		case rule.MergeKey:
			proc.merged = true
		default:
			// Every other setting is the field schema's business.
		}
	}

	return proc
}

type batchSizeBounds struct{ rule.Base }

func (r batchSizeBounds) Check(ctx *rule.Context) {
	for _, proc := range batchProcessors(ctx.File) {
		// A size the field cannot hold stands on its own: it fails to decode
		// whatever the other field says, and it stops the comparison, which
		// the collector never reaches.
		if !r.checkRange(ctx, proc) {
			r.checkSizes(ctx, proc)
		}

		r.checkTimeout(ctx, proc)
		r.checkMetadataKeys(ctx, proc)
	}
}

// checkSizes compares the cap against the threshold that triggers a send.
// Neither number is wrong on its own, so a schema describing one field at a
// time sees nothing: send_batch_size is when a batch goes, send_batch_max_size
// is how large one may get, and a cap below the trigger is rejected.
func (r batchSizeBounds) checkSizes(ctx *rule.Context, proc batchProcessor) {
	if proc.sendBatchSize.Unknown() || proc.sendBatchMaxSize.Unknown() {
		return
	}

	// A cap of zero, the default, means batches are not capped at all.
	if !proc.sendBatchMaxSize.Positive() {
		return
	}

	size := int64(defaultSendBatchSize)

	switch {
	case proc.sendBatchSize.Known:
		size = proc.sendBatchSize.Num
	case proc.merged:
		// The merge key may be what sets send_batch_size, and the collector
		// resolves it before either number is read. Filling in the default
		// here would report a figure the config overrides.
		return
	}

	if proc.sendBatchMaxSize.Num >= size {
		return
	}

	// The default is the whole point of the rule, and also the one number in
	// the message the reader will not find in their file, so it is named as a
	// default rather than quoted back at them as if they had written it.
	written := " and send_batch_size to " + rule.Itoa64(size)
	if !proc.sendBatchSize.Present {
		written = ", below the default send_batch_size of " + rule.Itoa64(size)
	}

	ctx.Report(rule.Finding{
		Node: proc.at(proc.sendBatchMaxSize), Path: rule.JoinPath(proc.path, "send_batch_max_size"),
		Message: proc.name() + " sets send_batch_max_size to " + rule.Itoa64(proc.sendBatchMaxSize.Num) + written +
			": send_batch_max_size must be greater or equal to send_batch_size",
		Hint: "raise send_batch_max_size to " + rule.Itoa64(size) +
			" or more, or leave it out so batches are not split at all",
		Docs: rule.BatchDocs,
	})
}

// checkRange reports a size outside the uint32 the field holds, and reports
// whether it found one. Such a value fails to decode, so the collector never
// gets as far as the bounds check: quoting it back in a comparison would
// diagnose the wrong problem and hint at a fix that still does not load.
func (r batchSizeBounds) checkRange(ctx *rule.Context, proc batchProcessor) bool {
	var found bool

	for _, size := range []struct {
		key string
		val rule.Setting
	}{
		{key: "send_batch_size", val: proc.sendBatchSize},
		{key: "send_batch_max_size", val: proc.sendBatchMaxSize},
	} {
		if !size.val.Known || (size.val.Num >= 0 && size.val.Num <= maxBatchSize) {
			continue
		}

		found = true

		ctx.Report(rule.Finding{
			Node: proc.at(size.val), Path: rule.JoinPath(proc.path, size.key),
			Message: proc.name() + " sets " + size.key + " to " + rule.Itoa64(size.val.Num) +
				", which the field cannot hold: it counts items as a uint32, " +
				"so the collector fails to load the config before it validates it",
			Hint: "write a whole number between 0 and " + rule.Itoa64(maxBatchSize),
			Docs: rule.BatchDocs,
		})
	}

	return found
}

// checkTimeout reports the one value the collector rejects outright. Zero is
// allowed and means every batch is sent as soon as it is formed.
func (r batchSizeBounds) checkTimeout(ctx *rule.Context, proc batchProcessor) {
	if !proc.timeout.Known || proc.timeout.Num >= 0 {
		return
	}

	ctx.Report(rule.Finding{
		Node: proc.at(proc.timeout), Path: rule.JoinPath(proc.path, "timeout"),
		Message: proc.name() + " sets timeout to " + time.Duration(proc.timeout.Num).String() +
			": timeout must be greater or equal to 0",
		Hint: "leave timeout out to take the default of " + defaultBatchTimeout.String() +
			", or set 0 to send each batch as soon as it is formed",
		Docs: rule.BatchDocs,
	})
}

// checkMetadataKeys reports an entry listed twice. The keys are folded to lower
// case before they are compared, so a duplicate need not look like one.
func (r batchSizeBounds) checkMetadataKeys(ctx *rule.Context, proc batchProcessor) {
	if proc.metadataKeys == nil || proc.metadataKeys.Kind != yaml.SequenceNode {
		return
	}

	path := rule.JoinPath(proc.path, "metadata_keys")
	seen := map[string]bool{}

	for i, item := range proc.metadataKeys.Content {
		if item.Kind != yaml.ScalarNode || rule.HasExpansion(item.Value) {
			continue
		}

		folded := strings.ToLower(item.Value)
		if !seen[folded] {
			seen[folded] = true

			continue
		}

		ctx.Report(rule.Finding{
			Node: item, Path: rule.IndexPath(path, i),
			Message: proc.name() + " repeats " + rule.Quote(item.Value) + ": " +
				"duplicate entry in metadata_keys: " + rule.Quote(folded) + " (case-insensitive)",
			Hint: "the keys are compared case-insensitively, so " + rule.Quote(item.Value) +
				" is one already listed; remove it",
			Docs: rule.BatchDocs,
		})
	}
}
