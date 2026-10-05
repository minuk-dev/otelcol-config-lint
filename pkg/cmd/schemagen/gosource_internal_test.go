package schemagen

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/ruletest"
	"github.com/minuk-dev/otelcol-config-lint/pkg/rule/unknownfield"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// TestAPartlyResolvedConfigIsLeftOpen pins the rule the hostmetrics receiver
// forced: a config that decodes itself from a confmap and holds a field
// mapstructure cannot fill accepts settings its tags do not name, so the tags
// that were read are not the complete set.
//
// Reading them as complete is what reported `scrapers` -- the receiver's
// central setting, and how it has always been configured -- as unknown on every
// release generated from the Go sources.
func TestAPartlyResolvedConfigIsLeftOpen(t *testing.T) {
	t.Parallel()

	const unmarshalMethod = `
func (cfg *Config) Unmarshal(componentParser *confmap.Conf) error { return nil }
`

	tests := map[string]struct {
		decl     string
		method   string
		wantOpen bool
		wantKeys []string
	}{
		"a setting excluded from mapstructure and read by hand": {
			decl: `
type Config struct {
	Scrapers map[string]internal.Config ` + "`mapstructure:\"-\"`" + `
	RootPath string ` + "`mapstructure:\"root_path\"`" + `
}`,
			method:   unmarshalMethod,
			wantOpen: true,
			wantKeys: []string{"root_path"},
		},
		"a setting held unexported and read by hand": {
			decl: `
type Config struct {
	receiverTemplates map[string]string
	WatchObservers []string ` + "`mapstructure:\"watch_observers\"`" + `
}`,
			method:   unmarshalMethod,
			wantOpen: true,
			wantKeys: []string{"watch_observers"},
		},
		"a config that decodes itself but names every setting": {
			decl: `
type Config struct {
	Endpoint string ` + "`mapstructure:\"endpoint\"`" + `
}`,
			method:   unmarshalMethod,
			wantOpen: false,
			wantKeys: []string{"endpoint"},
		},
		"a skipped field with no confmap decoding behind it": {
			decl: `
type Config struct {
	logger *zap.Logger
	Endpoint string ` + "`mapstructure:\"endpoint\"`" + `
}`,
			method:   "",
			wantOpen: false,
			wantKeys: []string{"endpoint"},
		},
		"an Unmarshal that is not the confmap one": {
			decl: `
type Config struct {
	Scrapers map[string]internal.Config ` + "`mapstructure:\"-\"`" + `
	Endpoint string ` + "`mapstructure:\"endpoint\"`" + `
}`,
			method:   "\nfunc (cfg *Config) Unmarshal(data []byte) error { return nil }\n",
			wantOpen: false,
			wantKeys: []string{"endpoint"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			index := newGoIndex()
			index.add("example.com/rcv", []byte(`package rcv

import "go.opentelemetry.io/collector/confmap"
`+tt.decl+tt.method))

			got := index.fields("example.com/rcv.Config", nil, 0)
			require.NotNil(t, got)

			assert.Equal(t, tt.wantOpen, got.Open, "open")
			assert.ElementsMatch(t, tt.wantKeys, keysOf(got.Children), "children")
		})
	}
}

// TestOpennessSquashesOutwards covers a partly resolved struct spliced into
// another: the outer mapping accepts what the inner one accepts, so it cannot
// be the stricter of the two.
func TestOpennessSquashesOutwards(t *testing.T) {
	t.Parallel()

	index := newGoIndex()
	index.add("example.com/rcv", []byte(`package rcv

import "go.opentelemetry.io/collector/confmap"

type Config struct {
	Inner `+"`mapstructure:\",squash\"`"+`
	Endpoint string `+"`mapstructure:\"endpoint\"`"+`
}

type Inner struct {
	hidden map[string]string
	Timeout string `+"`mapstructure:\"timeout\"`"+`
}

func (cfg *Inner) Unmarshal(componentParser *confmap.Conf) error { return nil }
`))

	got := index.fields("example.com/rcv.Config", nil, 0)
	require.NotNil(t, got)

	assert.True(t, got.Open, "the outer mapping inherits the inner one's openness")
	assert.ElementsMatch(t, []string{"endpoint", "timeout"}, keysOf(got.Children))
}

func TestNestedSelfDecodingStructsStayOpen(t *testing.T) {
	t.Parallel()

	index := newGoIndex()
	index.add("example.com/rcv", []byte(`package rcv

import "go.opentelemetry.io/collector/confmap"

type Config struct {
	Nested Inner `+"`mapstructure:\"nested\"`"+`
	Items []Inner `+"`mapstructure:\"items\"`"+`
	Closed Ordinary `+"`mapstructure:\"closed\"`"+`
}

type Inner struct {
	Known string `+"`mapstructure:\"known\"`"+`
	Hidden string `+"`mapstructure:\"-\"`"+`
}

func (cfg *Inner) Unmarshal(conf *confmap.Conf) error { return nil }

type Ordinary struct {
	Known string `+"`mapstructure:\"known\"`"+`
	Hidden string `+"`mapstructure:\"-\"`"+`
}
`))

	fields := index.fields("example.com/rcv.Config", nil, 0)
	require.NotNil(t, fields)
	assert.False(t, fields.Open, "a nested decoder does not open its parent")

	nested := fields.Children["nested"]
	require.NotNil(t, nested)

	items := fields.Children["items"]
	require.NotNil(t, items)
	assert.Equal(t, typeList, items.Type)
	item := items.Children["item"]
	require.NotNil(t, item)

	for _, field := range []*schema.Field{nested, item} {
		assert.True(t, field.Open, "named structs and list items preserve openness")
		assert.Equal(t, typeMap, field.Type)
		assert.Equal(t, map[string]*schema.Field{"known": {Type: typeString}}, field.Children)
	}

	sch := &schema.Schema{Components: map[config.Kind]map[string]*schema.Component{
		config.KindReceiver: {"custom": {Type: "custom", Fields: fields}},
	}}
	found, err := ruletest.RunWith(unknownfield.New(), `
receivers:
  custom:
    nested: {known: value, hidden: custom}
    items: [{known: value, hidden: custom}]
    closed: {known: value, hidden: unknown}
    typo: unknown
`, ruletest.Options{Strict: true, Schema: sch})
	require.NoError(t, err)
	require.Len(t, found, 2, "only the ordinary struct and parent reject unknown keys")
	assert.ElementsMatch(t, []string{"receivers.custom.closed.hidden", "receivers.custom.typo"},
		[]string{found[0].Path, found[1].Path})
	assert.Equal(t, diag.Error, found[0].Severity)
	assert.Equal(t, diag.Error, found[1].Severity)
}

func keysOf(children map[string]*schema.Field) []string {
	out := make([]string, 0, len(children))
	for name := range children {
		out = append(out, name)
	}

	return out
}
