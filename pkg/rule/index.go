package rule

import (
	"github.com/samber/lo"
	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
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

// NewIndex builds an index over a parsed config. The schema, which may be nil,
// is what says which settings hold an extension reference; without one the
// built-in list is all there is.
func NewIndex(f *config.File, sch *schema.Schema) *Index {
	idx := &Index{
		File:       f,
		declared:   map[config.Kind]map[config.ID]config.Component{},
		used:       map[config.Kind]map[config.ID]bool{},
		enabled:    map[config.ID]bool{},
		extRefs:    extensionRefs(f, sch),
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

func (idx *Index) markUsed(k config.Kind, id config.ID) {
	if idx.used[k] == nil {
		idx.used[k] = map[config.ID]bool{}
	}

	idx.used[k][id] = true
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

// Subject names the reference for a message: "storage extension", or a plain
// "extension" when all the schema said is that the value has to resolve.
func (r ExtensionRef) Subject() string {
	if r.Role == "" || r.Role == schema.RoleExtension {
		return "extension"
	}

	return r.Role + " extension"
}

// extensionField is a settings key whose value names an extension, for the
// releases whose published schemas predate the extensionRef marker. A schema
// that carries markers finds these on its own, and finds the ones this list
// never had.
type extensionField struct {
	// parent is the key holding the reference, e.g. "sending_queue", and key
	// is the field inside it that carries the id.
	parent, key string
	// role says what the extension is used for, for the message.
	role string
}

// extensionFields lists the settings that name an extension. They are matched
// wherever they appear in a component's settings, not only at the top level:
// a receiver writes its authenticator under protocols.grpc.auth, and each
// exporter's queue sits somewhere different again.
//
// It returns a fresh slice so callers cannot alter what everyone else sees.
func extensionFields() []extensionField {
	return []extensionField{
		{parent: SendingQueueKey, key: StorageKey, role: schema.RoleStorage},
		{parent: "auth", key: "authenticator", role: schema.RoleAuth},
	}
}

// extensionRefDocs is the upstream page a reference of the given role is
// described on. It is derived from the role rather than carried in the schema,
// which would put a URL beside every marked field to say one of two things.
func extensionRefDocs(role string) string {
	switch role {
	case schema.RoleStorage:
		return ExporterQueueDocs
	case schema.RoleAuth:
		return AuthDocs
	default:
		return ""
	}
}

// extensionRefs collects every extension reference in the file's component
// settings, in declaration order.
func extensionRefs(f *config.File, sch *schema.Schema) []ExtensionRef {
	return lo.FlatMap(config.Kinds(), func(kind config.Kind, _ int) []ExtensionRef {
		sec := f.Sections[kind]
		if sec == nil {
			return nil
		}

		return lo.FlatMap(sec.Components, func(c config.Component, _ int) []ExtensionRef {
			return componentExtensionRefs(c, settingsSchema(sch, kind, c))
		})
	})
}

// settingsSchema is the field schema describing a component's settings, or nil
// when the release being targeted has nothing to say about the component.
func settingsSchema(sch *schema.Schema, kind config.Kind, c config.Component) *schema.Field {
	if sch == nil {
		return nil
	}

	comp, found := sch.Lookup(kind, c.ID.Type)
	if !found {
		return nil
	}

	return comp.Fields
}

// componentExtensionRefs collects the extension references in one component's
// settings, in document order.
//
// The schema is what finds them: a field marked extensionRef says the scalar
// written under it names an extension, whatever the key is called and wherever
// it sits, so a setting upstream adds is checked as soon as its schema lands.
// The built-in pairs are the fallback, for a component the schema does not
// describe and for the published schemas generated before the marker existed.
func componentExtensionRefs(c config.Component, fields *schema.Field) []ExtensionRef {
	w := &refWalker{comp: c, out: nil}
	w.walk(fields, c.ValueNode, c.Kind.Section()+"."+c.ID.String(), 0)

	return w.out
}

// refWalker descends a component's settings alongside the schema describing
// them, collecting the extension references it finds.
type refWalker struct {
	comp config.Component
	out  []ExtensionRef
}

func (w *refWalker) walk(field *schema.Field, n *yaml.Node, path string, depth int) {
	n = ResolveAlias(n)
	if n == nil || depth > MaxSettingsDepth {
		return
	}

	switch n.Kind {
	case yaml.MappingNode:
		w.walkMap(field, n, path, depth)
	case yaml.SequenceNode:
		item := childSchema(field, "item")
		for i, el := range n.Content {
			w.walk(item, el, IndexPath(path, i), depth+1)
		}
	default:
		// A scalar the schema did not mark is just a value.
	}
}

func (w *refWalker) walkMap(field *schema.Field, n *yaml.Node, path string, depth int) {
	fields := extensionFields()

	for _, e := range MapEntries(n, path) {
		child := childSchema(field, e.Key)

		if child != nil && child.ExtensionRef != "" {
			if name, named := ScalarName(e.Node).Get(); named {
				w.record(name, e.Path, child.ExtensionRef)
			}

			continue
		}

		// The built-in pair reaches a level further down than the marker does,
		// so it is skipped where the schema marks the leaf itself; taking both
		// would report the same name twice.
		builtin, held := lo.Find(fields, func(f extensionField) bool { return f.parent == e.Key })
		if held && !marked(child, builtin.key) {
			if name, named := ScalarChild(e.Node, builtin.key).Get(); named {
				w.record(name, JoinPath(e.Path, builtin.key), builtin.role)
			}
		}

		w.walk(child, e.Node, e.Path, depth+1)
	}
}

func (w *refWalker) record(name *yaml.Node, path, role string) {
	w.out = append(w.out, ExtensionRef{
		ID: config.ParseID(name.Value), Node: name, Path: path,
		Component: w.comp, Role: role, Docs: extensionRefDocs(role),
	})
}

// childSchema is what the schema says about one key, or nil where it says
// nothing -- an unknown component, an open map, a release generated before the
// key existed.
func childSchema(field *schema.Field, key string) *schema.Field {
	if field == nil {
		return nil
	}

	return field.Children[key]
}

// marked reports whether the schema already says the named child is an
// extension reference.
func marked(field *schema.Field, key string) bool {
	child := childSchema(field, key)

	return child != nil && child.ExtensionRef != ""
}
