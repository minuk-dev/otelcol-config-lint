package rule

import (
	"regexp"
	"strings"
)

const maxIdentifierNameBytes = 1024

var (
	identifierType = regexp.MustCompile(`^[a-zA-Z][0-9a-zA-Z_]{0,62}$`)
	identifierName = regexp.MustCompile(`^[^\pZ\pC\pS]+$`)
)

// IdentifierError checks the original YAML value, before ID.String can drop an
// explicitly empty name. Component and pipeline IDs share this syntax in
// Collector v0.110.0 and v0.157.0; pipeline signals are checked separately.
func IdentifierError(raw string) string {
	typ, name, hasName := strings.Cut(raw, "/")
	typ, name = strings.TrimSpace(typ), strings.TrimSpace(name)

	switch {
	case typ == "":
		return "the type before / must not be empty"
	case !identifierType.MatchString(typ):
		return "the type must be 1–63 ASCII letters, digits or underscores, starting with a letter"
	case hasName && name == "":
		return "the name after / must not be empty"
	case len(name) > maxIdentifierNameBytes:
		return "the name must not exceed 1024 bytes"
	case hasName && !identifierName.MatchString(name):
		return "the name must not contain whitespace, control characters or Unicode symbols"
	default:
		return ""
	}
}
