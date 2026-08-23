package lint

import (
	"context"
	"sync"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// VersionIndex answers which collector releases ship a component type, by
// consulting every schema a store can serve. It is built on first use, so runs
// that never hit an unknown component pay nothing for it.
type VersionIndex struct {
	store schema.Store

	once  sync.Once
	byKey map[versionKey][]string
}

type versionKey struct {
	kind config.Kind
	typ  string
}

// NewVersionIndex returns an index over the schemas in store.
func NewVersionIndex(store schema.Store) *VersionIndex {
	return &VersionIndex{store: store, once: sync.Once{}, byKey: nil}
}

// Versions returns the schema versions containing the component, oldest first.
//
// The context is the asking run's: the first question is what walks the store,
// which for a remote registry means a fetch per release. It is built once, so
// the first question's context is the one that build runs under; a later
// question is answered from the map.
func (v *VersionIndex) Versions(ctx context.Context, k config.Kind, typ string) []string {
	v.once.Do(func() { v.build(ctx) })

	return v.byKey[versionKey{k, typ}]
}

func (v *VersionIndex) build(ctx context.Context) {
	v.byKey = map[versionKey][]string{}
	versions := v.store.Versions(ctx)
	// Store.Versions is newest first; walk backwards so results read oldest
	// first, which is how a "added in ..." hint wants to be read.
	for i := len(versions) - 1; i >= 0; i-- {
		c, err := v.store.Load(ctx, versions[i])
		if err != nil {
			continue
		}

		for kind, byType := range c.Components {
			for typ := range byType {
				key := versionKey{kind, typ}
				v.byKey[key] = append(v.byKey[key], versions[i])
			}
		}
	}
}
