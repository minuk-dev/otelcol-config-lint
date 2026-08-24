package schemagen

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/minuk-dev/otelcol-config-lint/pkg/config"
	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// The modes new directories and files are created with.
const (
	dirPerm  = 0o750
	filePerm = 0o600
)

// stdoutMarker is the --out value that writes to stdout, spelled the way every
// unix filter spells it.
const stdoutMarker = "-"

// writeFile writes one distribution's schema where the command line asked for
// it, which is where a single run's output belongs: the registry layout below
// is derived from the manifest, so it can only name one place.
func (o *Options) writeFile(cat *schema.Schema, formats []schema.Format) error {
	if o.outFile == stdoutMarker || o.outFile == "" {
		return writeTo(o.out, cat, formatFor(o.outFile, formats))
	}

	err := write(o.outFile, cat, formatFor(o.outFile, formats))
	if err != nil {
		return err
	}

	o.logf("  %s: %d components\n", o.outFile, cat.Count())

	return nil
}

// formatFor is the format a single file is written in: what its name implies,
// so "schema.json" is JSON, and failing that the first --formats entry.
func formatFor(path string, formats []schema.Format) schema.Format {
	if strings.HasSuffix(path, ".json") || strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml") {
		return schema.FormatOf(path)
	}

	return formats[0]
}

// writeRegistry files one distribution's schema under
// "<registry>/<distribution>/<version>.<format>", which is the layout the
// registry is read back in.
func (o *Options) writeRegistry(cat *schema.Schema, formats []schema.Format) error {
	dir := filepath.Join(o.registryDir, cat.Distribution)

	err := os.MkdirAll(dir, dirPerm)
	if err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}

	for _, format := range formats {
		dest := filepath.Join(dir, cat.CollectorVersion+"."+string(format))

		err := write(dest, cat, format)
		if err != nil {
			return err
		}
	}

	o.logf("  %s %s: %d components\n", cat.Distribution, cat.CollectorVersion, cat.Count())

	return nil
}

// writeIndex records what the registry can serve, and returns it so that what
// is published beside it describes the same releases. It is rebuilt by listing
// the registry directory, not from the manifests generated in this run, so
// regenerating one distribution leaves the others listed.
func (o *Options) writeIndex() (*schema.Index, error) {
	entries, err := os.ReadDir(o.registryDir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", o.registryDir, err)
	}

	idx := &schema.Index{Distributions: map[string][]string{}, Extensions: map[string]string{}}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		dir := filepath.Join(o.registryDir, e.Name())

		versions := versionsIn(dir)
		if len(versions) == 0 {
			// Not a distribution, just a directory that happens to sit here.
			continue
		}

		idx.Distributions[e.Name()] = versions

		ext := extensionIn(dir, versions)
		if ext != "" {
			idx.Extensions[e.Name()] = ext
		}
	}

	dest := filepath.Join(o.registryDir, schema.IndexFile)

	err = writeDoc(dest, idx.Write)
	if err != nil {
		return nil, err
	}

	o.logf("wrote %s (%d distributions)\n", dest, len(idx.Distributions))

	return idx, nil
}

// writeComponents records which releases ship each component type, so that a
// linter meeting a component the targeted release does not have can say where
// it does exist without downloading every schema in the registry to find out.
//
// It is rebuilt from the schemas the registry is left holding, for the same
// reason the index is: a run that regenerated one distribution must not
// republish the others from what this run happens to have in hand.
func (o *Options) writeComponents(idx *schema.Index) error {
	comps := &schema.Components{Distributions: map[string]schema.ComponentSpans{}}

	for _, dist := range idx.Names() {
		versions := idx.Versions(dist)

		spans, err := o.spansIn(dist, versions)
		if err != nil {
			// A distribution whose releases could not all be read is left out
			// rather than described from the ones that could: a release
			// missing from the walk reads as a component that was dropped and
			// added back. A reader that finds no entry falls back to the
			// schemas, which is where it was before this file existed.
			o.logf("  %s: no availability published (%v)\n", dist, err)

			continue
		}

		comps.Distributions[dist] = spans
	}

	dest := filepath.Join(o.registryDir, schema.ComponentsFile)

	err := writeDoc(dest, comps.Write)
	if err != nil {
		return err
	}

	o.logf("wrote %s (%d distributions)\n", dest, len(comps.Distributions))

	return nil
}

// spansIn works out which of a distribution's releases ship each component
// type, by reading every schema the registry holds for it.
func (o *Options) spansIn(dist string, versions []string) (schema.ComponentSpans, error) {
	// kind -> type -> the releases shipping it.
	present := map[config.Kind]map[string]map[string]bool{}

	for _, v := range versions {
		cat, err := readRegistrySchema(filepath.Join(o.registryDir, dist), v)
		if err != nil {
			return nil, err
		}

		for kind, byType := range cat.Components {
			byName, ok := present[kind]
			if !ok {
				byName = map[string]map[string]bool{}
				present[kind] = byName
			}

			for typ := range byType {
				if byName[typ] == nil {
					byName[typ] = map[string]bool{}
				}

				byName[typ][v] = true
			}
		}
	}

	out := schema.ComponentSpans{}

	for kind, byType := range present {
		out[kind] = make(map[string][]schema.Span, len(byType))

		for typ, releases := range byType {
			out[kind][typ] = schema.SpansOf(versions, releases)
		}
	}

	return out, nil
}

// readRegistrySchema reads one release out of a distribution's directory,
// whichever form it was published in. JSON comes first here, unlike everywhere
// else: this reads the whole registry rather than one file, and the two forms
// hold the same schema.
func readRegistrySchema(dir, version string) (*schema.Schema, error) {
	exts := []string{".json"}

	for _, ext := range schema.Extensions() {
		if ext != ".json" {
			exts = append(exts, ext)
		}
	}

	for _, ext := range exts {
		path := filepath.Join(dir, version+ext)

		_, err := os.Stat(path)
		if err != nil {
			continue
		}

		return schema.ReadFile(path)
	}

	return nil, fmt.Errorf("%w: %s", errNoSchemaFile, filepath.Join(dir, version))
}

// errNoSchemaFile reports a release the index lists but the registry has no
// file for, which is a registry the run should not describe as if it did.
var errNoSchemaFile = errors.New("no schema file")

// writeDoc writes one of the documents published beside the schemas, replacing
// whatever was there before.
func writeDoc(dest string, encode func(io.Writer) error) error {
	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create %s: %w", dest, err)
	}

	err = encode(f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}

	return err
}

// extensionIn returns the file extension a distribution's schemas should be
// fetched with: the form every release in the directory is served as, which is
// the preferred one where a release carries several. It is recorded in the
// index so that a remote fetch asks once instead of probing each extension in
// turn.
//
// Releases that disagree -- older ones published only as JSON, say, and newer
// ones as YAML -- have no single answer, and an index naming one of them would
// send half the fetches at a file that is not there. Those record nothing, and
// are probed as before.
func extensionIn(dir string, versions []string) string {
	answer := ""

	for _, v := range versions {
		found := ""

		for _, ext := range schema.Extensions() {
			_, err := os.Stat(filepath.Join(dir, v+ext))
			if err == nil {
				found = ext

				break
			}
		}

		if answer != "" && found != answer {
			return ""
		}

		answer = found
	}

	return answer
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

// writeTo serialises a schema to an open stream, which is what --out "-" does.
func writeTo(w io.Writer, cat *schema.Schema, format schema.Format) error {
	err := cat.Write(w, format)
	if err != nil {
		return fmt.Errorf("write schema: %w", err)
	}

	return nil
}
