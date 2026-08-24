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
