package schemagen

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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

// writeIndex records what the registry can serve. It is rebuilt by listing the
// registry directory, not from the manifests generated in this run, so
// regenerating one distribution leaves the others listed.
func (o *Options) writeIndex() error {
	entries, err := os.ReadDir(o.registryDir)
	if err != nil {
		return fmt.Errorf("read %s: %w", o.registryDir, err)
	}

	idx := &schema.Index{Distributions: map[string][]string{}}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		versions := versionsIn(filepath.Join(o.registryDir, e.Name()))
		if len(versions) == 0 {
			// Not a distribution, just a directory that happens to sit here.
			continue
		}

		idx.Distributions[e.Name()] = versions
	}

	dest := filepath.Join(o.registryDir, schema.IndexFile)

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

// writeTo serialises a schema to an open stream, which is what --out "-" does.
func writeTo(w io.Writer, cat *schema.Schema, format schema.Format) error {
	err := cat.Write(w, format)
	if err != nil {
		return fmt.Errorf("write schema: %w", err)
	}

	return nil
}
