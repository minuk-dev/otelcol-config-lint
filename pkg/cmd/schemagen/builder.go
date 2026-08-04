package schemagen

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// Errors reported for a manifest that cannot describe a distribution.
var (
	// errNoDistName reports a manifest with no dist.name to file the schema under.
	errNoDistName = errors.New("dist.name is required")
	// errNoDistVersion reports a manifest with no release to record.
	errNoDistVersion = errors.New("dist.otelcol_version or dist.version is required")
	// errNoComponents reports a manifest that declares no components at all.
	errNoComponents = errors.New("declares no components")
	// errBadGoMod reports a gomod line that is not "module version".
	errBadGoMod = errors.New(`not in "module version" form`)
)

// manifest is the subset of an OCB builder configuration schemagen needs. It is
// decoded leniently: the file is written for the builder, which keeps adding
// keys, and an unknown one says nothing about the components.
type manifest struct {
	Dist struct {
		Module string `yaml:"module"`
		Name   string `yaml:"name"`
		// Version is the distribution's own version; OtelColVersion is the
		// upstream release it is built from, and is what a config is linted
		// against.
		Version        string `yaml:"version"`
		OtelColVersion string `yaml:"otelcol_version"` //nolint:tagliatelle // the builder's own spelling
		// OutputPath is where the builder generates the collector's go.mod,
		// which is what the manifest's relative replacements are written
		// against -- not the manifest's own directory.
		OutputPath string `yaml:"output_path"` //nolint:tagliatelle // the builder's own spelling
	} `yaml:"dist"`
	Receivers  []manifestComponent `yaml:"receivers"`
	Processors []manifestComponent `yaml:"processors"`
	Exporters  []manifestComponent `yaml:"exporters"`
	Extensions []manifestComponent `yaml:"extensions"`
	Connectors []manifestComponent `yaml:"connectors"`
	// Replaces redirects a module elsewhere, in go.mod's "old => new" form. A
	// distribution under development points its own components at a checkout,
	// and the schema has to be read from there rather than from the proxy.
	Replaces []string `yaml:"replaces"`

	// dir is the directory the manifest was read from, which is what its
	// relative replacements are written against.
	dir string
}

// manifestComponent is one entry under a component section.
type manifestComponent struct {
	// GoMod is "<module> <version>", the form the builder takes.
	GoMod string `yaml:"gomod"`
	// Name overrides the type when the module name does not spell it.
	Name string `yaml:"name"`
}

// declared is one component the manifest asks the builder to compile in.
type declared struct {
	kind    config.Kind
	module  string
	version string
	// name is the type the manifest named, if it named one.
	name string
}

// splitBuilder splits a --builder value into the distribution name to file the
// schema under and the manifest to read. The name is optional: it is given when
// the registry spells a distribution differently from the manifest, as it does
// for the upstream releases, where "otelcol" is filed as "core".
func splitBuilder(value string) (string, string) {
	name, path, found := strings.Cut(value, "=")
	if !found {
		return "", value
	}

	return strings.TrimSpace(name), strings.TrimSpace(path)
}

// readManifest loads one OCB builder configuration. A non-empty name overrides
// the distribution the manifest names itself.
func readManifest(path, name string) (*manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}

	var parsed manifest

	err = yaml.Unmarshal(raw, &parsed)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	if name != "" {
		parsed.Dist.Name = name
	}

	if parsed.Dist.Name == "" {
		return nil, fmt.Errorf("%s: %w", path, errNoDistName)
	}

	if parsed.collectorVersion() == "" {
		return nil, fmt.Errorf("%s: %w", path, errNoDistVersion)
	}

	if len(parsed.components()) == 0 {
		return nil, fmt.Errorf("%s: %w", path, errNoComponents)
	}

	parsed.dir = filepath.Dir(path)

	return &parsed, nil
}

// collectorVersion is the upstream release the distribution is built from,
// which is what the schema is filed under. A manifest that only carries its own
// version is taken at its word: for the upstream release manifests the two are
// the same number.
func (m *manifest) collectorVersion() string {
	if m.Dist.OtelColVersion != "" {
		return schema.Normalize(m.Dist.OtelColVersion)
	}

	return schema.Normalize(m.Dist.Version)
}

// components lists every declared component, in kind order.
func (m *manifest) components() []declared {
	sections := []struct {
		kind    config.Kind
		entries []manifestComponent
	}{
		{config.KindReceiver, m.Receivers},
		{config.KindProcessor, m.Processors},
		{config.KindExporter, m.Exporters},
		{config.KindExtension, m.Extensions},
		{config.KindConnector, m.Connectors},
	}

	var out []declared

	for _, section := range sections {
		for _, entry := range section.entries {
			module, version, err := splitGoMod(entry.GoMod)
			if err != nil {
				continue // reported when the modules are resolved
			}

			out = append(out, declared{
				kind:    section.kind,
				module:  module,
				version: version,
				name:    entry.Name,
			})
		}
	}

	return out
}

// requires lists every "module version" the manifest asks for, including the
// entries that could not be parsed, so resolving them reports the bad line.
func (m *manifest) requires() ([]declared, error) {
	var out []declared

	for _, entry := range m.rawGoMods() {
		module, version, err := splitGoMod(entry)
		if err != nil {
			return nil, err
		}

		out = append(out, declared{module: module, version: version, kind: "", name: ""})
	}

	return out, nil
}

func (m *manifest) rawGoMods() []string {
	var out []string

	for _, section := range [][]manifestComponent{
		m.Receivers, m.Processors, m.Exporters, m.Extensions, m.Connectors,
	} {
		for _, entry := range section {
			out = append(out, entry.GoMod)
		}
	}

	return out
}

// replacements are the manifest's replacements with every local path made
// absolute. The builder writes them against the manifest, and they are read
// from a workspace of our own somewhere else entirely.
func (m *manifest) replacements() []string {
	out := make([]string, 0, len(m.Replaces))

	for _, replace := range m.Replaces {
		module, replacement, found := strings.Cut(replace, "=>")
		if !found {
			out = append(out, strings.TrimSpace(replace))

			continue
		}

		target := strings.TrimSpace(replacement)
		if isLocalPath(target) && !filepath.IsAbs(target) {
			target = filepath.Join(m.replaceBase(), target)
		}

		out = append(out, strings.TrimSpace(module)+" => "+target)
	}

	return out
}

// replaceBase is the directory a relative replacement is written against: the
// one the builder generates its go.mod in. Upstream's contrib manifest points
// at "../../../internal/obi-src", which is three levels up from
// "distributions/otelcol-contrib/_build" -- the repository root -- and only two
// from the manifest itself.
func (m *manifest) replaceBase() string {
	out := m.Dist.OutputPath

	switch {
	case out == "":
		// The builder would default to a temporary directory, which no
		// relative path can be meant against; the manifest is the best guess.
		return m.dir
	case filepath.IsAbs(out):
		return out
	default:
		return filepath.Join(m.dir, out)
	}
}

// isLocalPath reports a replacement that names a directory rather than a
// module, which go.mod spells with a leading dot or slash.
func isLocalPath(target string) bool {
	return strings.HasPrefix(target, "./") ||
		strings.HasPrefix(target, "../") ||
		strings.HasPrefix(target, "/") ||
		filepath.IsAbs(target)
}

// splitGoMod splits the builder's "<module> <version>" into its two halves.
func splitGoMod(gomod string) (string, string, error) {
	module, version, found := strings.Cut(strings.TrimSpace(gomod), " ")
	if !found || module == "" || version == "" {
		return "", "", fmt.Errorf("gomod %q is %w", gomod, errBadGoMod)
	}

	return module, strings.TrimSpace(version), nil
}

// typeName is the component type an entry declares. The manifest names it only
// when it differs from the convention every upstream component follows, where
// the module's last element is the type with the kind appended.
func (d declared) typeName() string {
	if d.name != "" {
		return d.name
	}

	base := path.Base(d.module)

	return strings.TrimSuffix(base, string(d.kind))
}
