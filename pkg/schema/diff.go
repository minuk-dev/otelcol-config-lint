package schema

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
)

// Diff reports what one collector release changed about a distribution's
// component inventory, against the release before it.
//
// It exists so a generated schema can be reviewed as a change rather than as a
// four megabyte file: the interesting part of a new release is the handful of
// components that appeared, went away, were renamed or crossed the stability
// line the rules report on, and all of that is derivable by comparing the two
// schemas.
type Diff struct {
	// Distribution is the binary both schemas describe.
	Distribution string
	// From is the release compared against; it is empty when the distribution
	// had no earlier schema, which makes every component an addition and is
	// reported as such rather than as several hundred bullet points.
	From string
	// To is the release being introduced.
	To string
	// Added and Removed list components present in only one of the two. A
	// renamed component appears in neither: it is in Renamed instead, since
	// reporting it twice would suggest two changes where upstream made one.
	Added   []Ref
	Removed []Ref
	// Renamed lists components upstream gave a new type, which it expresses by
	// keeping the old name registered as a deprecated alias of the new one.
	Renamed []Rename
	// Restabilised lists components that crossed the beta line in either
	// direction. Below beta is what component-stability reports on, so these
	// are the changes that alter what an unchanged config is told.
	Restabilised []StabilityChange
	// Total is how many components the new schema carries, which is the one
	// number worth having when From is empty.
	Total int
}

// Ref names one component in a schema.
type Ref struct {
	Kind config.Kind
	Type string
}

// Rename is a component type upstream replaced with another.
type Rename struct {
	Kind config.Kind
	From string
	To   string
}

// StabilityChange is one component's stability moving across the beta line.
type StabilityChange struct {
	Kind config.Kind
	Type string
	// Signal is the stability map's key: a signal, or "extension", or a
	// connector's "traces_to_metrics".
	Signal string
	From   Stability
	To     Stability
}

// AtLeastBeta reports whether a stability level is one a config should be built
// on. Deprecated and unmaintained are not on the maturity ladder at all, so
// they count as below it: a component reaching either is as much a change to
// what a config is told as one falling back to alpha.
func AtLeastBeta(s Stability) bool {
	return s == Beta || s == Stable
}

// DiffSchemas compares two schemas of the same distribution. A nil from means
// the distribution had no earlier release, which is not an error: it is the
// first schema, and the result says so by leaving From empty.
func DiffSchemas(previous, release *Schema) *Diff {
	d := &Diff{
		Distribution: release.Distribution,
		From:         "",
		To:           release.CollectorVersion,
		Added:        nil,
		Removed:      nil,
		Renamed:      nil,
		Restabilised: nil,
		Total:        release.Count(),
	}

	if previous == nil {
		return d
	}

	d.From = previous.CollectorVersion

	for _, kind := range config.Kinds() {
		old, current := previous.Components[kind], release.Components[kind]

		renamed := renamesIn(kind, old, current)
		d.Renamed = append(d.Renamed, renamed...)

		// A rename shows up as an addition of the new name, and the old name
		// survives as an alias so it is not a removal. Dropping the new name
		// from the additions leaves one entry describing one upstream change.
		skip := map[string]bool{}
		for _, r := range renamed {
			skip[r.To] = true
		}

		d.Added = append(d.Added, missingFrom(kind, old, current, skip)...)
		d.Removed = append(d.Removed, missingFrom(kind, current, old, nil)...)
		d.Restabilised = append(d.Restabilised, restabilisedIn(kind, old, current)...)
	}

	return d
}

// Empty reports whether the two releases ship the same components, described
// the same way.
func (d *Diff) Empty() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 &&
		len(d.Renamed) == 0 && len(d.Restabilised) == 0
}

// missingFrom lists the components of have that base does not, skipping the
// types the caller has already accounted for.
func missingFrom(kind config.Kind, base, have map[string]*Component, skip map[string]bool) []Ref {
	var out []Ref

	for typ := range have {
		if _, ok := base[typ]; ok || skip[typ] {
			continue
		}

		out = append(out, Ref{Kind: kind, Type: typ})
	}

	sort.Slice(out, func(a, b int) bool { return out[a].Type < out[b].Type })

	return out
}

// renamesIn finds the component types that gained a new name in this release.
//
// Upstream expresses a rename by registering the component under both names and
// marking the old one deprecated, which schemagen records as the pair of
// Alias and AliasOf. The pair is only news the release it appears in, so one
// that both schemas already carry is not reported.
func renamesIn(kind config.Kind, old, current map[string]*Component) []Rename {
	var out []Rename

	before := aliasPairs(old)

	for legacy, replacement := range aliasPairs(current) {
		if was, ok := before[legacy]; ok && was == replacement {
			continue
		}

		// Only a name the previous release already served is a rename; a
		// component that arrives carrying both names is simply new.
		if _, ok := old[legacy]; !ok {
			continue
		}

		out = append(out, Rename{Kind: kind, From: legacy, To: replacement})
	}

	sort.Slice(out, func(a, b int) bool { return out[a].From < out[b].From })

	return out
}

// aliasPairs maps each legacy component type to the type that replaced it. Both
// entries of a renamed component describe the same pair, so either one is
// enough to recognise it.
func aliasPairs(byType map[string]*Component) map[string]string {
	out := map[string]string{}

	for typ, comp := range byType {
		switch {
		case comp.AliasOf != "":
			out[typ] = comp.AliasOf
		case comp.Alias != "":
			out[comp.Alias] = typ
		}
	}

	return out
}

// restabilisedIn lists the components whose stability crossed the beta line,
// for any signal either release recorded one for.
func restabilisedIn(kind config.Kind, old, current map[string]*Component) []StabilityChange {
	var out []StabilityChange

	for typ, comp := range current {
		was, ok := old[typ]
		if !ok {
			continue // an addition, already reported as one
		}

		for _, signal := range stabilitySignals(was, comp) {
			before, after := was.Stability[signal], comp.Stability[signal]
			// A signal only one release records is that signal being added or
			// dropped, which the component's own entry does not capture but
			// which is also not a stability change.
			if before == "" || after == "" || AtLeastBeta(before) == AtLeastBeta(after) {
				continue
			}

			out = append(out, StabilityChange{
				Kind: kind, Type: typ, Signal: signal, From: before, To: after,
			})
		}
	}

	sort.Slice(out, func(a, b int) bool {
		if out[a].Type != out[b].Type {
			return out[a].Type < out[b].Type
		}

		return out[a].Signal < out[b].Signal
	})

	return out
}

// stabilitySignals returns the keys either component records a level under,
// sorted so the report reads the same way every run.
func stabilitySignals(a, b *Component) []string {
	seen := map[string]bool{}

	var out []string

	for _, comp := range []*Component{a, b} {
		for signal := range comp.Stability {
			if !seen[signal] {
				seen[signal] = true
				out = append(out, signal)
			}
		}
	}

	sort.Strings(out)

	return out
}

// maxListed caps how many entries of one kind a summary spells out. A release
// that renumbers a whole distribution would otherwise push the summary past
// what a pull request body can hold, and the point of the summary is the part a
// reviewer reads.
const maxListed = 40

// Markdown renders the diff as a section of a pull request body.
func (d *Diff) Markdown() string {
	var b strings.Builder

	if d.From == "" {
		fmt.Fprintf(&b, "### %s: new at `%s`\n\n%d components.\n",
			d.Distribution, d.To, d.Total)

		return b.String()
	}

	fmt.Fprintf(&b, "### %s: `%s` → `%s`\n\n%s\n", d.Distribution, d.From, d.To, d.headline())

	writeList(&b, "Added", len(d.Added), func(i int) string { return refLine(d.Added[i]) })
	writeList(&b, "Removed", len(d.Removed), func(i int) string { return refLine(d.Removed[i]) })
	writeList(&b, "Renamed", len(d.Renamed), func(i int) string {
		r := d.Renamed[i]

		return fmt.Sprintf("%s `%s` → `%s`", r.Kind, r.From, r.To)
	})
	writeList(&b, "Stability", len(d.Restabilised), func(i int) string {
		c := d.Restabilised[i]

		return fmt.Sprintf("%s `%s` (%s): %s → %s", c.Kind, c.Type, c.Signal, c.From, c.To)
	})

	return b.String()
}

// headline is the one line a reviewer reads when nothing below needs attention.
func (d *Diff) headline() string {
	if d.Empty() {
		return fmt.Sprintf("No component changes; %d in total.", d.Total)
	}

	counts := []struct {
		n    int
		noun string
	}{
		{len(d.Added), "added"},
		{len(d.Removed), "removed"},
		{len(d.Renamed), "renamed"},
		{len(d.Restabilised), "across the beta line"},
	}

	var parts []string

	for _, c := range counts {
		if c.n > 0 {
			parts = append(parts, strconv.Itoa(c.n)+" "+c.noun)
		}
	}

	return strings.Join(parts, ", ") + fmt.Sprintf("; %d components in total.", d.Total)
}

// refLine renders one component as a bullet's text.
func refLine(r Ref) string { return fmt.Sprintf("%s `%s`", r.Kind, r.Type) }

// writeList appends one titled bullet list, or nothing when it would be empty.
func writeList(b *strings.Builder, title string, n int, line func(int) string) {
	if n == 0 {
		return
	}

	fmt.Fprintf(b, "\n**%s**\n\n", title)

	shown := min(n, maxListed)
	for i := range shown {
		fmt.Fprintf(b, "- %s\n", line(i))
	}

	if shown < n {
		fmt.Fprintf(b, "- … and %d more\n", n-shown)
	}
}
