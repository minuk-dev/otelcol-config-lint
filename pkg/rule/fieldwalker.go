package rule

import (
	"slices"

	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// The schema types a field can declare. Only maps and lists are descended
// into; the rest are leaves.
const (
	typeMap  = "map"
	typeList = "list"
)

// FieldWalker validates a component's settings against its field schema. Each
// rule supplies the callbacks it cares about and leaves the rest nil, so the
// four field rules walk the same tree and each hears only about its own half
// of it.
type FieldWalker struct {
	// Ctx is the config being checked.
	Ctx *Context
	// OnUnknown is called for a key the schema does not describe, with the keys
	// it does.
	OnUnknown func(key string, node *yaml.Node, path string, known []string)
	// OnRequired is called for a key the schema requires and the document does
	// not write.
	OnRequired func(key string, node *yaml.Node, path string)
	// OnInvalid is called for a value of the wrong type or outside the allowed
	// set, with a description of what was wanted.
	OnInvalid func(node *yaml.Node, path, want string)
	// OnDeprecated is called for a key upstream has replaced, with the note
	// saying what replaces it.
	OnDeprecated func(key string, node *yaml.Node, path, note string)
}

// WalkComponents validates every declared component that has a field schema.
// Components without one are skipped entirely, so partial schema coverage
// never produces false positives.
func (w FieldWalker) WalkComponents() {
	if !w.Ctx.SchemaReady() {
		return
	}

	for _, kind := range config.Kinds() {
		sec := w.Ctx.File.Sections[kind]
		if sec == nil {
			continue
		}

		for _, c := range sec.Components {
			comp, ok := w.Ctx.Schema.Lookup(kind, c.ID.Type)
			if !ok || comp.Fields == nil {
				continue
			}

			w.walk(comp.Fields, c.ValueNode, kind.Section()+"."+c.ID.String())
		}
	}
}

func (w FieldWalker) walk(field *schema.Field, node *yaml.Node, path string) {
	if field == nil || node == nil {
		return
	}

	// Validate what the anchor resolves to. An anchor can name an alias, so
	// one hop is not always enough; what is left after the whole chain is an
	// anchor that contains itself, which is not a document the collector
	// loads either and not one to report a type error against.
	node = ResolveAlias(node)
	if node == nil || node.Kind == yaml.AliasNode {
		return
	}

	if IsNull(node) {
		w.checkRequired(field, nil, node, path)

		return
	}

	if !w.checkType(field, node, path) {
		return
	}

	switch field.Type {
	case typeMap, "":
		w.walkMap(field, node, path)
	case typeList:
		w.walkList(field, node, path)
	default:
		// Scalars have no children to descend into.
	}
}

// walkMap validates a mapping's keys against the schema's children.
func (w FieldWalker) walkMap(field *schema.Field, node *yaml.Node, path string) {
	if node.Kind != yaml.MappingNode {
		return
	}

	present := map[string]bool{}

	for _, e := range MapEntries(node, path) {
		present[e.Key] = true

		child, known := field.Children[e.Key]

		switch {
		case known:
			if child.Deprecated != "" && w.OnDeprecated != nil {
				w.OnDeprecated(e.Key, e.KeyNode, e.Path, child.Deprecated)
			}

			w.walk(child, e.Node, e.Path)
		case !field.Open && len(field.Children) > 0 && w.OnUnknown != nil:
			w.OnUnknown(e.Key, e.KeyNode, e.Path, SortedKeys(field.Children))
		}
	}

	w.checkRequired(field, present, node, path)
}

// walkList validates every element against the schema's "item" child, which is
// how a list schema describes what it holds.
func (w FieldWalker) walkList(field *schema.Field, node *yaml.Node, path string) {
	if node.Kind != yaml.SequenceNode {
		return
	}

	item := field.Children["item"]
	if item == nil {
		return
	}

	for i, el := range node.Content {
		w.walk(item, el, IndexPath(path, i))
	}
}

func (w FieldWalker) checkRequired(field *schema.Field, present map[string]bool, node *yaml.Node, path string) {
	if w.OnRequired == nil {
		return
	}

	for _, req := range field.Required {
		if !present[req] {
			w.OnRequired(req, node, JoinPath(path, req))
		}
	}
}

// checkType reports a type mismatch and returns whether it is safe to descend.
func (w FieldWalker) checkType(field *schema.Field, node *yaml.Node, path string) bool {
	if field.Type == "" {
		return true
	}

	if node.Kind == yaml.ScalarNode && HasExpansion(node.Value) {
		return false // resolved at runtime by a confmap provider
	}

	if ok := matchesType(field, node); !ok {
		if w.OnInvalid != nil {
			w.OnInvalid(node, path, describeType(field))
		}

		return false
	}

	if len(field.Enum) > 0 && node.Kind == yaml.ScalarNode && !slices.Contains(field.Enum, node.Value) {
		if w.OnInvalid != nil {
			w.OnInvalid(node, path, "one of "+List(field.Enum))
		}

		return false
	}

	return true
}

func matchesType(field *schema.Field, node *yaml.Node) bool {
	switch field.Type {
	case typeMap:
		return node.Kind == yaml.MappingNode
	case typeList:
		return node.Kind == yaml.SequenceNode
	case "string":
		return node.Kind == yaml.ScalarNode
	case "bool":
		return node.Tag == BoolTag
	case "int":
		return node.Tag == "!!int"
	case "float":
		return node.Tag == "!!float" || node.Tag == "!!int"
	case "duration":
		return node.Kind == yaml.ScalarNode && durationRE.MatchString(node.Value)
	default:
		return true
	}
}

func describeType(field *schema.Field) string {
	switch field.Type {
	case "duration":
		return "a duration such as 5s, 200ms or 1m30s"
	case typeMap:
		return "a mapping"
	case typeList:
		return "a list"
	default:
		return "a " + field.Type
	}
}
