package schema

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/spf13/afero"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
)

// ComponentsFile is the name a registry publishes its component availability
// index under.
const ComponentsFile = "components.json"

// Components says which releases ship each component type. It is what answers
// "exists in v0.110.0 but not in v0.157.0" without reading a schema: the
// question is about every release at once, and a schema describes one, so
// asking them one by one means downloading the whole registry to learn a
// single date.
//
// It is published beside the index rather than inside it because the index is
// read by every run, and this document only by a run that met a component the
// targeted release does not have.
type Components struct {
	// Distributions maps each distribution to the components it has shipped.
	// Coverage differs between them, the same way it does in the index: a
	// component in contrib says nothing about core.
	Distributions map[string]ComponentSpans `json:"distributions"`
}

// ComponentSpans is one distribution's components, indexed by kind
// ("receiver", "processor", ...) and then by component type ("otlp",
// "batch", ...).
type ComponentSpans map[config.Kind]map[string][]Span

// Span is a run of consecutive releases shipping one component type.
//
// Releases rather than a list of versions because a component is added once
// and then carried: a span stays one line as the registry grows, where a list
// would gain an entry per component per release, and adding a release would
// rewrite every line of the file.
type Span struct {
	// From is the oldest release in the run.
	From string `json:"from"`
	// To is the newest. It is left out while the component is still shipped,
	// which is the usual case and keeps a new release from touching the entry
	// of every component that release did not change.
	To string `json:"to,omitempty"`
}

// Covers reports whether a release falls inside the span. An open span covers
// everything from its start on, which is what "still shipped" means when the
// answer is read against the releases the registry serves now.
func (s Span) Covers(version string) bool {
	if Compare(version, s.From) < 0 {
		return false
	}

	return s.To == "" || Compare(version, s.To) <= 0
}

// SpansOf collapses the releases a component appears in into spans. releases
// is every release the distribution serves, in any order; present says which
// of them ship the component.
//
// The newest release ends its span open rather than named, so that the entry
// of a component that is still shipped does not change when the next release
// is added.
func SpansOf(releases []string, present map[string]bool) []Span {
	ordered := oldestFirst(releases)

	var spans []Span

	cur := -1

	for _, v := range ordered {
		if !present[v] {
			cur = -1

			continue
		}

		if cur < 0 {
			spans = append(spans, Span{From: v, To: v})
			cur = len(spans) - 1

			continue
		}

		spans[cur].To = v
	}

	if len(spans) > 0 && len(ordered) > 0 && spans[len(spans)-1].To == ordered[len(ordered)-1] {
		spans[len(spans)-1].To = ""
	}

	return spans
}

// Expand returns which of releases ship each of a distribution's component
// types, oldest first, which is how an "added in ..." hint wants to be read.
// An unknown distribution has none.
//
// The releases are the caller's rather than the ones the document was written
// against: a registry that has since dropped an old release should not be
// quoted as still serving it.
func (c *Components) Expand(distribution string, releases []string) map[config.Kind]map[string][]string {
	byKind, ok := c.Distributions[distribution]
	if !ok {
		return nil
	}

	ordered := oldestFirst(releases)
	out := make(map[config.Kind]map[string][]string, len(byKind))

	for kind, byType := range byKind {
		out[kind] = make(map[string][]string, len(byType))

		for typ, spans := range byType {
			var versions []string

			for _, v := range ordered {
				if covers(spans, v) {
					versions = append(versions, v)
				}
			}

			out[kind][typ] = versions
		}
	}

	return out
}

// Has reports whether the document covers a distribution. One that does not is
// no answer at all, rather than an answer of "nothing was ever shipped".
func (c *Components) Has(distribution string) bool {
	_, ok := c.Distributions[distribution]

	return ok
}

// covers reports whether any span holds the release.
func covers(spans []Span, version string) bool {
	for _, s := range spans {
		if s.Covers(version) {
			return true
		}
	}

	return false
}

// oldestFirst returns the releases sorted the way an availability answer reads,
// leaving the caller's slice alone.
func oldestFirst(releases []string) []string {
	out := make([]string, len(releases))
	copy(out, releases)

	sort.Slice(out, func(i, j int) bool { return Compare(out[i], out[j]) < 0 })

	return out
}

// ReadComponents decodes a component availability index.
func ReadComponents(r io.Reader) (*Components, error) {
	var comps Components

	err := json.NewDecoder(r).Decode(&comps)
	if err != nil {
		return nil, fmt.Errorf("decode components: %w", err)
	}

	return &comps, nil
}

// ReadComponentsFile decodes a component availability index from a file on
// disk.
func ReadComponentsFile(path string) (*Components, error) {
	return readComponentsFile(afero.NewOsFs(), path)
}

// readComponentsFile decodes a component availability index from a file on the
// given filesystem.
func readComponentsFile(fsys afero.Fs, path string) (*Components, error) {
	f, err := fsys.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open components: %w", err)
	}

	defer func() { _ = f.Close() }()

	comps, err := ReadComponents(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	return comps, nil
}

// Write encodes the component availability index as JSON.
func (c *Components) Write(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	err := enc.Encode(c)
	if err != nil {
		return fmt.Errorf("encode components: %w", err)
	}

	return nil
}
