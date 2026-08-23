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
	// ctx is the run's, so a build that reaches the network is cancelled with
	// it. The index is built lazily, deep inside a rule that asked a question,
	// so there is no call to carry it on instead.
	//
	//nolint:containedctx // the index is lazy; the context cannot arrive with the question
	ctx   context.Context
	store schema.Store

	once  sync.Once
	byKey map[versionKey][]string
}

type versionKey struct {
	kind config.Kind
	typ  string
}

// NewVersionIndex returns an index over the schemas in store. It is built on
// first use, under ctx, which is the run's.
func NewVersionIndex(ctx context.Context, store schema.Store) *VersionIndex {
	return &VersionIndex{ctx: ctx, store: store, once: sync.Once{}, byKey: nil}
}

// Versions returns the schema versions containing the component, oldest first.
func (v *VersionIndex) Versions(k config.Kind, typ string) []string {
	v.once.Do(v.build)

	return v.byKey[versionKey{k, typ}]
}

func (v *VersionIndex) build() {
	v.byKey = map[versionKey][]string{}
	versions := v.store.Versions(v.ctx)
	// Store.Versions is newest first; walk backwards so results read oldest
	// first, which is how a "added in ..." hint wants to be read.
	for i := len(versions) - 1; i >= 0; i-- {
		c, err := v.store.Load(v.ctx, versions[i])
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
