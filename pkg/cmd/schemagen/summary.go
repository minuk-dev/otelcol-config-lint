package schemagen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// summarise records what one generated schema changes against the newest
// release the registry already holds for that distribution.
//
// It runs before the schema is written, so the comparison is against what the
// registry served up to now even when a release is regenerated in place. A
// distribution with nothing to compare against still gets an entry, saying how
// large its first schema is.
func (o *options) summarise(cat *schema.Schema) {
	if o.summaryFile == "" || o.registryDir == "" {
		return
	}

	previous := previousIn(filepath.Join(o.registryDir, cat.Distribution), cat.CollectorVersion)

	o.diffs = append(o.diffs, schema.DiffSchemas(previous, cat))
}

// previousIn reads the newest release a distribution directory holds that is
// older than version, or nil when it holds none. A file that cannot be read is
// treated as absent: a summary is a convenience, and failing the run over it
// would throw away the schemas that did generate.
func previousIn(dir, version string) *schema.Schema {
	var newest string

	for _, v := range versionsIn(dir) {
		if schema.Compare(v, version) >= 0 {
			continue
		}

		if newest == "" || schema.Compare(v, newest) > 0 {
			newest = v
		}
	}

	if newest == "" {
		return nil
	}

	for _, ext := range schema.Extensions() {
		cat, err := schema.ReadFile(filepath.Join(dir, newest+ext))
		if err == nil {
			return cat
		}
	}

	return nil
}

// writeSummary renders every diff the run collected, in the order the
// distributions were generated. The file is written even when nothing changed:
// a reviewer reading "no component changes" learns something, and a caller
// pasting the file into a pull request body should not have to handle a missing
// one.
func (o *options) writeSummary() error {
	if o.summaryFile == "" {
		return nil
	}

	var b strings.Builder

	for i, d := range o.diffs {
		if i > 0 {
			b.WriteString("\n")
		}

		b.WriteString(d.Markdown())
	}

	if len(o.diffs) == 0 {
		b.WriteString("No schemas were generated.\n")
	}

	if o.summaryFile == stdoutMarker {
		_, err := fmt.Fprint(o.out, b.String())
		if err != nil {
			return fmt.Errorf("write summary: %w", err)
		}

		return nil
	}

	err := os.WriteFile(o.summaryFile, []byte(b.String()), filePerm)
	if err != nil {
		return fmt.Errorf("write summary: %w", err)
	}

	o.logf("wrote %s (%d distributions)\n", o.summaryFile, len(o.diffs))

	return nil
}
