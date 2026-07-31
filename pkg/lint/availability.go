package lint

import (
	"sync"

	"github.com/minuk-dev/otel-collector-config-linter/pkg/catalog"
	"github.com/minuk-dev/otel-collector-config-linter/pkg/config"
)

// VersionIndex answers which collector releases ship a component type, by
// consulting every catalog a store can serve. It is built on first use, so runs
// that never hit an unknown component pay nothing for it.
type VersionIndex struct {
	store catalog.Store

	once  sync.Once
	byKey map[versionKey][]string
}

type versionKey struct {
	kind config.Kind
	typ  string
}

// NewVersionIndex returns an index over the catalogs in store.
func NewVersionIndex(store catalog.Store) *VersionIndex {
	return &VersionIndex{store: store}
}

// Versions returns the catalog versions containing the component, oldest first.
func (v *VersionIndex) Versions(k config.Kind, typ string) []string {
	v.once.Do(v.build)
	return v.byKey[versionKey{k, typ}]
}

func (v *VersionIndex) build() {
	v.byKey = map[versionKey][]string{}
	versions := v.store.Versions()
	// Store.Versions is newest first; walk backwards so results read oldest
	// first, which is how a "added in ..." hint wants to be read.
	for i := len(versions) - 1; i >= 0; i-- {
		c, err := v.store.Load(versions[i])
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
