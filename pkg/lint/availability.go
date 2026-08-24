package lint

import (
	"context"
	"slices"
	"sync"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// VersionIndex answers which collector releases ship a component type. It is
// built on first use, so runs that never hit an unknown component pay nothing
// for it.
//
// The answer comes from the availability index a registry publishes, which
// covers every release it serves in one document. Only a registry that
// publishes none is answered by reading the schemas themselves, and then only
// as far as walkLimit allows: a schema is a multi-megabyte document per
// release, so working the answer out that way costs the whole registry to
// produce a single "added in ..." hint, and does it against the rate limit
// most likely to refuse it.
type VersionIndex struct {
	store schema.Store

	once  sync.Once
	byKey map[versionKey][]string
}

type versionKey struct {
	kind config.Kind
	typ  string
}

// walkLimit is how many releases the fallback reads when each one is a
// download. The newest releases are the ones a hint is usually about -- a
// component added recently, or one dropped recently -- and a hint is worth a
// handful of requests, not a hundred megabytes.
//
// A local registry is not bounded by it: there the walk is a file read per
// release, and the complete answer costs no more than a partial one.
const walkLimit = 12

// NewVersionIndex returns an index over the schemas in store.
func NewVersionIndex(store schema.Store) *VersionIndex {
	return &VersionIndex{store: store, once: sync.Once{}, byKey: nil}
}

// Versions returns the schema versions containing the component, oldest first.
//
// The context is the asking run's: the first question is what consults the
// store, which for a remote registry means a fetch. It is built once, so the
// first question's context is the one that build runs under; a later question
// is answered from the map.
func (v *VersionIndex) Versions(ctx context.Context, k config.Kind, typ string) []string {
	v.once.Do(func() { v.build(ctx) })

	return v.byKey[versionKey{k, typ}]
}

func (v *VersionIndex) build(ctx context.Context) {
	v.byKey = map[versionKey][]string{}

	if v.published(ctx) {
		return
	}

	v.walk(ctx)
}

// published fills the index from the availability index the registry publishes,
// and reports whether there was one to read.
func (v *VersionIndex) published(ctx context.Context) bool {
	avail := v.store.Availability(ctx)
	if avail == nil {
		return false
	}

	for kind, byType := range avail {
		for typ, versions := range byType {
			v.byKey[versionKey{kind, typ}] = versions
		}
	}

	return true
}

// walk fills the index by reading the schemas, for a registry that publishes no
// availability index of its own.
func (v *VersionIndex) walk(ctx context.Context) {
	versions := v.store.Versions(ctx)
	// Store.Versions is newest first, so a bound keeps the newest releases,
	// and walking backwards makes results read oldest first, which is how an
	// "added in ..." hint wants to be read.
	if v.store.Remote() && len(versions) > walkLimit {
		versions = versions[:walkLimit]
	}

	failed := 0

	for _, version := range slices.Backward(versions) {
		c, err := v.store.Load(ctx, version)
		if err != nil {
			failed++

			continue
		}

		for kind, byType := range c.Components {
			for typ := range byType {
				key := versionKey{kind, typ}
				v.byKey[key] = append(v.byKey[key], version)
			}
		}
	}

	// A registry that answered for half its releases cannot say where a
	// component came from or when it went: whichever release throttling
	// happened to drop is the one the hint would name. Nothing is a better
	// answer than a plausible wrong one, so the index is emptied and the
	// finding falls back to the hint it has without one.
	if failed*2 >= len(versions) {
		v.byKey = map[versionKey][]string{}
	}
}
