package schemagen

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// errIncompleteOverlay reports an overlay missing its kind or type.
var errIncompleteOverlay = errors.New("kind and type are required")

// overlay adds a field schema to a component the upstream metadata cannot
// describe.
type overlay struct {
	Kind       config.Kind   `yaml:"kind"`
	Type       string        `yaml:"type"`
	MinVersion string        `yaml:"minVersion"`
	MaxVersion string        `yaml:"maxVersion"`
	Fields     *schema.Field `yaml:"fields"`
}

func (o *Options) loadOverlays() ([]overlay, error) {
	var out []overlay

	err := filepath.WalkDir(o.overlayDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil // overlays are optional
			}

			return err
		}

		if d.IsDir() || (filepath.Ext(p) != ".yaml" && filepath.Ext(p) != ".yml") {
			return nil
		}

		raw, err := os.ReadFile(p) //nolint:gosec // the overlay directory is a build input, not user data
		if err != nil {
			return fmt.Errorf("read overlay %s: %w", p, err)
		}

		var spec overlay

		err = yaml.Unmarshal(raw, &spec)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}

		if spec.Type == "" || spec.Kind == "" {
			return fmt.Errorf("%s: %w", p, errIncompleteOverlay)
		}

		out = append(out, spec)

		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("load overlays: %w", err)
	}

	o.logf("loaded %d field overlay(s)\n", len(out))

	return out, nil
}

func applyOverlays(cat *schema.Schema, overlays []overlay) {
	for _, spec := range overlays {
		if spec.MinVersion != "" && schema.Compare(cat.CollectorVersion, spec.MinVersion) < 0 {
			continue
		}

		if spec.MaxVersion != "" && schema.Compare(cat.CollectorVersion, spec.MaxVersion) > 0 {
			continue
		}

		comp, ok := cat.Lookup(spec.Kind, spec.Type)
		if !ok {
			continue // the component does not exist in this release
		}

		comp.Fields = spec.Fields
	}
}
