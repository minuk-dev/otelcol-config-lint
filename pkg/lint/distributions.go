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
	store   schema.Store
	version string

	once  sync.Once
	byKey map[versionKey][]string
}

// NewDistributionIndex returns an index over the sibling distributions store
// can serve at the given collector release.
func NewDistributionIndex(store schema.Store, version string) *DistributionIndex {
	return &DistributionIndex{store: store, version: version, once: sync.Once{}, byKey: nil}
}

// Distributions returns the distributions shipping the component, sorted. The
// one already being checked against is not among them: the caller knows it is
// absent there, which is why it is asking.
//
// The context is the asking run's, on the same terms as VersionIndex.Versions:
// the first question is what reads the sibling distributions.
func (d *DistributionIndex) Distributions(ctx context.Context, k config.Kind, typ string) []string {
	d.once.Do(func() { d.build(ctx) })

	return d.byKey[versionKey{k, typ}]
}

func (d *DistributionIndex) build(ctx context.Context) {
	d.byKey = map[versionKey][]string{}

	for _, dist := range d.store.Distributions(ctx) {
		if dist == d.store.Distribution {
			continue
		}

		c, err := d.store.WithDistribution(dist).Load(ctx, d.version)
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
