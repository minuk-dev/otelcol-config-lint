package schemagen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// summarise records what one generated schema changes against what the
// registry served for that distribution up to now.
//
// Which file that is depends on whether the release is new. A new one is
// compared against the release before it, since what a reviewer wants to know
// is what upstream changed. A release the registry already holds is compared
// against itself: it is being regenerated because the generator now describes
// something differently -- a component it leaves open rather than closing over
// half of it, say -- and that change is what there is to review. Comparing it
// against the release before it would answer the other question, and in a
// backfill run generating oldest first it would answer it against a file this
// same run has just rewritten, so the change being made would appear nowhere.
//
// It runs before the schema is written, which is what leaves the old file
// there to compare against. A distribution with nothing to compare against
// still gets an entry, saying how large its first schema is.
func (o *options) summarise(cat *schema.Schema) {
	if o.summaryFile == "" || o.registryDir == "" {
		return
	}

	dir := filepath.Join(o.registryDir, cat.Distribution)

	previous := readVersion(dir, cat.CollectorVersion)
	if previous == nil {
		previous = previousIn(dir, cat.CollectorVersion)
	}

	o.diffs = append(o.diffs, schema.DiffSchemas(previous, cat))
}

// previousIn reads the newest release a distribution directory holds that is
// older than version, or nil when it holds none.
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

	return readVersion(dir, newest)
}

// readVersion reads one release out of a distribution directory, in whichever
// form it is filed, or nil when the directory does not hold it. A file that
// cannot be read is treated as absent: a summary is a convenience, and failing
// the run over it would throw away the schemas that did generate.
func readVersion(dir, version string) *schema.Schema {
	for _, ext := range schema.Extensions() {
		cat, err := schema.ReadFile(filepath.Join(dir, version+ext))
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
