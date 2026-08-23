package lint

import (
	"context"
	"sync"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// DistributionIndex answers which distributions ship a component type at one
// collector release. A schema holds only the distribution it describes, so a
// component missing from it is simply absent, and saying where it does ship
// means consulting the siblings.
//
// Like VersionIndex it is built on first use, so a run that never meets an
// unknown component pays nothing for it.
type DistributionIndex struct {
	// ctx is the run's, for the same reason VersionIndex holds one: the index
	// is built inside the rule that asked, not at the call that made it.
	//
	//nolint:containedctx // the index is lazy; the context cannot arrive with the question
	ctx     context.Context
	store   schema.Store
	version string

	once  sync.Once
	byKey map[versionKey][]string
}

// NewDistributionIndex returns an index over the sibling distributions store
// can serve at the given collector release.
func NewDistributionIndex(ctx context.Context, store schema.Store, version string) *DistributionIndex {
	return &DistributionIndex{ctx: ctx, store: store, version: version, once: sync.Once{}, byKey: nil}
}

// Distributions returns the distributions shipping the component, sorted. The
// one already being checked against is not among them: the caller knows it is
// absent there, which is why it is asking.
func (d *DistributionIndex) Distributions(k config.Kind, typ string) []string {
	d.once.Do(d.build)

	return d.byKey[versionKey{k, typ}]
}

func (d *DistributionIndex) build() {
	d.byKey = map[versionKey][]string{}

	for _, dist := range d.store.Distributions(d.ctx) {
		if dist == d.store.Distribution {
			continue
		}

		c, err := d.store.WithDistribution(dist).Load(d.ctx, d.version)
		if err != nil {
			continue
		}

		for kind, byType := range c.Components {
			for typ := range byType {
				key := versionKey{kind, typ}
				d.byKey[key] = append(d.byKey[key], dist)
			}
		}
	}
}
