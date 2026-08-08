package schemagen

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/minuk-dev/otelcol-config-lint/pkg/schema"
)

// prune drops the releases a registry is no longer meant to serve.
//
// A registry that gains a schema for every upstream release grows without
// bound -- upstream tags roughly every other week, and one release is several
// megabytes across the distributions and formats -- so a run that fills one on
// a schedule needs to say how far back it keeps. The policy is two numbers: how
// many of the newest releases to keep, and which older ones are milestones that
// are never dropped, so a config pinned to a round version keeps resolving long
// after the releases either side of it are gone.
//
// Pruning bounds what the registry serves and what a checkout costs, not the
// history of the repository holding it; the files stay in every commit that had
// them.
func (o *Options) prune() error {
	if o.retain <= 0 {
		return nil
	}

	entries, err := os.ReadDir(o.registryDir)
	if err != nil {
		return fmt.Errorf("read %s: %w", o.registryDir, err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		dir := filepath.Join(o.registryDir, e.Name())

		err := o.pruneDistribution(dir)
		if err != nil {
			return err
		}
	}

	return nil
}

// pruneDistribution removes the schema files of one distribution's dropped
// releases, in every format they were written in.
func (o *Options) pruneDistribution(dir string) error {
	dropped := drop(versionsIn(dir), o.retain, o.retainEvery)

	for _, v := range dropped {
		for _, ext := range schema.Extensions() {
			err := os.Remove(filepath.Join(dir, v+ext))
			if err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove %s: %w", filepath.Join(dir, v+ext), err)
			}
		}
	}

	if len(dropped) > 0 {
		o.logf("  %s: dropped %s\n", filepath.Base(dir), strings.Join(dropped, ", "))
	}

	return nil
}

// drop returns the releases the policy does not keep: everything past the keep
// newest, minus the milestones, whose minor version is a multiple of every. A
// zero every keeps no milestones, which is how a caller asks for a flat window.
func drop(versions []string, keep, every int) []string {
	sorted := append([]string(nil), versions...)
	sort.Slice(sorted, func(a, b int) bool { return schema.Compare(sorted[a], sorted[b]) > 0 })

	var out []string

	for i, v := range sorted {
		if i < keep || isMilestone(v, every) {
			continue
		}

		out = append(out, v)
	}

	return out
}

// isMilestone reports whether a release is one the policy keeps regardless of
// its age. A version whose minor cannot be read is treated as a milestone: it
// is not a release this tool wrote, so it is not this tool's to delete.
func isMilestone(version string, every int) bool {
	if every <= 0 {
		return false
	}

	minor, ok := minorOf(version)
	if !ok {
		return true
	}

	return minor%every == 0
}

// minorOf returns the Y of a "vX.Y.Z" release.
func minorOf(version string) (int, bool) {
	parts := strings.Split(strings.TrimPrefix(schema.Normalize(version), "v"), ".")

	const minorIndex = 1
	if len(parts) <= minorIndex {
		return 0, false
	}

	n, err := strconv.Atoi(parts[minorIndex])
	if err != nil {
		return 0, false
	}

	return n, true
}
