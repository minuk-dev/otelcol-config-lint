package schema_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// TestReadTakesTheWholeFile covers what a schema file is allowed to hold. A
// second document is the interesting one: everything after the first would be
// dropped, and nothing about the outcome would say a component went missing.
func TestReadTakesTheWholeFile(t *testing.T) {
	t.Parallel()

	const one = "collectorVersion: v0.157.0\ncomponents:\n  receiver:\n    otlp: {}\n"

	sch, err := schema.Read(strings.NewReader(one))
	require.NoError(t, err)
	assert.Equal(t, "v0.157.0", sch.CollectorVersion)

	// The type is filled in from the key it was written under.
	comp, ok := sch.Lookup(config.KindReceiver, "otlp")
	require.True(t, ok)
	assert.Equal(t, "otlp", comp.Type)

	_, err = schema.Read(strings.NewReader(one + "---\n" + one))
	require.Error(t, err, "a second document should not be dropped in silence")

	// The second document is read strictly too, so a broken one is reported as
	// what it is rather than as an extra document.
	_, err = schema.Read(strings.NewReader(one + "---\nnotAField: true\n"))
	require.Error(t, err)

	_, err = schema.Read(strings.NewReader(""))
	require.Error(t, err, "an empty file is not a schema")
}

func TestReadRejectsNullComponents(t *testing.T) {
	t.Parallel()

	for name, input := range map[string]string{
		"JSON": `{"components":{"receiver":{"otlp":null}}}`,
		"YAML": "components:\n  receiver:\n    otlp: null\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			sch, err := schema.Read(strings.NewReader(input))
			require.ErrorContains(t, err, "receiver.otlp: component must not be null")
			assert.Nil(t, sch)
		})
	}
}

func TestReadRejectsNullNestedFields(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name, json, yaml, path string
	}{
		{
			name: "child",
			json: `{"type":"map","children":{"protocols":null}}`,
			yaml: "        type: map\n        children:\n          protocols: null\n",
			path: "receiver.otlp.fields.protocols",
		},
		{
			name: "deeper child",
			json: `{"type":"map","children":{"protocols":{"type":"map","children":{"grpc":null}}}}`,
			yaml: `        type: map
        children:
          protocols:
            type: map
            children:
              grpc: null
`,
			path: "receiver.otlp.fields.protocols.grpc",
		},
		{
			name: "list item",
			json: `{"type":"list","children":{"item":null}}`,
			yaml: "        type: list\n        children:\n          item: null\n",
			path: "receiver.otlp.fields.item",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for name, input := range map[string]string{
				"JSON": `{"components":{"receiver":{"otlp":{"fields":` + tt.json + `}}}}`,
				"YAML": "components:\n  receiver:\n    otlp:\n      fields:\n" + tt.yaml,
			} {
				t.Run(name, func(t *testing.T) {
					t.Parallel()

					sch, err := schema.Read(strings.NewReader(input))
					require.ErrorContains(t, err, tt.path+": field must not be null")
					assert.Nil(t, sch)
				})
			}
		})
	}
}

func TestReadPreservesOptionalFields(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name, json, yaml string
		wantFields       *schema.Field
	}{
		{name: "omitted", json: `{}`, yaml: "{}", wantFields: nil},
		{name: "null", json: `{"fields":null}`, yaml: "\n      fields: null", wantFields: nil},
		{
			name:       "empty",
			json:       `{"fields":{}}`,
			yaml:       "\n      fields: {}",
			wantFields: &schema.Field{},
		},
		{
			name: "valid child",
			json: `{"fields":{"type":"map","children":{"protocols":{}}}}`,
			yaml: "\n      fields:\n        type: map\n        children:\n          protocols: {}",
			wantFields: &schema.Field{Type: "map", Children: map[string]*schema.Field{
				"protocols": {},
			}},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for name, input := range map[string]string{
				"JSON": `{"components":{"receiver":{"otlp":` + tt.json + `}}}`,
				"YAML": "components:\n  receiver:\n    otlp: " + tt.yaml + "\n",
			} {
				t.Run(name, func(t *testing.T) {
					t.Parallel()

					sch, err := schema.Read(strings.NewReader(input))
					require.NoError(t, err)

					comp, ok := sch.Lookup(config.KindReceiver, "otlp")
					require.True(t, ok)
					assert.Equal(t, tt.wantFields, comp.Fields)
				})
			}
		})
	}
}
