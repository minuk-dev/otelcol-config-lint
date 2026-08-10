package schema

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/spf13/afero"
)

// IndexFile is the name a registry publishes its index under.
const IndexFile = "index.json"

// DefaultDistribution is checked against when none is chosen. It is the
// distribution most collectors are built from, and the widest published one, so
// it is the least likely to report a component the running binary does have.
const DefaultDistribution = "contrib"

// Index lists what a registry can serve. A directory can be listed to work that
// out, but a remote registry cannot, so it publishes the answer as a file.
type Index struct {
	// Distributions maps each distribution to the releases it has a schema
	// for, newest first. Coverage differs between them: upstream had no otlp
	// distribution before v0.120.0, so asking a flat list of versions what the
	// otlp registry holds would name releases it cannot serve.
	Distributions map[string][]string `json:"distributions"`
}

// Names returns the distributions the index covers, sorted.
func (i *Index) Names() []string {
	out := make([]string, 0, len(i.Distributions))
	for name := range i.Distributions {
		out = append(out, name)
	}

	sort.Strings(out)

	return out
}

// Versions returns the releases one distribution has schemas for, newest
// first. An unknown distribution has none.
func (i *Index) Versions(distribution string) []string {
	return i.Distributions[distribution]
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
	return readIndexFile(afero.NewOsFs(), path)
}

// readIndexFile decodes a registry index from a file on the given filesystem.
func readIndexFile(fsys afero.Fs, path string) (*Index, error) {
	f, err := fsys.Open(path)
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

// Sort puts each distribution's versions newest first, so a regenerated index
// diffs cleanly.
func (i *Index) Sort() {
	for _, versions := range i.Distributions {
		sort.Slice(versions, func(a, b int) bool { return Compare(versions[a], versions[b]) > 0 })
	}
}

// Has reports whether the index covers a distribution.
func (i *Index) Has(distribution string) bool {
	_, ok := i.Distributions[distribution]

	return ok
}
