// Package catalog describes which components exist in a given collector
// release, and what each of them accepts.
package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
)

// Catalog is the component inventory of one collector release.
type Catalog struct {
	// CollectorVersion is the upstream release tag, e.g. "v0.157.0".
	CollectorVersion string `json:"collectorVersion" yaml:"collectorVersion"`
	// Distributions lists which upstream distributions were merged in.
	Distributions []string `json:"distributions" yaml:"distributions"`
	GeneratedAt   string   `json:"generatedAt,omitempty" yaml:"generatedAt,omitempty"`
	// Sources maps each distribution to the module it was generated from.
	Sources map[string]string `json:"sources,omitempty" yaml:"sources,omitempty"`
	// Components is indexed by kind ("receiver", "processor", ...) and then by
	// component type ("otlp", "batch", ...).
	Components map[config.Kind]map[string]*Component `json:"components" yaml:"components"`
}

// Component describes a single component type available in a release.
type Component struct {
	Type string `json:"type" yaml:"type"`
	// Signals lists the pipeline signals the component supports. Extensions
	// have none; connectors express theirs through Pairs instead.
	Signals []config.Signal `json:"signals,omitempty" yaml:"signals,omitempty"`
	// Stability maps a signal (or "extension", or "traces_to_metrics" for
	// connectors) to its stability level.
	Stability map[string]Stability `json:"stability,omitempty" yaml:"stability,omitempty"`
	// Pairs lists the signal conversions a connector supports.
	Pairs []Pair `json:"pairs,omitempty" yaml:"pairs,omitempty"`
	// Distributions lists the upstream distributions shipping the component.
	Distributions []string `json:"distributions,omitempty" yaml:"distributions,omitempty"`
	// Module is the Go module path the component lives in.
	Module string `json:"module,omitempty" yaml:"module,omitempty"`
	// Deprecated is set when the component is on its way out; the value is the
	// upstream note explaining what to use instead.
	Deprecated string `json:"deprecated,omitempty" yaml:"deprecated,omitempty"`
	// AliasOf names the canonical type when this entry is a legacy name kept
	// for compatibility. Upstream is renaming component types to snake_case,
	// so "otlp" is an alias of "otlp_grpc" from v0.157.0 on.
	AliasOf string `json:"aliasOf,omitempty" yaml:"aliasOf,omitempty"`
	// Alias is the legacy name this component is also registered under.
	Alias string `json:"alias,omitempty" yaml:"alias,omitempty"`
	// Fields is the optional field-level schema. It is only populated for
	// components covered by an overlay, and rules that inspect settings skip
	// components without it.
	Fields *Field `json:"fields,omitempty" yaml:"fields,omitempty"`
}

// Pair is a connector's supported signal conversion.
type Pair struct {
	From config.Signal `json:"from" yaml:"from"`
	To   config.Signal `json:"to" yaml:"to"`
}

// Stability is an upstream component stability level.
type Stability string

// Stability levels, ordered from least to most mature.
const (
	Development  Stability = "development"
	Alpha        Stability = "alpha"
	Beta         Stability = "beta"
	Stable       Stability = "stable"
	Deprecated   Stability = "deprecated"
	Unmaintained Stability = "unmaintained"
)

// Field is a node in a component's settings schema.
type Field struct {
	// Type is the expected YAML type: "map", "list", "string", "int", "float",
	// "bool", "duration" or "" when unconstrained.
	Type string `json:"type,omitempty" yaml:"type,omitempty"`
	// Children are the accepted keys when Type is "map".
	Children map[string]*Field `json:"children,omitempty" yaml:"children,omitempty"`
	// Required lists child keys that must be present.
	Required []string `json:"required,omitempty" yaml:"required,omitempty"`
	// Enum restricts a scalar to a fixed set of values.
	Enum []string `json:"enum,omitempty" yaml:"enum,omitempty"`
	// Open allows keys outside Children, for free-form maps such as headers.
	Open bool `json:"open,omitempty" yaml:"open,omitempty"`
	// Deprecated explains what replaced this field, when it is on its way out.
	Deprecated string `json:"deprecated,omitempty" yaml:"deprecated,omitempty"`
	// Doc is a one-line description used in diagnostics hints.
	Doc string `json:"doc,omitempty" yaml:"doc,omitempty"`
}

// Lookup returns the component of the given kind and type.
func (c *Catalog) Lookup(k config.Kind, typ string) (*Component, bool) {
	byType, ok := c.Components[k]
	if !ok {
		return nil, false
	}
	comp, ok := byType[typ]
	return comp, ok
}

// Types returns every component type of a kind, sorted.
func (c *Catalog) Types(k config.Kind) []string {
	out := make([]string, 0, len(c.Components[k]))
	for t := range c.Components[k] {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// Count returns the total number of components in the catalog.
func (c *Catalog) Count() int {
	n := 0
	for _, byType := range c.Components {
		n += len(byType)
	}
	return n
}

// Supports reports whether the component can take part in a pipeline of the
// given signal. Connectors are matched against either end of their pairs, since
// they appear as both a receiver and an exporter.
func (comp *Component) Supports(s config.Signal) bool {
	for _, x := range comp.Signals {
		if x == s {
			return true
		}
	}
	for _, p := range comp.Pairs {
		if p.From == s || p.To == s {
			return true
		}
	}
	return false
}

// SupportsAsExporter reports whether a connector accepts the signal on its
// input (exporter) side.
func (comp *Component) SupportsAsExporter(s config.Signal) bool {
	for _, p := range comp.Pairs {
		if p.From == s {
			return true
		}
	}
	return false
}

// SupportsAsReceiver reports whether a connector emits the signal on its output
// (receiver) side.
func (comp *Component) SupportsAsReceiver(s config.Signal) bool {
	for _, p := range comp.Pairs {
		if p.To == s {
			return true
		}
	}
	return false
}

// SignalList renders the supported signals for use in a message.
func (comp *Component) SignalList() string {
	if len(comp.Pairs) > 0 {
		parts := make([]string, 0, len(comp.Pairs))
		for _, p := range comp.Pairs {
			parts = append(parts, string(p.From)+"->"+string(p.To))
		}
		return strings.Join(parts, ", ")
	}
	parts := make([]string, 0, len(comp.Signals))
	for _, s := range comp.Signals {
		parts = append(parts, string(s))
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

// StabilityFor returns the stability of a component for one signal, falling
// back to the single recorded level for components that have just one.
func (comp *Component) StabilityFor(s config.Signal) (Stability, bool) {
	if v, ok := comp.Stability[string(s)]; ok {
		return v, true
	}
	if len(comp.Stability) == 1 {
		for _, v := range comp.Stability {
			return v, true
		}
	}
	return "", false
}

// Format is a catalog serialisation format.
type Format string

// The formats a catalog can be written in. YAML is the human-readable form
// kept in the repository; JSON is offered for tools that prefer it.
const (
	YAML Format = "yaml"
	JSON Format = "json"
)

// Read decodes a catalog. Both YAML and JSON are accepted, since JSON is valid
// YAML, so callers do not have to know which form they were handed.
func Read(r io.Reader) (*Catalog, error) {
	var c Catalog
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("decode catalog: empty document")
		}
		return nil, fmt.Errorf("decode catalog: %w", err)
	}
	if c.Components == nil {
		c.Components = map[config.Kind]map[string]*Component{}
	}
	for _, byType := range c.Components {
		for typ, comp := range byType {
			if comp.Type == "" {
				comp.Type = typ
			}
		}
	}
	return &c, nil
}

// ReadFile decodes a catalog from a file on disk.
func ReadFile(path string) (*Catalog, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	c, err := Read(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Write encodes the catalog in the given format. An empty format writes YAML.
func (c *Catalog) Write(w io.Writer, format Format) error {
	if format == JSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(c)
	}
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	if err := enc.Encode(c); err != nil {
		return err
	}
	return enc.Close()
}

// FormatOf returns the format a file name implies.
func FormatOf(path string) Format {
	if strings.HasSuffix(path, ".json") {
		return JSON
	}
	return YAML
}
