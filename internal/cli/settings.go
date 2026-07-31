package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"gopkg.in/yaml.v3"
)

// DefaultSettingsFile is looked for in the working directory when no
// -config flag is given.
const DefaultSettingsFile = ".otelcol-config-lint.yaml"

// settings is the file form of the command line options, so a repository can
// commit its linting policy instead of repeating flags in CI.
type settings struct {
	CollectorVersion string `yaml:"collectorVersion"`
	// CatalogLocations are searched in order before the built-in catalogs.
	CatalogLocations     []string          `yaml:"catalogLocations"`
	Output               string            `yaml:"output"`
	Strict               *bool             `yaml:"strict"`
	IgnoreMissingSchemas *bool             `yaml:"ignoreMissingSchemas"`
	Summary              *bool             `yaml:"summary"`
	MinSeverity          string            `yaml:"minSeverity"`
	FailOn               string            `yaml:"failOn"`
	Disable              []string          `yaml:"disable"`
	Severity             map[string]string `yaml:"severity"`
	Exclude              []string          `yaml:"exclude"`
}

// loadSettings reads a settings file. When path is empty the default file is
// used if it exists, and a missing default is not an error.
func loadSettings(path string) (*settings, error) {
	required := path != ""
	if path == "" {
		path = DefaultSettingsFile
	}
	src, err := os.ReadFile(path)
	if err != nil {
		if !required && errors.Is(err, fs.ErrNotExist) {
			return &settings{}, nil
		}
		return nil, err
	}
	var s settings
	dec := yaml.NewDecoder(bytes.NewReader(src))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &s, nil
}
