package catalog

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
)

// IndexFile is the name a registry publishes its index under.
const IndexFile = "index.json"

// AllDistributions is the catalog holding every component, whichever
// distribution ships it. It is what the linter checked against before
// distributions were modelled at all, so it stays the default.
const AllDistributions = "all"

// Index lists what a registry can serve. A directory can be listed to work that
// out, but a remote registry cannot, so it publishes the answer as a file.
type Index struct {
	// Distributions are the binaries the registry has catalogs for.
	Distributions []string `json:"distributions"`
	// Versions are the collector releases it has catalogs for, newest first.
	Versions []string `json:"versions"`
}

// ReadIndex decodes a registry index.
func ReadIndex(r io.Reader) (*Index, error) {
	var idx Index

	err := json.NewDecoder(r).Decode(&idx)
	if err != nil {
		return nil, fmt.Errorf("decode index: %w", err)
	}

	idx.Sort()

	return &idx, nil
}

// ReadIndexFile decodes a registry index from a file on disk.
func ReadIndexFile(path string) (*Index, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open index: %w", err)
	}

	defer func() { _ = f.Close() }()

	idx, err := ReadIndex(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	return idx, nil
}

// Write encodes the index as JSON.
func (i *Index) Write(w io.Writer) error {
	i.Sort()

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	err := enc.Encode(i)
	if err != nil {
		return fmt.Errorf("encode index: %w", err)
	}

	return nil
}

// Sort puts the index in its canonical order: distributions alphabetically,
// versions newest first, so a regenerated index diffs cleanly.
func (i *Index) Sort() {
	sort.Strings(i.Distributions)
	sort.Slice(i.Versions, func(a, b int) bool { return Compare(i.Versions[a], i.Versions[b]) > 0 })
}

// Has reports whether the index lists a distribution.
func (i *Index) Has(distribution string) bool {
	return slices.Contains(i.Distributions, distribution)
}
