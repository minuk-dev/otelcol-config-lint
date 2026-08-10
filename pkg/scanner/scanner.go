// Package scanner expands the paths named on a command line into the set of
// config files to lint.
package scanner

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/afero"

	"github.com/minuk-dev/otelcol-config-lint/pkg/sets"
)

// StdinMarker is the path that asks for the config on standard input. A Scanner
// passes it through untouched, for the caller to recognise.
const StdinMarker = "-"

// Scanner turns paths into the files to lint.
type Scanner struct {
	// Fs is the filesystem the paths are read from. A nil Fs means the real
	// one, so the zero value scans the machine the linter runs on.
	Fs afero.Fs
	// Exclude are glob patterns skipped during a directory walk, matched
	// against both the full path and the base name. A pattern containing "*"
	// also matches anywhere in the path.
	Exclude []string
	// Extensions are the file extensions a directory walk picks up, compared
	// in lower case and including the dot.
	Extensions []string
	// SkipDirs are directory names a walk does not descend into. Directories
	// whose name starts with a dot are always skipped.
	SkipDirs []string
}

// New builds a Scanner that picks up YAML files, does not descend into the
// usual vendored directories, and skips anything matching exclude.
func New(exclude []string) *Scanner {
	return &Scanner{
		Fs:         nil,
		Exclude:    exclude,
		Extensions: []string{".yaml", ".yml"},
		SkipDirs:   []string{"vendor", "node_modules"},
	}
}

// Scan expands paths into the files to lint. Directories are walked
// recursively and StdinMarker is passed through. The same file can be named
// twice, or named and also walked into, so the result is a set: it holds each
// file once and carries no order.
func (s *Scanner) Scan(paths []string) (sets.Set[string], error) {
	files := sets.New[string]()

	for _, path := range paths {
		if path == StdinMarker {
			files.Insert(StdinMarker)

			continue
		}

		info, err := s.fs().Stat(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}

		if !info.IsDir() {
			// An explicitly named file is linted even if it would be excluded
			// by a directory walk, matching how other linters behave.
			files.Insert(filepath.Clean(path))

			continue
		}

		err = s.walk(path, files)
		if err != nil {
			return nil, err
		}
	}

	return files, nil
}

// fs returns the filesystem to read, which is the real one unless the caller
// named another.
func (s *Scanner) fs() afero.Fs {
	if s.Fs == nil {
		return afero.NewOsFs()
	}

	return s.Fs
}

// walk adds every config file under root to files.
func (s *Scanner) walk(root string, files sets.Set[string]) error {
	err := afero.Walk(s.fs(), root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			if path != root && s.skipDir(info.Name()) {
				return fs.SkipDir
			}

			return nil
		}

		if !s.wanted(info.Name()) || s.excluded(path) {
			return nil
		}

		files.Insert(filepath.Clean(path))

		return nil
	})
	if err != nil {
		return fmt.Errorf("walk %s: %w", root, err)
	}

	return nil
}

// skipDir reports whether a walk should stay out of a directory.
func (s *Scanner) skipDir(name string) bool {
	return strings.HasPrefix(name, ".") || slices.Contains(s.SkipDirs, name)
}

// wanted reports whether a file name has an extension the walk picks up.
func (s *Scanner) wanted(name string) bool {
	return slices.Contains(s.Extensions, strings.ToLower(filepath.Ext(name)))
}

// excluded reports whether a path matches any exclude pattern. Patterns are
// matched against both the full path and the base name.
func (s *Scanner) excluded(path string) bool {
	base := filepath.Base(path)
	for _, pattern := range s.Exclude {
		if ok, _ := filepath.Match(pattern, base); ok {
			return true
		}

		if ok, _ := filepath.Match(pattern, path); ok {
			return true
		}

		// A pattern with a star also matches anywhere in the path, so
		// "*generated*" reaches files a plain glob would not.
		if strings.Contains(pattern, "*") && strings.Contains(path, strings.Trim(pattern, "*")) {
			return true
		}
	}

	return false
}
