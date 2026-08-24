package config

import "gopkg.in/yaml.v3"

// MergeTag is the tag yaml.v3 gives the YAML merge key, "<<". The tag rather
// than the spelling is what says a key is one: a quoted "<<" is a plain string
// and stays a key of its own, which is how yaml.v3 reads it too.
const MergeTag = "!!merge"

// resolve rewrites the document into the one the collector will read: every
// alias replaced by what it points at, and every merge key replaced by the
// entries it supplies.
//
// The linter walks the node tree rather than a decoded value, and the tree is
// the document as written -- a merge key is a key called "<<" whose value is
// an alias, and an alias is a node standing in for the mapping it names. The
// collector resolves both before it sees a single setting, because confmap
// unmarshals through yaml.v3, so without this a rule reads a different config
// from the one that will run and reports settings as absent that are plainly
// in the file.
//
// Nodes are reused rather than copied, so a merged setting keeps the position
// it was written at: a finding about it lands on the line in the anchor, which
// is the line the reader has to edit.
func resolve(root *yaml.Node) {
	r := resolver{active: map[*yaml.Node]bool{}, walked: map[*yaml.Node]bool{}}
	r.value(root)
}

// resolver carries what a single pass over one document has to remember.
type resolver struct {
	// active is the aliases currently being followed, which is what stops an
	// anchor that contains itself from recursing forever. yaml.v3 refuses such
	// a document when it decodes one, but it hands back the node tree without
	// complaint, so the linter has to survive it to report anything at all.
	active map[*yaml.Node]bool
	// walked is the nodes already rewritten. An anchor merged into a dozen
	// components is one node reached a dozen times, and rewriting it once is
	// both faster and what keeps its own merges from being applied twice.
	walked map[*yaml.Node]bool
}

// value returns what a node stands for: an alias becomes the node it names,
// and anything else is rewritten in place and handed back as it is.
func (r *resolver) value(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}

	if n.Kind != yaml.AliasNode {
		r.walk(n)

		return n
	}

	// An alias naming no anchor, or one that leads back to itself, is left as
	// it was written: there is nothing to put in its place, and a document
	// holding either does not load for the collector either.
	if n.Alias == nil || r.active[n] {
		return n
	}

	r.active[n] = true
	defer delete(r.active, n)

	return r.value(n.Alias)
}

// walk rewrites a node's children, once per node however many aliases reach it.
func (r *resolver) walk(n *yaml.Node) {
	if r.walked[n] {
		return
	}

	r.walked[n] = true

	if n.Kind == yaml.MappingNode {
		r.mapping(n)

		return
	}

	for i, c := range n.Content {
		n.Content[i] = r.value(c)
	}
}

// mapping rewrites one mapping: its values resolved, and the entries its merge
// keys supply spliced in behind the keys it writes itself.
func (r *resolver) mapping(n *yaml.Node) {
	local := make([]*yaml.Node, 0, len(n.Content))

	var merged []*yaml.Node

	for i := 0; i+1 < len(n.Content); i += mappingStride {
		k, v := n.Content[i], n.Content[i+1]

		sources, isMerge := r.mergeSources(k, v)
		if isMerge {
			merged = append(merged, sources...)

			continue
		}

		local = append(local, k, r.value(v))
	}

	local = append(local, mergedEntries(local, merged)...)
	n.Content = local
}

// mergeSources returns the mappings an entry merges in, and whether the entry
// is a merge at all.
//
// A merge whose value is neither a mapping nor a list of them is not one the
// collector can load -- yaml.v3 fails the decode outright -- so it is left in
// place as the key it was written as, where the rules can say something about
// it rather than the linter quietly dropping it.
func (r *resolver) mergeSources(k, v *yaml.Node) ([]*yaml.Node, bool) {
	if k.Tag != MergeTag {
		return nil, false
	}

	val := r.value(v)

	switch {
	case val == nil:
		return nil, false
	case val.Kind == yaml.MappingNode:
		return []*yaml.Node{val}, true
	case val.Kind != yaml.SequenceNode:
		return nil, false
	}

	out := make([]*yaml.Node, 0, len(val.Content))

	for _, item := range val.Content {
		if item.Kind != yaml.MappingNode {
			return nil, false
		}

		out = append(out, item)
	}

	return out, true
}

// mergedEntries returns the key/value pairs the merges supply that the mapping
// does not already have. A key the mapping writes itself wins outright, which
// is the merge key's whole purpose, and among several merges the first to
// carry a key wins, which is the order yaml.v3 applies them in.
func mergedEntries(local, merged []*yaml.Node) []*yaml.Node {
	if len(merged) == 0 {
		return nil
	}

	seen := make(map[string]bool, len(local)/mappingStride)
	for i := 0; i < len(local); i += mappingStride {
		seen[local[i].Value] = true
	}

	var out []*yaml.Node

	for _, src := range merged {
		for i := 0; i+1 < len(src.Content); i += mappingStride {
			k, v := src.Content[i], src.Content[i+1]
			if seen[k.Value] {
				continue
			}

			seen[k.Value] = true
			out = append(out, k, v)
		}
	}

	return out
}
