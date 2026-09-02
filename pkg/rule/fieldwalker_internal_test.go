package rule

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// TestWalkFollowsAWholeAliasChain covers the alias that names an alias, which
// is what an anchor written onto one produces: `b: &y *x` and then `c: *y`.
// Stopping after a single hop left the type check reading an alias node and
// reporting the value as the wrong type.
func TestWalkFollowsAWholeAliasChain(t *testing.T) {
	t.Parallel()

	chained, cyclic := aliasChain(t)

	for name, node := range map[string]*yaml.Node{
		"an alias naming an alias":    chained,
		"an anchor containing itself": cyclic,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var got []string

			w := FieldWalker{
				Ctx:        nil,
				OnUnknown:  nil,
				OnRequired: nil,
				OnInvalid: func(_ *yaml.Node, path, want string) {
					got = append(got, path+": "+want)
				},
				OnDeprecated: nil,
			}
			w.walk(&schema.Field{Type: "string"}, node, "receivers.otlp.endpoint")

			assert.Empty(t, got, "a resolved alias is the value it stands for, not a type error")
		})
	}
}

// aliasChain returns an alias whose target is itself an alias, and then one
// that leads back to itself. The parser resolves every alias it can, so both
// are built by rewiring what it produced for a single hop.
func aliasChain(t *testing.T) (*yaml.Node, *yaml.Node) {
	t.Helper()

	var doc yaml.Node

	require.NoError(t, yaml.Unmarshal([]byte("a: &x localhost:4317\nb: *x\nc: *x\n"), &doc))

	root := doc.Content[0]
	first, second := root.Content[3], root.Content[5]

	require.Equal(t, yaml.AliasNode, first.Kind)
	require.Equal(t, yaml.AliasNode, second.Kind)

	second.Alias = first

	cyclic := &yaml.Node{} //nolint:exhaustruct // the zero node is rewired into an alias to itself
	cyclic.Kind = yaml.AliasNode
	cyclic.Alias = cyclic

	return second, cyclic
}

// TestAFieldStatingNoShapeIsNotReported covers the schema that describes a Go
// `any` as a bare object: the resource processor's attributes[].value takes a
// plain string, and typing it as a map made the linter report the ordinary way
// of writing it as an error.
//
// The distinction is what the schema knows, not what the value is. A map that
// lists its keys still has to be a mapping; one that lists none and accepts any
// key is stating nothing, and nothing is what it should be checked against.
func TestAFieldStatingNoShapeIsNotReported(t *testing.T) {
	t.Parallel()

	scalar := scalarNode(t, "log")

	tests := map[string]struct {
		field *schema.Field
		want  bool // whether a scalar should be reported against it
	}{
		"an open map with no children states nothing": {
			field: &schema.Field{Type: typeMap, Open: true},
			want:  false,
		},
		"a map that lists its keys is still a mapping": {
			field: &schema.Field{Type: typeMap, Children: map[string]*schema.Field{
				"storage": {Type: "string"},
			}},
			want: true,
		},
		"an open map that lists keys is still a mapping": {
			field: &schema.Field{Type: typeMap, Open: true, Children: map[string]*schema.Field{
				"storage": {Type: "string"},
			}},
			want: true,
		},
		"no type at all was already unconstrained": {
			field: &schema.Field{},
			want:  false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var got []string

			w := FieldWalker{
				Ctx:        nil,
				OnUnknown:  nil,
				OnRequired: nil,
				OnInvalid: func(_ *yaml.Node, path, want string) {
					got = append(got, path+": "+want)
				},
				OnDeprecated: nil,
			}
			w.walk(tt.field, scalar, "processors.resource.attributes[0].value")

			if tt.want {
				assert.NotEmpty(t, got, "a schema that says the value is a mapping should report a scalar")

				return
			}

			assert.Empty(t, got, "a schema that says nothing about the value should not report it")
		})
	}
}

// scalarNode returns the value node of `v: <value>`, which is how a scalar
// reaches the walker.
func scalarNode(t *testing.T, value string) *yaml.Node {
	t.Helper()

	var doc yaml.Node

	require.NoError(t, yaml.Unmarshal([]byte("v: "+value+"\n"), &doc))

	node := doc.Content[0].Content[1]
	require.Equal(t, yaml.ScalarNode, node.Kind)

	return node
}
