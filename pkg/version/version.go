// Package version reports the linter's own version.
//
// It is a leaf package on purpose: the build stamps it with -ldflags, and
// anything that wants the version -- the command, an HTTP User-Agent -- can
// depend on it without depending on the command package.
package version

// Version is the linter's own version, injected at build time.
//
// The linker ignores -X against a symbol that does not exist, so a build that
// gets the path wrong silently ships "dev"; `make verify-version` builds and
// reads it back to catch that.
//
//nolint:gochecknoglobals // injected at build time with -ldflags
var Version = "dev"
