package schemagen

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestAnObjectStatingNothingIsUnconstrained pins the difference between the two
// objects that carry no keys of their own.
//
// Upstream renders a Go `any` as a bare object, and the resource processor's
// attributes[].value is one: it takes a plain string. Recording that as a map
// made the linter demand a mapping and report `value: "log"` -- the ordinary way
// to write the processor -- as an error. An object with additionalProperties is
// a different thing: free-form, but a mapping, and still worth checking.
func TestAnObjectStatingNothingIsUnconstrained(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		doc      string
		wantType string
		wantOpen bool
	}{
		"a Go any renders as a bare object": {
			doc:      "type: object\n",
			wantType: "",
			wantOpen: true,
		},
		"a map of arbitrary keys is still a mapping": {
			doc:      "type: object\nadditionalProperties:\n  type: string\n",
			wantType: typeMap,
			wantOpen: true,
		},
		"an object that lists its keys is a mapping and closed": {
			doc:      "type: object\nproperties:\n  endpoint:\n    type: string\n",
			wantType: typeMap,
			wantOpen: false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var doc jsonSchema

			require.NoError(t, yaml.Unmarshal([]byte(tt.doc), &doc))

			got := newSchemaSet().field(&doc, ".", nil, 0)
			require.NotNil(t, got)

			assert.Equal(t, tt.wantType, got.Type, "type")
			assert.Equal(t, tt.wantOpen, got.Open, "open")
		})
	}
}
