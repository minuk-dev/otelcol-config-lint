package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// configExts are the file extensions collected when walking a directory.
var configExts = map[string]bool{".yaml": true, ".yml": true}

// collect expands the command line arguments into a list of files to lint.
// Directories are walked recursively; "-" is kept as a marker for stdin.
func collect(args []string, exclude []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}

	for _, arg := range args {
		if arg == "-" {
			add("-")
			continue
		}
		info, err := os.Stat(arg)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			// An explicitly named file is linted even if it would be excluded
			// by a directory walk, matching how other linters behave.
			add(filepath.Clean(arg))
			continue
		}
		err = filepath.WalkDir(arg, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			name := d.Name()
			if d.IsDir() {
				if path != arg && (strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules") {
					return fs.SkipDir
				}
				return nil
			}
			if !configExts[strings.ToLower(filepath.Ext(name))] {
				return nil
			}
			if excluded(path, exclude) {
				return nil
			}
			add(filepath.Clean(path))
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// excluded reports whether a path matches any exclude pattern. Patterns are
// matched against both the full path and the base name.
func excluded(path string, patterns []string) bool {
	base := filepath.Base(path)
	for _, p := range patterns {
		if ok, _ := filepath.Match(p, base); ok {
			return true
		}
		if ok, _ := filepath.Match(p, path); ok {
			return true
		}
		if strings.Contains(path, strings.Trim(p, "*")) && strings.Contains(p, "*") {
			return true
		}
	}
	return false
}
