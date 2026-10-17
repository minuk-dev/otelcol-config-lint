package missingbatch

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule"
)

var errBatchSettings = errors.New("expected additional_processors: [processor-type, ...]")

// WithSettings lets custom distributions declare processors that batch data.
// Naming a type is an explicit assertion of its semantics, not schema inference.
func (r missingBatch) WithSettings(node *yaml.Node) (rule.Rule, error) {
	if node.Kind != yaml.MappingNode {
		return nil, errBatchSettings
	}

	additional := []string{}

	for _, entry := range rule.MapEntries(node, "") {
		if entry.Key != "additional_processors" || entry.Node.Kind != yaml.SequenceNode {
			return nil, errBatchSettings
		}

		for _, value := range entry.Node.Content {
			if value.Tag != "!!str" || strings.TrimSpace(value.Value) == "" ||
				strings.ContainsAny(value.Value, "/${}") || strings.TrimSpace(value.Value) != value.Value {
				return nil, fmt.Errorf("%w: invalid type %q", errBatchSettings, value.Value)
			}

			additional = append(additional, value.Value)
		}
	}

	r.additionalProcessors = additional

	return r, nil
}

func (r missingBatch) processorTypes() []string {
	return append([]string{rule.BatchType}, r.additionalProcessors...)
}

func (r missingBatch) hasBatchProcessor(p config.Pipeline) bool {
	return slices.ContainsFunc(p.Processors, func(ref config.Ref) bool {
		return ref.ID.Type == rule.BatchType || slices.Contains(r.additionalProcessors, ref.ID.Type)
	})
}
