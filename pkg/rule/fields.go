package rule

import (
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/catalog"
	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
)

func init() {
	Register(
		unknownField{base{"unknown-field",
			"a setting the component does not accept; the collector rejects these", diag.Warning}},
		requiredField{base{"required-field",
			"a setting the component cannot start without", diag.Error}},
		invalidValue{base{"invalid-value",
			"a setting whose value has the wrong type or is outside the allowed set", diag.Error}},
		deprecatedField{base{"deprecated-field",
			"a setting upstream has replaced", diag.Warning}},
	)
}

// fieldWalker validates a component's settings against its catalog schema.
// Each rule supplies the callbacks it cares about and ignores the rest.
type fieldWalker struct {
	ctx        *Context
	onUnknown  func(key string, node *yaml.Node, path string, known []string)
	onRequired func(key string, node *yaml.Node, path string)
	onInvalid  func(node *yaml.Node, path, want string)
	onDeprecat func(key string, node *yaml.Node, path, note string)
}

// walkComponents validates every declared component that has a field schema.
// Components without one are skipped entirely, so partial catalog coverage
// never produces false positives.
func (w fieldWalker) walkComponents() {
	if !w.ctx.catalogReady() {
		return
	}
	for _, kind := range config.Kinds {
		sec := w.ctx.File.Sections[kind]
		if sec == nil {
			continue
		}
		for _, c := range sec.Components {
			comp, ok := w.ctx.Catalog.Lookup(kind, c.ID.Type)
			if !ok || comp.Fields == nil {
				continue
			}
			w.walk(comp.Fields, c.ValueNode, kind.Section()+"."+c.ID.String())
		}
	}
}

func (w fieldWalker) walk(schema *catalog.Field, node *yaml.Node, path string) {
	if schema == nil || node == nil {
		return
	}
	if node.Kind == yaml.AliasNode {
		node = node.Alias // validate what the anchor resolves to
		if node == nil {
			return
		}
	}
	if isNull(node) {
		w.checkRequired(schema, nil, node, path)
		return
	}
	if !w.checkType(schema, node, path) {
		return
	}

	switch schema.Type {
	case "map", "":
		if node.Kind != yaml.MappingNode {
			return
		}
		present := map[string]bool{}
		for _, e := range mapEntries(node, path) {
			present[e.key] = true
			child, known := schema.Children[e.key]
			switch {
			case known:
				if child.Deprecated != "" && w.onDeprecat != nil {
					w.onDeprecat(e.key, e.keyNode, e.path, child.Deprecated)
				}
				w.walk(child, e.node, e.path)
			case !schema.Open && len(schema.Children) > 0 && w.onUnknown != nil:
				w.onUnknown(e.key, e.keyNode, e.path, sortedKeys(schema.Children))
			}
		}
		w.checkRequired(schema, present, node, path)
	case "list":
		if node.Kind != yaml.SequenceNode {
			return
		}
		// A list schema describes its elements through a single "item" child.
		if item := schema.Children["item"]; item != nil {
			for i, el := range node.Content {
				w.walk(item, el, indexPath(path, i))
			}
		}
	}
}

func (w fieldWalker) checkRequired(schema *catalog.Field, present map[string]bool, node *yaml.Node, path string) {
	if w.onRequired == nil {
		return
	}
	for _, req := range schema.Required {
		if !present[req] {
			w.onRequired(req, node, joinPath(path, req))
		}
	}
}

// checkType reports a type mismatch and returns whether it is safe to descend.
func (w fieldWalker) checkType(schema *catalog.Field, node *yaml.Node, path string) bool {
	if schema.Type == "" {
		return true
	}
	if node.Kind == yaml.ScalarNode && hasExpansion(node.Value) {
		return false // resolved at runtime by a confmap provider
	}
	if ok := matchesType(schema, node); !ok {
		if w.onInvalid != nil {
			w.onInvalid(node, path, describeType(schema))
		}
		return false
	}
	if len(schema.Enum) > 0 && node.Kind == yaml.ScalarNode && !contains(schema.Enum, node.Value) {
		if w.onInvalid != nil {
			w.onInvalid(node, path, "one of "+list(schema.Enum))
		}
		return false
	}
	return true
}

func matchesType(schema *catalog.Field, node *yaml.Node) bool {
	switch schema.Type {
	case "map":
		return node.Kind == yaml.MappingNode
	case "list":
		return node.Kind == yaml.SequenceNode
	case "string":
		return node.Kind == yaml.ScalarNode
	case "bool":
		return node.Tag == "!!bool"
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

func describeType(schema *catalog.Field) string {
	switch schema.Type {
	case "duration":
		return "a duration such as 5s, 200ms or 1m30s"
	case "map":
		return "a mapping"
	case "list":
		return "a list"
	default:
		return "a " + schema.Type
	}
}

var (
	durationRE  = regexp.MustCompile(`^-?(\d+(\.\d+)?(ns|us|µs|ms|s|m|h))+$`)
	expansionRE = regexp.MustCompile(`\$\{[^}]*\}|\$[A-Za-z_][A-Za-z0-9_]*`)
)

// hasExpansion reports whether a scalar contains a confmap expansion such as
// ${env:HOST}, whose value is unknown until the collector starts.
func hasExpansion(s string) bool { return expansionRE.MatchString(s) }

type unknownField struct{ base }

func (r unknownField) Check(ctx *Context) {
	sev := diag.Warning
	if ctx.Strict {
		sev = diag.Error
	}
	fieldWalker{ctx: ctx, onUnknown: func(key string, node *yaml.Node, path string, known []string) {
		ctx.Report(Finding{
			Node: node, Path: path, Severity: sev,
			Message: "unknown setting " + quote(key) + " for " + componentOf(path),
			Hint:    "accepted settings: " + list(known) + suggest(key, known),
		})
	}}.walkComponents()
}

type requiredField struct{ base }

func (r requiredField) Check(ctx *Context) {
	fieldWalker{ctx: ctx, onRequired: func(key string, node *yaml.Node, path string) {
		ctx.Report(Finding{
			Node: node, Path: path,
			Message: "missing required setting " + quote(key) + " for " + componentOf(path),
		})
	}}.walkComponents()
}

type invalidValue struct{ base }

func (r invalidValue) Check(ctx *Context) {
	fieldWalker{ctx: ctx, onInvalid: func(node *yaml.Node, path, want string) {
		ctx.Report(Finding{
			Node: node, Path: path,
			Message: quote(shortPath(path)) + " must be " + want,
		})
	}}.walkComponents()
}

type deprecatedField struct{ base }

func (r deprecatedField) Check(ctx *Context) {
	fieldWalker{ctx: ctx, onDeprecat: func(key string, node *yaml.Node, path, note string) {
		ctx.Report(Finding{
			Node: node, Path: path,
			Message: "setting " + quote(key) + " is deprecated",
			Hint:    note,
		})
	}}.walkComponents()
}

type mapEntry struct {
	key           string
	keyNode, node *yaml.Node
	path          string
}

func mapEntries(n *yaml.Node, path string) []mapEntry {
	out := make([]mapEntry, 0, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		out = append(out, mapEntry{key: k.Value, keyNode: k, node: v, path: joinPath(path, k.Value)})
	}
	return out
}

func joinPath(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}

func indexPath(path string, i int) string {
	return path + "[" + itoa(i) + "]"
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

// componentOf renders the "receivers.otlp" prefix of a settings path.
func componentOf(path string) string {
	parts := strings.SplitN(path, ".", 3)
	if len(parts) < 2 {
		return path
	}
	return parts[0] + "." + parts[1]
}

// shortPath drops the section prefix, leaving the settings path a user typed.
func shortPath(path string) string {
	parts := strings.SplitN(path, ".", 3)
	if len(parts) < 3 {
		return path
	}
	return parts[2]
}

func contains(items []string, s string) bool {
	for _, x := range items {
		if x == s {
			return true
		}
	}
	return false
}
