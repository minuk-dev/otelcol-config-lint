package schema_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// release builds a schema from a receiver-only inventory, which is enough for
// every comparison the diff makes; the kinds are walked the same way.
func release(version string, receivers map[string]*schema.Component) *schema.Schema {
	return &schema.Schema{
		CollectorVersion: version,
		Distribution:     "contrib",
		Components: map[config.Kind]map[string]*schema.Component{
			config.KindReceiver: receivers,
		},
	}
}

func TestDiffReportsAdditionsAndRemovals(t *testing.T) {
	t.Parallel()

	from := release("v0.157.0", map[string]*schema.Component{
		"otlp": {Type: "otlp"},
		"gone": {Type: "gone"},
	})
	to := release("v0.158.0", map[string]*schema.Component{
		"otlp":  {Type: "otlp"},
		"fresh": {Type: "fresh"},
	})

	d := schema.DiffSchemas(from, to)

	assert.Equal(t, "v0.157.0", d.From)
	assert.Equal(t, "v0.158.0", d.To)
	assert.Equal(t, []schema.Ref{{Kind: config.KindReceiver, Type: "fresh"}}, d.Added)
	assert.Equal(t, []schema.Ref{{Kind: config.KindReceiver, Type: "gone"}}, d.Removed)
	assert.False(t, d.Empty())
}

func TestDiffOfAnUnchangedReleaseIsEmpty(t *testing.T) {
	t.Parallel()

	components := func() map[string]*schema.Component {
		return map[string]*schema.Component{
			"otlp": {Type: "otlp", Stability: map[string]schema.Stability{"traces": schema.Beta}},
		}
	}

	d := schema.DiffSchemas(release("v0.157.0", components()), release("v0.158.0", components()))

	assert.True(t, d.Empty())
	assert.Contains(t, d.Markdown(), "No component changes")
}

// A rename is one upstream change, so it is reported once rather than as the
// new name appearing and the old one being deprecated.
func TestDiffReportsARenameOnce(t *testing.T) {
	t.Parallel()

	from := release("v0.156.0", map[string]*schema.Component{
		"otlp": {Type: "otlp"},
	})
	to := release("v0.157.0", map[string]*schema.Component{
		"otlp_grpc": {Type: "otlp_grpc", Alias: "otlp"},
		"otlp":      {Type: "otlp", AliasOf: "otlp_grpc", Deprecated: `renamed to "otlp_grpc"`},
	})

	d := schema.DiffSchemas(from, to)

	assert.Equal(t, []schema.Rename{
		{Kind: config.KindReceiver, From: "otlp", To: "otlp_grpc"},
	}, d.Renamed)
	assert.Empty(t, d.Added)
	assert.Empty(t, d.Removed)
}

// The pair survives into later releases, where it is no longer news.
func TestDiffDoesNotRepeatAnOlderRename(t *testing.T) {
	t.Parallel()

	renamed := func() map[string]*schema.Component {
		return map[string]*schema.Component{
			"otlp_grpc": {Type: "otlp_grpc", Alias: "otlp"},
			"otlp":      {Type: "otlp", AliasOf: "otlp_grpc"},
		}
	}

	d := schema.DiffSchemas(release("v0.157.0", renamed()), release("v0.158.0", renamed()))

	assert.Empty(t, d.Renamed)
	assert.True(t, d.Empty())
}

// A component that arrives already carrying both names was not renamed here.
func TestDiffTreatsANewAliasedComponentAsAnAddition(t *testing.T) {
	t.Parallel()

	from := release("v0.157.0", map[string]*schema.Component{})
	to := release("v0.158.0", map[string]*schema.Component{
		"new_thing": {Type: "new_thing", Alias: "newthing"},
		"newthing":  {Type: "newthing", AliasOf: "new_thing"},
	})

	d := schema.DiffSchemas(from, to)

	assert.Empty(t, d.Renamed)
	assert.Equal(t, []schema.Ref{
		{Kind: config.KindReceiver, Type: "new_thing"},
		{Kind: config.KindReceiver, Type: "newthing"},
	}, d.Added)
}

func TestDiffReportsOnlyStabilityCrossingBeta(t *testing.T) {
	t.Parallel()

	from := release("v0.157.0", map[string]*schema.Component{
		"promoted": {Type: "promoted", Stability: map[string]schema.Stability{"traces": schema.Alpha}},
		"matured":  {Type: "matured", Stability: map[string]schema.Stability{"traces": schema.Beta}},
		"within":   {Type: "within", Stability: map[string]schema.Stability{"traces": schema.Development}},
		"added":    {Type: "added", Stability: map[string]schema.Stability{"traces": schema.Beta}},
	})
	next := release("v0.158.0", map[string]*schema.Component{
		"promoted": {Type: "promoted", Stability: map[string]schema.Stability{"traces": schema.Beta}},
		"matured":  {Type: "matured", Stability: map[string]schema.Stability{"traces": schema.Unmaintained}},
		"within":   {Type: "within", Stability: map[string]schema.Stability{"traces": schema.Alpha}},
		"added": {Type: "added", Stability: map[string]schema.Stability{
			"traces": schema.Beta, "metrics": schema.Alpha,
		}},
	})

	d := schema.DiffSchemas(from, next)

	assert.Equal(t, []schema.StabilityChange{
		{Kind: config.KindReceiver, Type: "matured", Signal: "traces", From: schema.Beta, To: schema.Unmaintained},
		{Kind: config.KindReceiver, Type: "promoted", Signal: "traces", From: schema.Alpha, To: schema.Beta},
	}, d.Restabilised)
}

func TestDiffAgainstNothingReportsTheSize(t *testing.T) {
	t.Parallel()

	to := release("v0.158.0", map[string]*schema.Component{
		"otlp": {Type: "otlp"}, "kafka": {Type: "kafka"},
	})

	d := schema.DiffSchemas(nil, to)

	assert.Empty(t, d.From)
	assert.Equal(t, 2, d.Total)

	rendered := d.Markdown()
	assert.Contains(t, rendered, "new at `v0.158.0`")
	assert.Contains(t, rendered, "2 components")
}

func TestMarkdownListsEveryChange(t *testing.T) {
	t.Parallel()

	from := release("v0.157.0", map[string]*schema.Component{
		"otlp": {Type: "otlp", Stability: map[string]schema.Stability{"traces": schema.Alpha}},
		"gone": {Type: "gone"},
	})
	next := release("v0.158.0", map[string]*schema.Component{
		"otlp_grpc": {Type: "otlp_grpc", Alias: "otlp",
			Stability: map[string]schema.Stability{"traces": schema.Alpha}},
		"otlp": {Type: "otlp", AliasOf: "otlp_grpc",
			Stability: map[string]schema.Stability{"traces": schema.Beta}},
		"fresh": {Type: "fresh"},
	})

	rendered := schema.DiffSchemas(from, next).Markdown()

	assert.Contains(t, rendered, "### contrib: `v0.157.0` → `v0.158.0`")
	assert.Contains(t, rendered, "- receiver `fresh`")
	assert.Contains(t, rendered, "- receiver `gone`")
	assert.Contains(t, rendered, "- receiver `otlp` → `otlp_grpc`")
	assert.Contains(t, rendered, "- receiver `otlp` (traces): alpha → beta")
	assert.Contains(t, rendered, "1 added, 1 removed, 1 renamed, 1 across the beta line")
}

// A summary is meant to be read, and a pull request body has a size limit, so a
// release that changes hundreds of components is counted rather than listed.
func TestMarkdownCapsALongList(t *testing.T) {
	t.Parallel()

	capped := map[string]*schema.Component{}
	for _, name := range componentNames(100) {
		capped[name] = &schema.Component{Type: name}
	}

	rendered := schema.DiffSchemas(
		release("v0.157.0", map[string]*schema.Component{}),
		release("v0.158.0", capped),
	).Markdown()

	require.Contains(t, rendered, "… and 60 more")
	assert.Equal(t, 41, strings.Count(rendered, "- receiver `")+strings.Count(rendered, "- … and"))
}

func TestAtLeastBeta(t *testing.T) {
	t.Parallel()

	for level, want := range map[schema.Stability]bool{
		schema.Development:  false,
		schema.Alpha:        false,
		schema.Beta:         true,
		schema.Stable:       true,
		schema.Deprecated:   false,
		schema.Unmaintained: false,
	} {
		assert.Equal(t, want, schema.AtLeastBeta(level), string(level))
	}
}

// componentNames returns n distinct type names.
func componentNames(n int) []string {
	out := make([]string, 0, n)
	for i := range n {
		out = append(out, "component"+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}

	return out
}
