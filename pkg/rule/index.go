package rule

import (
	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
)

// Index resolves references from the service block to component declarations,
// and records which declarations are actually used.
type Index struct {
	File *config.File

	declared map[config.Kind]map[config.ID]config.Component
	used     map[config.Kind]map[config.ID]bool

	// connectorRole records the pipelines a connector takes part in, split by
	// the side it is wired to.
	asReceiver map[config.ID][]config.Pipeline
	asExporter map[config.ID][]config.Pipeline
}

// NewIndex builds an index over a parsed config.
func NewIndex(f *config.File) *Index {
	idx := &Index{
		File:       f,
		declared:   map[config.Kind]map[config.ID]config.Component{},
		used:       map[config.Kind]map[config.ID]bool{},
		asReceiver: map[config.ID][]config.Pipeline{},
		asExporter: map[config.ID][]config.Pipeline{},
	}
	for kind, sec := range f.Sections {
		idx.declared[kind] = map[config.ID]config.Component{}
		idx.used[kind] = map[config.ID]bool{}
		for _, c := range sec.Components {
			// A duplicate key keeps the last declaration, matching YAML.
			idx.declared[kind][c.ID] = c
		}
	}

	for _, ref := range f.Service.Extensions {
		idx.markUsed(config.KindExtension, ref.ID)
	}
	for _, p := range f.Service.Pipelines {
		for _, slot := range []config.Kind{config.KindReceiver, config.KindProcessor, config.KindExporter} {
			for _, ref := range p.Refs(slot) {
				c, ok := idx.Resolve(slot, ref.ID)
				if !ok {
					continue
				}
				idx.markUsed(c.Kind, ref.ID)
				if c.Kind == config.KindConnector {
					switch slot {
					case config.KindReceiver:
						idx.asReceiver[ref.ID] = append(idx.asReceiver[ref.ID], p)
					case config.KindExporter:
						idx.asExporter[ref.ID] = append(idx.asExporter[ref.ID], p)
					}
				}
			}
		}
	}
	return idx
}

func (idx *Index) markUsed(k config.Kind, id config.ID) {
	if idx.used[k] == nil {
		idx.used[k] = map[config.ID]bool{}
	}
	idx.used[k][id] = true
}

// Declared returns the component declared under the given kind.
func (idx *Index) Declared(k config.Kind, id config.ID) (config.Component, bool) {
	c, ok := idx.declared[k][id]
	return c, ok
}

// Resolve finds the declaration backing a reference from a pipeline slot.
// Receiver and exporter slots also accept connectors, which is how the
// collector wires two pipelines together.
func (idx *Index) Resolve(slot config.Kind, id config.ID) (config.Component, bool) {
	if c, ok := idx.declared[slot][id]; ok {
		return c, true
	}
	if slot == config.KindReceiver || slot == config.KindExporter {
		if c, ok := idx.declared[config.KindConnector][id]; ok {
			return c, true
		}
	}
	return config.Component{}, false
}

// Used reports whether a declared component is referenced by the service block.
func (idx *Index) Used(k config.Kind, id config.ID) bool { return idx.used[k][id] }

// ConnectorPipelines returns the pipelines a connector feeds (as a receiver)
// and the pipelines that feed it (as an exporter).
func (idx *Index) ConnectorPipelines(id config.ID) (asReceiver, asExporter []config.Pipeline) {
	return idx.asReceiver[id], idx.asExporter[id]
}

// KindOf reports which section declares the given component type, searching
// every kind. It is used to explain mistakes like listing a processor under
// "receivers:".
func (idx *Index) KindOf(id config.ID) (config.Kind, bool) {
	for _, k := range config.Kinds {
		if _, ok := idx.declared[k][id]; ok {
			return k, true
		}
	}
	return "", false
}
