package schema

import (
	"strconv"
	"strings"
)

// Decoder records the resolved config decoder independently of the distribution version.
type Decoder struct {
	ConfmapVersion string `json:"confmapVersion,omitempty" yaml:"confmapVersion,omitempty"`
	// Modified marks local or fork replacements of confmap or mapstructure.
	Modified bool `json:"modified,omitempty" yaml:"modified,omitempty"`
}

// ScalarDecoding returns weak scalar conversion, null-to-zero behavior, and whether
// those defaults are known. Feature-gate overrides are not inferred from versions.
func (s *Schema) ScalarDecoding() (bool, bool, bool) {
	if s == nil {
		return false, false, false
	}

	version, nullBoundary := s.CollectorVersion, "v0.127.0"
	if s.Decoder != nil {
		if s.Decoder.Modified {
			return false, false, false
		}

		version, nullBoundary = s.Decoder.ConfmapVersion, "v1.33.0"
	} else {
		switch s.Distribution {
		case "", "core", "contrib", "k8s", "otlp":
		default:
			return false, false, false
		}
	}

	version, known := releaseVersion(version)
	if !known {
		return false, false, false
	}

	return Compare(version, "v0.105.0") < 0, Compare(version, nullBoundary) >= 0, true
}

// releaseVersion accepts release tags and drops build metadata, never guessing from a prerelease or custom label.
func releaseVersion(version string) (string, bool) {
	version, _, _ = strings.Cut(Normalize(version), "+")
	parts := strings.Split(strings.TrimPrefix(version, "v"), ".")

	const versionParts = 3

	if len(parts) != versionParts {
		return "", false
	}

	for _, part := range parts {
		n, err := strconv.ParseUint(part, 10, 32)
		if err != nil || strconv.FormatUint(n, 10) != part {
			return "", false
		}
	}

	return version, true
}
