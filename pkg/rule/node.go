package rule

import (
	"regexp"

	"github.com/samber/lo"
	"github.com/samber/mo"
	"gopkg.in/yaml.v3"
)

// BoolTag is the tag yaml.v3 resolves a boolean scalar to. A setting is only
// read as one when the document actually wrote a boolean, so the string
// "false" is not mistaken for the value.
const BoolTag = "!!bool"

// mappingStride is the step between keys in a yaml.Node mapping, whose Content
// alternates key, value, key, value.
const mappingStride = 2

// MaxSettingsDepth bounds a walk over a component's settings. It is past the
// deepest real component config, and stops an anchor that resolves into itself.
const MaxSettingsDepth = 16

// MapEntry is one key/value pair of a YAML mapping, together with the dotted
// path the value sits at.
type MapEntry struct {
	// Key is the mapping key as written.
	Key string
	// KeyNode and Node are the key and its value.
	KeyNode, Node *yaml.Node
	// Path is the dotted path of the value.
	Path string
}

// MapEntries returns a mapping's entries in document order, each carrying the
// path it sits at under the given parent path.
func MapEntries(n *yaml.Node, path string) []MapEntry {
	out := make([]MapEntry, 0, len(n.Content)/mappingStride)
	for i := 0; i+1 < len(n.Content); i += mappingStride {
		k, v := n.Content[i], n.Content[i+1]
		out = append(out, MapEntry{Key: k.Value, KeyNode: k, Node: v, Path: JoinPath(path, k.Value)})
	}

	return out
}

// ChildNode returns the value a mapping writes under a key, whatever shape it
// has. What counts as a usable value is the caller's business: an expansion is
// nothing to a rule resolving a name, and still worth reading to one asking
// which address a component listens on.
func ChildNode(n *yaml.Node, key string) mo.Option[*yaml.Node] {
	n = ResolveAlias(n)
	if n == nil || n.Kind != yaml.MappingNode {
		return mo.None[*yaml.Node]()
	}

	e, found := lo.Find(MapEntries(n, ""), func(e MapEntry) bool { return e.Key == key })
	if !found {
		return mo.None[*yaml.Node]()
	}

	return mo.Some(e.Node)
}

// ScalarChild returns the named scalar of a mapping, when it holds a name
// worth resolving. An empty value is not a reference, and one built from a
// confmap expansion is only known once the collector starts.
func ScalarChild(n *yaml.Node, key string) mo.Option[*yaml.Node] {
	val, written := ChildNode(n, key).Get()
	if !written {
		return mo.None[*yaml.Node]()
	}

	val = ResolveAlias(val)
	if val == nil || val.Kind != yaml.ScalarNode || val.Value == "" || HasExpansion(val.Value) {
		return mo.None[*yaml.Node]()
	}

	return mo.Some(val)
}

// WalkSettings visits every mapping inside a component's settings, together
// with its dotted path.
func WalkSettings(n *yaml.Node, path string, depth int, visit func(*yaml.Node, string)) {
	n = ResolveAlias(n)
	if n == nil || depth > MaxSettingsDepth {
		return
	}

	switch n.Kind {
	case yaml.MappingNode:
		visit(n, path)

		for _, e := range MapEntries(n, path) {
			WalkSettings(e.Node, e.Path, depth+1, visit)
		}
	case yaml.SequenceNode:
		for i, item := range n.Content {
			WalkSettings(item, IndexPath(path, i), depth+1, visit)
		}
	default:
		// A scalar carries no further settings.
	}
}

// ResolveAlias follows an anchor reference to the node it stands for.
//
// The parser resolves every alias it can, so what is left for this to follow
// is the one it cannot: an anchor that contains itself, which yaml.v3 refuses
// to decode and the collector therefore never loads. The depth bound is what
// makes that safe to walk.
func ResolveAlias(n *yaml.Node) *yaml.Node {
	for i := 0; n != nil && n.Kind == yaml.AliasNode && i < MaxSettingsDepth; i++ {
		n = n.Alias
	}

	return n
}

// IsNull reports a node that was written with no value, or none at all.
func IsNull(n *yaml.Node) bool {
	return n == nil || n.Tag == "!!null"
}

// NodeOr returns n, or the fallback when the key it would have come from was
// never written.
func NodeOr(n, fallback *yaml.Node) *yaml.Node {
	if n != nil {
		return n
	}

	return fallback
}

// NodeKind names a node's shape for a message.
func NodeKind(n *yaml.Node) string {
	switch n.Kind {
	case yaml.MappingNode:
		return "a mapping"
	case yaml.SequenceNode:
		return "a list"
	case yaml.ScalarNode:
		return "a scalar"
	case yaml.AliasNode:
		return "an alias"
	default:
		return "an unexpected node"
	}
}

// JoinPath appends a key to a dotted path.
func JoinPath(parent, key string) string {
	if parent == "" {
		return key
	}

	return parent + "." + key
}

// IndexPath appends a list index to a dotted path.
func IndexPath(path string, i int) string {
	return path + "[" + Itoa(i) + "]"
}

var (
	durationRE  = regexp.MustCompile(`^-?(\d+(\.\d+)?(ns|us|µs|ms|s|m|h))+$`)
	expansionRE = regexp.MustCompile(`\$\{[^}]*\}|\$[A-Za-z_][A-Za-z0-9_]*`)
)

// HasExpansion reports whether a scalar contains a confmap expansion such as
// ${env:HOST}, whose value is unknown until the collector starts.
func HasExpansion(s string) bool { return expansionRE.MatchString(s) }

// MaskExpansions replaces every confmap expansion in a value with mask, so text
// carrying one can be taken apart by something that cannot see past it. The
// mask stands for a value nobody knows yet rather than for nothing, which is
// what lets the caller tell "the port is an expansion" from "there is no port".
func MaskExpansions(value, mask string) string {
	return expansionRE.ReplaceAllString(value, mask)
}
