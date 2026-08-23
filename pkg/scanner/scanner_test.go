package scanner_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/spf13/afero"

	"github.com/minuk-dev/otelcol-config-lint/pkg/scanner"
	"github.com/minuk-dev/otelcol-config-lint/pkg/sets"
)

// root is where every test tree is written. It is in-memory, so the name is
// arbitrary and nothing on the machine running the tests is touched.
const root = "/repo"

// tree writes each named file to a fresh in-memory filesystem under root and
// returns it.
func tree(t *testing.T, names ...string) afero.Fs {
	t.Helper()

	fsys := afero.NewMemMapFs()

	err := fsys.MkdirAll(root, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range names {
		path := filepath.Join(root, filepath.FromSlash(name))

		err := fsys.MkdirAll(filepath.Dir(path), 0o750)
		if err != nil {
			t.Fatal(err)
		}

		err = afero.WriteFile(fsys, path, []byte("receivers:\n"), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	return fsys
}

// newScanner builds the default scanner pointed at an in-memory tree.
func newScanner(fsys afero.Fs, exclude []string) *scanner.Scanner {
	s := scanner.New(nil, exclude)
	s.Fs = fsys

	return s
}

// relative turns the scan result back into slash-separated paths under root, so
// the assertions read like the tree that was written.
func relative(t *testing.T, files sets.Set[string]) []string {
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

	fsys := tree(t, "a.yaml", "b.yml", "nested/c.yaml", "notes.txt", "no-extension")

	files, err := newScanner(fsys, nil).Scan([]string{root})
	if err != nil {
		t.Fatal(err)
	}

	got := relative(t, files)
	if want := []string{"a.yaml", "b.yml", "nested/c.yaml"}; !slices.Equal(got, want) {
		t.Errorf("Scan() = %v, want %v", got, want)
	}
}

func TestScanIsCaseInsensitiveAboutExtensions(t *testing.T) {
	t.Parallel()

	fsys := tree(t, "loud.YAML", "mixed.Yml")

	files, err := newScanner(fsys, nil).Scan([]string{root})
	if err != nil {
		t.Fatal(err)
	}

	if files.Len() != 2 {
		t.Errorf("Scan() found %v, want both files", relative(t, files))
	}
}

func TestScanSkipsDotAndVendorDirectories(t *testing.T) {
	t.Parallel()

	fsys := tree(t,
		"keep.yaml",
		".git/config.yaml",
		"vendor/dep.yaml",
		"node_modules/pkg.yaml",
		"nested/.hidden/deep.yaml",
	)

	files, err := newScanner(fsys, nil).Scan([]string{root})
	if err != nil {
		t.Fatal(err)
	}

	got := relative(t, files)
	if want := []string{"keep.yaml"}; !slices.Equal(got, want) {
		t.Errorf("Scan() = %v, want %v", got, want)
	}
}

// TestScanEntersADotDirectoryNamedDirectly pins that the skip applies to what a
// walk descends into, not to the root the caller asked for.
func TestScanEntersADotDirectoryNamedDirectly(t *testing.T) {
	t.Parallel()

	fsys := tree(t, ".config/app.yaml")

	files, err := newScanner(fsys, nil).Scan([]string{filepath.Join(root, ".config")})
	if err != nil {
		t.Fatal(err)
	}

	got := relative(t, files)
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

			fsys := tree(t, "agent.yaml", "keep.yaml", "gen/agent.generated.yaml")

			files, err := newScanner(fsys, tt.exclude).Scan([]string{root})
			if err != nil {
				t.Fatal(err)
			}

			if got := relative(t, files); !slices.Equal(got, tt.want) {
				t.Errorf("Scan() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestScanKeepsAnExplicitlyNamedFile pins that naming a file wins over the
// exclude patterns, which only govern directory walks.
func TestScanKeepsAnExplicitlyNamedFile(t *testing.T) {
	t.Parallel()

	fsys := tree(t, "agent.yaml")
	path := filepath.Join(root, "agent.yaml")

	files, err := newScanner(fsys, []string{"agent.yaml"}).Scan([]string{path})
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

	fsys := tree(t, "config.txt")
	path := filepath.Join(root, "config.txt")

	files, err := newScanner(fsys, nil).Scan([]string{path})
	if err != nil {
		t.Fatal(err)
	}

	if !files.Has(path) {
		t.Errorf("an explicitly named file should be kept, got %v", sets.List(files))
	}
}

func TestScanDeduplicates(t *testing.T) {
	t.Parallel()

	fsys := tree(t, "agent.yaml")
	path := filepath.Join(root, "agent.yaml")

	// Named through the walk, named directly, and named directly again with a
	// path that needs cleaning.
	unclean := filepath.Join(root, ".", "agent.yaml")

	files, err := newScanner(fsys, nil).Scan([]string{root, path, unclean})
	if err != nil {
		t.Fatal(err)
	}

	if files.Len() != 1 {
		t.Errorf("Scan() = %v, want one file", sets.List(files))
	}
}

func TestScanPassesStdinThrough(t *testing.T) {
	t.Parallel()

	fsys := tree(t, "agent.yaml")

	files, err := newScanner(fsys, nil).Scan([]string{scanner.StdinMarker, root})
	if err != nil {
		t.Fatal(err)
	}

	if !files.Has(scanner.StdinMarker) {
		t.Errorf("the stdin marker should survive, got %v", sets.List(files))
	}

	if files.Len() != 2 {
		t.Errorf("Scan() = %v, want the marker and the file", relative(t, files))
	}
}

func TestScanReportsAMissingPath(t *testing.T) {
	t.Parallel()

	_, err := newScanner(tree(t), nil).Scan([]string{filepath.Join(root, "nope.yaml")})
	if err == nil {
		t.Fatal("want an error for a path that does not exist")
	}

	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the cause should survive wrapping, got %v", err)
	}
}

// TestScanReportsADirectoryItCannotRead pins that a walk failure reaches the
// caller instead of being swallowed into a short file list. A real filesystem
// can only produce this with a chmod, which root would not honour anyway.
func TestScanReportsADirectoryItCannotRead(t *testing.T) {
	t.Parallel()

	locked := filepath.Join(root, "locked")

	fsys := blocked{Fs: tree(t, "keep.yaml", "locked/deep.yaml"), path: locked}

	_, err := newScanner(fsys, nil).Scan([]string{root})
	if err == nil {
		t.Fatal("want an error for a directory the walk may not read")
	}

	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("the cause should survive wrapping, got %v", err)
	}
}

// blocked refuses to open one path, standing in for a directory the process
// does not have permission to read.
type blocked struct {
	afero.Fs

	path string
}

func (b blocked) Open(name string) (afero.File, error) {
	if name == b.path {
		return nil, &os.PathError{Op: "open", Path: name, Err: os.ErrPermission}
	}

	return b.Fs.Open(name)
}

// TestScanSurvivesAnEntryItCannotStat pins that one bad entry does not cost the
// walk. A file can be removed between the directory read and the stat that
// follows it, and a directory can be listable without being searchable; ending
// the scan there would report nothing for a tree that is otherwise fine. The
// name is still known, so the file is kept and the linter reports it as one it
// could not read.
func TestScanSurvivesAnEntryItCannotStat(t *testing.T) {
	t.Parallel()

	fsys := vanished{Fs: tree(t, "keep.yaml", "gone.yaml"), path: filepath.Join(root, "gone.yaml")}

	files, err := newScanner(fsys, nil).Scan([]string{root})
	if err != nil {
		t.Fatal(err)
	}

	got := relative(t, files)
	if want := []string{"gone.yaml", "keep.yaml"}; !slices.Equal(got, want) {
		t.Errorf("Scan() = %v, want %v", got, want)
	}
}

// vanished lists one path but refuses to stat it, standing in for a file
// removed while the walk was running.
type vanished struct {
	afero.Fs

	path string
}

func (v vanished) Stat(name string) (os.FileInfo, error) {
	if name == v.path {
		return nil, &os.PathError{Op: "stat", Path: name, Err: os.ErrNotExist}
	}

	return v.Fs.Stat(name)
}

func TestScanOfNothingIsEmpty(t *testing.T) {
	t.Parallel()

	files, err := newScanner(tree(t), nil).Scan(nil)
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

	s := &scanner.Scanner{
		Fs:         tree(t, "a.json", "b.yaml", "skipme/c.json"),
		Exclude:    nil,
		Extensions: []string{".json"},
		SkipDirs:   []string{"skipme"},
	}

	files, err := s.Scan([]string{root})
	if err != nil {
		t.Fatal(err)
	}

	got := relative(t, files)
	if want := []string{"a.json"}; !slices.Equal(got, want) {
		t.Errorf("Scan() = %v, want %v", got, want)
	}
}

// TestNilFsReadsTheRealFilesystem pins the documented default: a Scanner that
// was never given an Fs still walks the machine it runs on.
//
// It repeats what the in-memory tests cover because the two filesystems are
// not interchangeable underneath. OsFs implements afero.Lstater and MemMapFs
// does not, so the walk takes a different path through afero on each, and this
// is the one the shipped binary runs.
func TestNilFsReadsTheRealFilesystem(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	for _, name := range []string{
		"agent.yaml",
		"nested/deep.yml",
		"notes.txt",
		".git/config.yaml",
		"vendor/dep.yaml",
		"gen/agent.generated.yaml",
	} {
		path := filepath.Join(dir, filepath.FromSlash(name))

		err := os.MkdirAll(filepath.Dir(path), 0o750)
		if err != nil {
			t.Fatal(err)
		}

		err = os.WriteFile(path, []byte("receivers:\n"), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	files, err := scanner.New(nil, []string{"*.generated.yaml"}).Scan([]string{dir})
	if err != nil {
		t.Fatal(err)
	}

	got := make([]string, 0, files.Len())

	for _, path := range sets.List(files) {
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			t.Fatal(err)
		}

		got = append(got, filepath.ToSlash(rel))
	}

	if want := []string{"agent.yaml", "nested/deep.yml"}; !slices.Equal(got, want) {
		t.Errorf("Scan() = %v, want %v", got, want)
	}
}
