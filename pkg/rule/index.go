package rule

import (
	"github.com/samber/lo"
	"github.com/samber/mo"
	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
)

// Index resolves references from the service block to component declarations,
// and records which declarations are actually used.
type Index struct {
	File *config.File

	declared map[config.Kind]map[config.ID]config.Component
	used     map[config.Kind]map[config.ID]bool
	// enabled holds the extensions service.extensions lists, which is the only
	// place that starts one. A settings reference marks an extension used
	// without enabling it, and telling the two apart is what lets
	// undefined-extension-reference report an extension nobody starts.
	enabled map[config.ID]bool
	// extRefs holds the extension references written inside component
	// settings, in file order.
	extRefs []ExtensionRef

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
		enabled:    map[config.ID]bool{},
		extRefs:    extensionRefs(f),
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
		idx.enabled[ref.ID] = true

		idx.markUsed(config.KindExtension, ref.ID)
	}

	// A component that names an extension in its own settings is using it,
	// whether or not the service block enables it. Without this, an extension
	// referenced only from a sending_queue would be reported twice: once as
	// unused, once as not enabled.
	for _, ref := range idx.extRefs {
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
					default:
						// A connector in the processor slot is not valid wiring;
						// the signal-support rule reports it.
					}
				}
			}
		}
	}

	return idx
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

// Used reports whether a declared component is referenced by the service
// block, or, for an extension, by another component's settings.
func (idx *Index) Used(k config.Kind, id config.ID) bool { return idx.used[k][id] }

// Enabled reports whether service.extensions lists the extension, which is
// what makes the collector instantiate it. An extension can be referenced
// without being enabled, and then it never runs.
func (idx *Index) Enabled(id config.ID) bool { return idx.enabled[id] }

// ExtensionRefs returns the extension references written inside component
// settings, such as an exporter's sending_queue.storage.
func (idx *Index) ExtensionRefs() []ExtensionRef { return idx.extRefs }

// ConnectorPipelines returns the pipelines a connector feeds, as its receiver
// side, and then the pipelines that feed it, as its exporter side.
func (idx *Index) ConnectorPipelines(id config.ID) ([]config.Pipeline, []config.Pipeline) {
	return idx.asReceiver[id], idx.asExporter[id]
}

// KindOf reports which section declares the given component type, searching
// every kind. It is used to explain mistakes like listing a processor under
// "receivers:".
func (idx *Index) KindOf(id config.ID) (config.Kind, bool) {
	for _, k := range config.Kinds() {
		if _, ok := idx.declared[k][id]; ok {
			return k, true
		}
	}

	return "", false
}

// ExtensionRef is a reference to an extension written inside a component's
// own settings rather than in the service block, such as an exporter's
// sending_queue.storage.
type ExtensionRef struct {
	// ID is the extension the setting names.
	ID config.ID
	// Node is the scalar holding the name, and Path its dotted path.
	Node *yaml.Node
	Path string
	// Component is the component whose settings carry the reference.
	Component config.Component
	// Role says what the extension is used for, e.g. "storage".
	Role string
	// Docs is the upstream page describing the setting.
	Docs string
}

// extensionField is a settings key whose value names an extension. The list is
// hardcoded because nothing in the field schema marks a string as an extension
// id: neither configauth.Config nor the queue config says so, so a schema walk
// cannot find these on its own.
type extensionField struct {
	// parent is the key holding the reference, e.g. "sending_queue", and key
	// is the field inside it that carries the id.
	parent, key string
	// role says what the extension is used for, for the message.
	role string
	// docs is the upstream page describing the setting.
	docs string
}

// extensionFields lists the settings that name an extension. They are matched
// wherever they appear in a component's settings, not only at the top level:
// a receiver writes its authenticator under protocols.grpc.auth, and each
// exporter's queue sits somewhere different again.
//
// It returns a fresh slice so callers cannot alter what everyone else sees.
func extensionFields() []extensionField {
	return []extensionField{
		{parent: "sending_queue", key: "storage", role: "storage", docs: exporterQueueDocs},
		{parent: "auth", key: "authenticator", role: "auth", docs: authDocs},
	}
}

// maxSettingsDepth bounds the settings walk. It is past the deepest real
// component config, and stops an anchor that resolves into itself.
const maxSettingsDepth = 16

// extensionRefs collects every extension reference in the file's component
// settings, in declaration order.
func extensionRefs(f *config.File) []ExtensionRef {
	return lo.FlatMap(config.Kinds(), func(kind config.Kind, _ int) []ExtensionRef {
		sec := f.Sections[kind]
		if sec == nil {
			return nil
		}

		return lo.FlatMap(sec.Components, func(c config.Component, _ int) []ExtensionRef {
			return componentExtensionRefs(c)
		})
	})
}

func componentExtensionRefs(c config.Component) []ExtensionRef {
	var out []ExtensionRef

	fields := extensionFields()

	walkSettings(c.ValueNode, c.Kind.Section()+"."+c.ID.String(), 0, func(n *yaml.Node, path string) {
		for _, e := range mapEntries(n, path) {
			field, held := lo.Find(fields, func(f extensionField) bool { return f.parent == e.key })
			if !held {
				continue
			}

			name, named := scalarChild(e.node, field.key).Get()
			if !named {
				continue
			}

			out = append(out, ExtensionRef{
				ID: config.ParseID(name.Value), Node: name,
				Path: joinPath(e.path, field.key), Component: c,
				Role: field.role, Docs: field.docs,
			})
		}
	})

	return out
}

// scalarChild returns the named scalar of a mapping, when it holds a name
// worth resolving. An empty value is not a reference, and one built from a
// confmap expansion is only known once the collector starts.
func scalarChild(n *yaml.Node, key string) mo.Option[*yaml.Node] {
	n = resolveAlias(n)
	if n == nil || n.Kind != yaml.MappingNode {
		return mo.None[*yaml.Node]()
	}

	e, found := lo.Find(mapEntries(n, ""), func(e mapEntry) bool { return e.key == key })
	if !found {
		return mo.None[*yaml.Node]()
	}

	val := resolveAlias(e.node)
	if val == nil || val.Kind != yaml.ScalarNode || val.Value == "" || hasExpansion(val.Value) {
		return mo.None[*yaml.Node]()
	}

	return mo.Some(val)
}

// walkSettings visits every mapping inside a component's settings, together
// with its dotted path.
func walkSettings(n *yaml.Node, path string, depth int, visit func(*yaml.Node, string)) {
	n = resolveAlias(n)
	if n == nil || depth > maxSettingsDepth {
		return
	}

	switch n.Kind {
	case yaml.MappingNode:
		visit(n, path)

		for _, e := range mapEntries(n, path) {
			walkSettings(e.node, e.path, depth+1, visit)
		}
	case yaml.SequenceNode:
		for i, item := range n.Content {
			walkSettings(item, indexPath(path, i), depth+1, visit)
		}
	default:
		// A scalar carries no further settings.
	}
}

// resolveAlias follows an anchor reference to the node it stands for.
func resolveAlias(n *yaml.Node) *yaml.Node {
	for i := 0; n != nil && n.Kind == yaml.AliasNode && i < maxSettingsDepth; i++ {
		n = n.Alias
	}

	return n
}

func (idx *Index) markUsed(k config.Kind, id config.ID) {
	if idx.used[k] == nil {
		idx.used[k] = map[config.ID]bool{}
	}

	idx.used[k][id] = true
}
