package rule

import (
	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// Availability reports which schema versions ship a component type. It lets
// rules explain that a component exists, just not in the targeted release.
type Availability interface {
	// Versions returns the schema versions containing the component, oldest
	// first.
	Versions(k config.Kind, typ string) []string
}

// AvailabilityFunc adapts a function to Availability. It is how a caller binds
// what the interface deliberately does not carry -- the context of the run
// asking, say -- without a rule having to know that anything was bound.
type AvailabilityFunc func(k config.Kind, typ string) []string

// Versions returns the schema versions containing the component, oldest first.
func (f AvailabilityFunc) Versions(k config.Kind, typ string) []string { return f(k, typ) }

// Distributions reports which distributions of the targeted release ship a
// component type. A schema describes one distribution, so this is what lets a
// rule say where a component the running binary lacks does ship.
type Distributions interface {
	// Distributions returns the distributions containing the component,
	// sorted, excluding the one being checked against.
	Distributions(k config.Kind, typ string) []string
}

// DistributionsFunc adapts a function to Distributions, as AvailabilityFunc
// does to Availability.
type DistributionsFunc func(k config.Kind, typ string) []string

// Distributions returns the distributions containing the component, sorted.
func (f DistributionsFunc) Distributions(k config.Kind, typ string) []string { return f(k, typ) }

// SchemaReady reports whether a schema with components was resolved. Rules
// that consult the schema stay silent otherwise rather than reporting every
// component as unknown.
func (c *Context) SchemaReady() bool { return c.Schema != nil && c.Schema.Count() > 0 }

// Version is the targeted collector release, for use in messages.
func (c *Context) Version() string {
	if c.Schema == nil || c.Schema.CollectorVersion == "" {
		return "the targeted release"
	}

	if c.Schema.Distribution != "" {
		return "collector " + c.Schema.CollectorVersion + " (" + c.Schema.Distribution + ")"
	}

	return "collector " + c.Schema.CollectorVersion
}

// EachUsedComponent visits every declared component that the service block
// actually references, together with its schema entry.
func EachUsedComponent(ctx *Context, visit func(config.Component, *schema.Component)) {
	for _, kind := range config.Kinds() {
		sec := ctx.File.Sections[kind]
		if sec == nil {
			continue
		}

		for _, c := range sec.Components {
			if !ctx.Index.Used(kind, c.ID) {
				continue
			}

			if comp, ok := ctx.Schema.Lookup(kind, c.ID.Type); ok {
				visit(c, comp)
			}
		}
	}
}

// MisplacedHint explains a reference that points at a component declared under
// a different section, which is the usual cause of an undefined reference.
func MisplacedHint(ctx *Context, id config.ID, want config.Kind) string {
	if k, ok := ctx.Index.KindOf(id); ok && k != want {
		return Quote(id.String()) + " is declared under " + k.Section() +
			"; it cannot be used as " + Article(string(want)) + " " + string(want)
	}

	if declared := declaredIDs(ctx, want); len(declared) > 0 {
		return "declared " + want.Section() + ": " + List(declared) + Suggest(id.String(), declared)
	}

	return "no " + want.Section() + " are declared in this config"
}

func declaredIDs(ctx *Context, k config.Kind) []string {
	sec := ctx.File.Sections[k]
	if sec == nil {
		return nil
	}

	out := make([]string, 0, len(sec.Components))
	for _, c := range sec.Components {
		out = append(out, c.ID.String())
	}

	return out
}
