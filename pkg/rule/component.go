package rule

import (
	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// componentRules check declarations against the targeted release's schema.
func componentRules() []Rule {
	return []Rule{
		unknownComponent{base{"unknown-component",
			"a component type that does not exist in the targeted collector release", diag.Error}},
		signalSupport{base{"signal-support",
			"a component used in a pipeline whose signal it does not support", diag.Error}},
		componentStability{base{"component-stability",
			"components below beta stability can change or break without notice", diag.Info}},
		deprecatedComponent{base{"deprecated-component",
			"a component upstream has marked as deprecated or unmaintained", diag.Warning}},
	}
}

// Availability reports which schema versions ship a component type. It lets
// rules explain that a component exists, just not in the targeted release.
type Availability interface {
	// Versions returns the schema versions containing the component, oldest
	// first.
	Versions(k config.Kind, typ string) []string
}

// Distributions reports which distributions of the targeted release ship a
// component type. A schema describes one distribution, so this is what lets a
// rule say where a component the running binary lacks does ship.
type Distributions interface {
	// Distributions returns the distributions containing the component,
	// sorted, excluding the one being checked against.
	Distributions(k config.Kind, typ string) []string
}

// schemaReady reports whether a schema with components was resolved. Rules
// that consult the schema stay silent otherwise rather than reporting every
// component as unknown.
func (c *Context) schemaReady() bool { return c.Schema != nil && c.Schema.Count() > 0 }

// version is the targeted collector release, for use in messages.
func (c *Context) version() string {
	if c.Schema == nil || c.Schema.CollectorVersion == "" {
		return "the targeted release"
	}

	if c.Schema.Distribution != "" {
		return "collector " + c.Schema.CollectorVersion + " (" + c.Schema.Distribution + ")"
	}

	return "collector " + c.Schema.CollectorVersion
}

type unknownComponent struct{ base }

func (r unknownComponent) Check(ctx *Context) {
	if !ctx.schemaReady() {
		return
	}

	for _, kind := range config.Kinds() {
		sec := ctx.File.Sections[kind]
		if sec == nil {
			continue
		}

		for _, c := range sec.Components {
			if _, ok := ctx.Schema.Lookup(kind, c.ID.Type); ok {
				continue
			}

			ctx.Report(Finding{
				Node: c.KeyNode, Path: kind.Section() + "." + c.ID.String(),
				Message: "unknown " + string(kind) + " type " + quote(c.ID.Type) + " in " + ctx.version(),
				Hint:    unknownHint(ctx, kind, c.ID.Type),
			})
		}
	}
}

// unknownHint explains an unknown component: it may be declared under the wrong
// section, ship only in other distributions, exist only in other releases, or
// simply be a typo.
func unknownHint(ctx *Context, kind config.Kind, typ string) string {
	for _, other := range config.Kinds() {
		if other == kind {
			continue
		}

		if _, ok := ctx.Schema.Lookup(other, typ); ok {
			return quote(typ) + " is " + article(string(other)) + " " + string(other) +
				"; declare it under " + other.Section()
		}
	}

	// A component the running binary does not ship is a different problem from
	// a typo: the fix is switching distributions, not correcting the name.
	if ctx.Dists != nil && ctx.Schema.Distribution != "" {
		if dists := ctx.Dists.Distributions(kind, typ); len(dists) > 0 {
			return quote(typ) + " is not in the " + ctx.Schema.Distribution +
				" distribution; it ships in " + list(dists)
		}
	}

	if ctx.Avail != nil {
		if versions := ctx.Avail.Versions(kind, typ); len(versions) > 0 {
			return quote(typ) + " exists in " + list(versions) +
				" but not in " + ctx.Schema.CollectorVersion
		}
	}

	return "checked against " + ctx.version() + suggest(typ, ctx.Schema.Types(kind))
}

type signalSupport struct{ base }

func (r signalSupport) Check(ctx *Context) {
	if !ctx.schemaReady() {
		return
	}

	for _, p := range ctx.File.Service.Pipelines {
		if !isSignal(p.Signal) {
			continue // invalidPipelineKey reports this
		}

		for _, slot := range []config.Kind{config.KindReceiver, config.KindProcessor, config.KindExporter} {
			for _, ref := range p.Refs(slot) {
				decl, declared := ctx.Index.Resolve(slot, ref.ID)
				if !declared {
					continue // undefinedReference reports this
				}

				comp, known := ctx.Schema.Lookup(decl.Kind, ref.ID.Type)
				if !known {
					continue // unknownComponent reports this
				}

				if supports(comp, decl.Kind, slot, p.Signal) {
					continue
				}

				ctx.Report(Finding{
					Node: ref.Node, Path: ref.Path,
					Message: string(decl.Kind) + " " + quote(ref.ID.Type) + " does not support " +
						string(p.Signal) + " in pipeline " + quote(p.Key),
					Hint: "it supports: " + comp.SignalList(),
				})
			}
		}
	}
}

// supports reports whether a component can sit in a pipeline slot for a signal.
// Connectors are directional: the exporter side consumes the pipeline's signal,
// the receiver side produces it.
func supports(comp *schema.Component, declKind, slot config.Kind, s config.Signal) bool {
	if declKind != config.KindConnector {
		return comp.Supports(s)
	}

	switch slot {
	case config.KindReceiver:
		return comp.SupportsAsReceiver(s)
	case config.KindExporter:
		return comp.SupportsAsExporter(s)
	default:
		return comp.Supports(s)
	}
}

type componentStability struct{ base }

func (r componentStability) Check(ctx *Context) {
	if !ctx.schemaReady() {
		return
	}

	eachUsedComponent(ctx, func(c config.Component, comp *schema.Component) {
		for _, s := range usedSignals(ctx, c) {
			level, ok := comp.StabilityFor(s)
			if !ok {
				continue
			}

			subject := string(c.Kind) + " " + quote(c.ID.Type) + forSignal(s)

			switch level {
			case schema.Development:
				ctx.Report(Finding{
					Node: c.KeyNode, Path: c.Kind.Section() + "." + c.ID.String(),
					Message:  subject + " is in development and may change without notice",
					Severity: diag.Warning,
				})
			case schema.Alpha:
				ctx.Report(Finding{
					Node: c.KeyNode, Path: c.Kind.Section() + "." + c.ID.String(),
					Message: subject + " is alpha; its configuration can change between releases",
				})
			default:
				// Beta and above are what a config should be built on, and
				// deprecatedComponent covers the other end.
			}

			return // one finding per component is enough
		}
	})
}

type deprecatedComponent struct{ base }

func (r deprecatedComponent) Check(ctx *Context) {
	if !ctx.schemaReady() {
		return
	}

	eachUsedComponent(ctx, func(c config.Component, comp *schema.Component) {
		path := c.Kind.Section() + "." + c.ID.String()
		if comp.Deprecated != "" {
			ctx.Report(Finding{
				Node: c.KeyNode, Path: path,
				Message: string(c.Kind) + " " + quote(c.ID.Type) + " is deprecated",
				Hint:    comp.Deprecated,
			})

			return
		}

		for _, level := range comp.Stability {
			if level == schema.Deprecated || level == schema.Unmaintained {
				ctx.Report(Finding{
					Node: c.KeyNode, Path: path,
					Message: string(c.Kind) + " " + quote(c.ID.Type) + " is marked " + string(level) + " upstream",
					Hint:    "plan a migration; it may be removed in a future release",
				})

				return
			}
		}
	})
}

// eachUsedComponent visits every declared component that the service block
// actually references, together with its schema entry.
func eachUsedComponent(ctx *Context, visit func(config.Component, *schema.Component)) {
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

// usedSignals returns the signals a component is wired into, so stability can
// be reported for the signals that matter. Extensions have none, so they get a
// single empty signal to keep callers simple.
func usedSignals(ctx *Context, c config.Component) []config.Signal {
	if c.Kind == config.KindExtension {
		return []config.Signal{""}
	}

	var out []config.Signal

	seen := map[config.Signal]bool{}

	for _, p := range ctx.File.Service.Pipelines {
		for _, slot := range []config.Kind{config.KindReceiver, config.KindProcessor, config.KindExporter} {
			for _, ref := range p.Refs(slot) {
				if ref.ID == c.ID && !seen[p.Signal] {
					seen[p.Signal] = true
					out = append(out, p.Signal)
				}
			}
		}
	}

	return out
}

// forSignal renders the " for traces" clause, which extensions do not have.
func forSignal(s config.Signal) string {
	if s == "" {
		return ""
	}

	return " for " + string(s)
}

func article(word string) string {
	if word == "" {
		return "a"
	}

	switch word[0] {
	case 'a', 'e', 'i', 'o', 'u':
		return "an"
	default:
		return "a"
	}
}
