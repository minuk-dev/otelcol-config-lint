package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExpansionLimit(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name  string
		items int
		valid bool
	}{
		{name: "at limit", items: maxExpandedNodes - 3, valid: true},
		{name: "above limit", items: maxExpandedNodes - 2, valid: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// The root mapping, key and sequence account for the other three nodes.
			src := "items: [" + strings.Repeat("x,", tt.items) + "]\n"
			f, err := Parse("wide.yaml", []byte(src))

			if tt.valid {
				require.NoError(t, err)
				require.NotNil(t, f)

				return
			}

			var syn *SyntaxError

			require.ErrorAs(t, err, &syn)
			assert.Nil(t, f)
			assert.Equal(t, "wide.yaml", syn.Path)
			assert.Equal(t, 1, syn.Line)
			assert.Positive(t, syn.Column)
			assert.Contains(t, syn.Msg, "YAML expansion limit of 100000 nodes")
		})
	}
}

func TestShallowAliasAmplificationIsRejected(t *testing.T) {
	t.Parallel()

	src := "base: &base [x,x,x,x,x,x,x,x,x,x]\ncopies: [" + strings.Repeat("*base,", 10_000) + "]\n"
	f, err := Parse("aliases.yaml", []byte(src))

	var syn *SyntaxError

	require.ErrorAs(t, err, &syn)
	assert.Nil(t, f)
	assert.Equal(t, 2, syn.Line)
	assert.Contains(t, syn.Msg, "YAML expansion limit")
}
