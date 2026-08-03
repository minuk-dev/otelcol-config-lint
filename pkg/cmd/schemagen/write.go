package schemagen

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// dirPerm is the mode new directories are created with.
const dirPerm = 0o750

// writeDistributions splits a merged schema into one file per distribution,
// under "<out>/<distribution>/<version>.<format>". No union is written: it
// would be exactly the distributions put back together, and no collector ships
// it, so checking against one could only hide a component the binary lacks.
func (o *Options) writeDistributions(cat *schema.Schema, formats []schema.Format) error {
	for _, dist := range distributionsIn(cat) {
		sub := filterDistribution(cat, dist)

		// An empty schema would report every component as unknown, which is
		// worse than having no schema for the release at all.
		if sub.Count() == 0 {
			continue
		}

		dir := filepath.Join(o.outDir, dist)

		err := os.MkdirAll(dir, dirPerm)
		if err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}

		for _, format := range formats {
			dest := filepath.Join(dir, sub.CollectorVersion+"."+string(format))

			err := write(dest, sub, format)
			if err != nil {
				return err
			}
		}

		o.logf("  %s: %d components\n", dist, sub.Count())
	}

	return nil
}

// distributionsIn returns every distribution any component in the schema
// ships in, sorted.
func distributionsIn(cat *schema.Schema) []string {
	seen := map[string]bool{}

	for _, byType := range cat.Components {
		for _, comp := range byType {
			for _, d := range comp.Distributions {
				seen[d] = true
			}
		}
	}

	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}

	sort.Strings(out)

	return out
}

// filterDistribution copies the schema down to the components one
// distribution ships.
func filterDistribution(cat *schema.Schema, dist string) *schema.Schema {
	out := *cat
	out.Distribution = dist
	out.Components = map[config.Kind]map[string]*schema.Component{}

	for kind, byType := range cat.Components {
		for typ, comp := range byType {
			if !slices.Contains(comp.Distributions, dist) {
				continue
			}

			if out.Components[kind] == nil {
				out.Components[kind] = map[string]*schema.Component{}
			}

			// The written component drops the distribution list: the
			// directory it lands in already says which binary ships it. The
			// copy matters because the merged catalogue is filtered once per
			// distribution and still needs the list.
			written := *comp
			written.Distributions = nil
			out.Components[kind][typ] = &written
		}
	}

	return &out
}

// writeIndex records what the registry can serve. It is rebuilt by listing the
// output directory, not from the versions generated in this run, so
// regenerating one release leaves the others listed. Versions are recorded per
// distribution because coverage differs: upstream had no otlp distribution
// before v0.120.0.
func (o *Options) writeIndex() error {
	entries, err := os.ReadDir(o.outDir)
	if err != nil {
		return fmt.Errorf("read %s: %w", o.outDir, err)
	}

	idx := &schema.Index{Distributions: map[string][]string{}}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		versions := versionsIn(filepath.Join(o.outDir, e.Name()))
		if len(versions) == 0 {
			// Not a distribution, just a directory that happens to sit here.
			continue
		}

		idx.Distributions[e.Name()] = versions
	}

	dest := filepath.Join(o.outDir, schema.IndexFile)

	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create %s: %w", dest, err)
	}

	err = idx.Write(f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}

	if err != nil {
		return err
	}

	o.logf("wrote %s (%d distributions)\n", dest, len(idx.Distributions))

	return nil
}

// versionsIn lists the releases a distribution directory holds, in any of the
// formats a schema may be written in.
func versionsIn(dir string) []string {
	seen := map[string]bool{}

	var out []string

	for _, ext := range schema.Extensions() {
		names, _ := filepath.Glob(filepath.Join(dir, "*"+ext))
		for _, n := range names {
			v := strings.TrimSuffix(filepath.Base(n), ext)
			if !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}

	return out
}

// write serialises a schema to disk, replacing whatever was there before.
func write(dest string, cat *schema.Schema, format schema.Format) error {
	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create %s: %w", dest, err)
	}

	err = cat.Write(f, format)
	if cerr := f.Close(); err == nil {
		err = cerr
	}

	return err
}
