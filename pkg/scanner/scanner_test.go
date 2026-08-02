package scanner_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/minuk-dev/otelcol-config-lint/pkg/scanner"
	"github.com/minuk-dev/otelcol-config-lint/pkg/sets"
)

// tree writes each named file under a fresh temporary directory and returns it.
func tree(t *testing.T, names ...string) string {
	t.Helper()

	root := t.TempDir()

	for _, name := range names {
		path := filepath.Join(root, filepath.FromSlash(name))

		err := os.MkdirAll(filepath.Dir(path), 0o750)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(path, []byte("receivers:\n"), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	return root
}

// relative turns the scan result back into slash-separated paths under root, so
// the assertions read like the tree that was written.
func relative(t *testing.T, root string, files sets.Set[string]) []string {
	t.Helper()

	out := make([]string, 0, files.Len())

	for _, path := range sets.List(files) {
		if path == scanner.StdinMarker {
			out = append(out, path)

			continue
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatal(err)
		}

		out = append(out, filepath.ToSlash(rel))
	}

	return out
}

func TestScanWalksADirectory(t *testing.T) {
	t.Parallel()

	root := tree(t, "a.yaml", "b.yml", "nested/c.yaml", "notes.txt", "no-extension")

	files, err := scanner.New(nil).Scan([]string{root})
	if err != nil {
		t.Fatal(err)
	}

	got := relative(t, root, files)
	if want := []string{"a.yaml", "b.yml", "nested/c.yaml"}; !slices.Equal(got, want) {
		t.Errorf("Scan() = %v, want %v", got, want)
	}
}

func TestScanIsCaseInsensitiveAboutExtensions(t *testing.T) {
	t.Parallel()

	root := tree(t, "loud.YAML", "mixed.Yml")

	files, err := scanner.New(nil).Scan([]string{root})
	if err != nil {
		t.Fatal(err)
	}

	if files.Len() != 2 {
		t.Errorf("Scan() found %v, want both files", relative(t, root, files))
	}
}

func TestScanSkipsDotAndVendorDirectories(t *testing.T) {
	t.Parallel()

	root := tree(t,
		"keep.yaml",
		".git/config.yaml",
		"vendor/dep.yaml",
		"node_modules/pkg.yaml",
		"nested/.hidden/deep.yaml",
	)

	files, err := scanner.New(nil).Scan([]string{root})
	if err != nil {
		t.Fatal(err)
	}

	got := relative(t, root, files)
	if want := []string{"keep.yaml"}; !slices.Equal(got, want) {
		t.Errorf("Scan() = %v, want %v", got, want)
	}
}

// TestScanEntersADotDirectoryNamedDirectly pins that the skip applies to what a
// walk descends into, not to the root the caller asked for.
func TestScanEntersADotDirectoryNamedDirectly(t *testing.T) {
	t.Parallel()

	root := tree(t, ".config/app.yaml")

	files, err := scanner.New(nil).Scan([]string{filepath.Join(root, ".config")})
	if err != nil {
		t.Fatal(err)
	}

	got := relative(t, root, files)
	if want := []string{".config/app.yaml"}; !slices.Equal(got, want) {
		t.Errorf("Scan() = %v, want %v", got, want)
	}
}

func TestScanExcludePatterns(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		exclude []string
		want    []string
	}{
		"nothing excluded": {
			exclude: nil,
			want:    []string{"agent.yaml", "gen/agent.generated.yaml", "keep.yaml"},
		},
		"by base name": {
			exclude: []string{"agent.yaml"},
			want:    []string{"gen/agent.generated.yaml", "keep.yaml"},
		},
		"by glob on the base name": {
			exclude: []string{"*.generated.yaml"},
			want:    []string{"agent.yaml", "keep.yaml"},
		},
		"a starred pattern matches anywhere in the path": {
			exclude: []string{"*generated*"},
			want:    []string{"agent.yaml", "keep.yaml"},
		},
		// The stars are stripped and what is left is matched as a substring,
		// so a short pattern reaches further than it looks: "gen" is inside
		// "agent". Surprising, but it is what the flag has always done.
		"a starred pattern matches on a bare substring": {
			exclude: []string{"*gen*"},
			want:    []string{"keep.yaml"},
		},
		"several patterns": {
			exclude: []string{"agent.yaml", "*.generated.yaml"},
			want:    []string{"keep.yaml"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			root := tree(t, "agent.yaml", "keep.yaml", "gen/agent.generated.yaml")

			files, err := scanner.New(tt.exclude).Scan([]string{root})
			if err != nil {
				t.Fatal(err)
			}

			if got := relative(t, root, files); !slices.Equal(got, tt.want) {
				t.Errorf("Scan() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestScanKeepsAnExplicitlyNamedFile pins that naming a file wins over the
// exclude patterns, which only govern directory walks.
func TestScanKeepsAnExplicitlyNamedFile(t *testing.T) {
	t.Parallel()

	root := tree(t, "agent.yaml")
	path := filepath.Join(root, "agent.yaml")

	files, err := scanner.New([]string{"agent.yaml"}).Scan([]string{path})
	if err != nil {
		t.Fatal(err)
	}

	if !files.Has(path) {
		t.Errorf("an explicitly named file should be kept, got %v", sets.List(files))
	}
}

// TestScanKeepsAnExplicitlyNamedFileWhateverItsExtension pins that the
// extension filter also only governs directory walks.
func TestScanKeepsAnExplicitlyNamedFileWhateverItsExtension(t *testing.T) {
	t.Parallel()

	root := tree(t, "config.txt")
	path := filepath.Join(root, "config.txt")

	files, err := scanner.New(nil).Scan([]string{path})
	if err != nil {
		t.Fatal(err)
	}

	if !files.Has(path) {
		t.Errorf("an explicitly named file should be kept, got %v", sets.List(files))
	}
}

func TestScanDeduplicates(t *testing.T) {
	t.Parallel()

	root := tree(t, "agent.yaml")
	path := filepath.Join(root, "agent.yaml")

	// Named through the walk, named directly, and named directly again with a
	// path that needs cleaning.
	unclean := filepath.Join(root, ".", "agent.yaml")

	files, err := scanner.New(nil).Scan([]string{root, path, unclean})
	if err != nil {
		t.Fatal(err)
	}

	if files.Len() != 1 {
		t.Errorf("Scan() = %v, want one file", sets.List(files))
	}
}

func TestScanPassesStdinThrough(t *testing.T) {
	t.Parallel()

	root := tree(t, "agent.yaml")

	files, err := scanner.New(nil).Scan([]string{scanner.StdinMarker, root})
	if err != nil {
		t.Fatal(err)
	}

	if !files.Has(scanner.StdinMarker) {
		t.Errorf("the stdin marker should survive, got %v", sets.List(files))
	}

	if files.Len() != 2 {
		t.Errorf("Scan() = %v, want the marker and the file", relative(t, root, files))
	}
}

func TestScanReportsAMissingPath(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "nope.yaml")

	_, err := scanner.New(nil).Scan([]string{missing})
	if err == nil {
		t.Fatal("want an error for a path that does not exist")
	}

	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the cause should survive wrapping, got %v", err)
	}
}

func TestScanOfNothingIsEmpty(t *testing.T) {
	t.Parallel()

	files, err := scanner.New(nil).Scan(nil)
	if err != nil {
		t.Fatal(err)
	}

	if files.Len() != 0 {
		t.Errorf("Scan(nil) = %v, want an empty set", sets.List(files))
	}
}

// TestScannerFieldsAreHonoured pins that the defaults New picks are only
// defaults, not something the walk hard-codes.
func TestScannerFieldsAreHonoured(t *testing.T) {
	t.Parallel()

	root := tree(t, "a.json", "b.yaml", "skipme/c.json")

	s := &scanner.Scanner{
		Exclude:    nil,
		Extensions: []string{".json"},
		SkipDirs:   []string{"skipme"},
	}

	files, err := s.Scan([]string{root})
	if err != nil {
		t.Fatal(err)
	}

	got := relative(t, root, files)
	if want := []string{"a.json"}; !slices.Equal(got, want) {
		t.Errorf("Scan() = %v, want %v", got, want)
	}
}
