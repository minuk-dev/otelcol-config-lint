package rule

import (
	"slices"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
)

// UpstreamCovered reports whether every ingress path passes through a declared
// processor of one of the given types, following only the supplied connectors.
// Unknown references, missing producers, signal changes and cycles are not proof
// of coverage. Callers must choose connectors that preserve their rule's property.
func (idx *Index) UpstreamCovered(p config.Pipeline, processors, connectors []string) bool {
	if idx.HasDynamicRefs(config.KindExporter) {
		return false
	}

	type visit struct {
		pipeline string
		covered  bool
	}
	// A remembered false also marks an in-progress visit, so cycles fail closed.
	memo := map[visit]bool{}

	var walk func(config.Pipeline, bool) bool

	walk = func(current config.Pipeline, covered bool) bool {
		key := visit{pipeline: current.Key, covered: covered}
		if result, seen := memo[key]; seen {
			return result
		}

		memo[key] = false

		covered = covered || idx.hasProcessor(current, processors)
		if len(current.Receivers) == 0 || IsExpansion(current.ReceiversNode) {
			return false
		}

		for _, ref := range current.Receivers {
			component, found := idx.Resolve(config.KindReceiver, ref.ID)
			if !found {
				return false
			}

			if component.Kind == config.KindReceiver && covered {
				continue
			}

			if component.Kind != config.KindConnector || !slices.Contains(connectors, component.ID.Type) {
				return false
			}

			_, producers := idx.ConnectorPipelines(component.ID)
			if len(producers) == 0 {
				return false
			}

			if slices.ContainsFunc(producers, func(producer config.Pipeline) bool {
				return producer.Signal != current.Signal || !walk(producer, covered)
			}) {
				return false
			}
		}

		memo[key] = true

		return true
	}

	return walk(p, false)
}

func (idx *Index) hasProcessor(p config.Pipeline, types []string) bool {
	for _, ref := range p.Processors {
		component, found := idx.Resolve(config.KindProcessor, ref.ID)
		if found && slices.Contains(types, component.ID.Type) {
			return true
		}
	}

	return false
}
