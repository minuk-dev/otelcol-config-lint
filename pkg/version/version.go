// Package version reports the linter's own version.
//
// Nothing injects it. The Go toolchain records the main module's version and
// the state of the repository it was built from, and this reads that back: a
// release binary reports its tag because it was built from that tag, not
// because a build path remembered to say so. It also covers the install the
// README recommends -- go install ...@latest, which no build path of ours can
// reach -- so that reports the version it resolved rather than a placeholder.
package version

import (
	"runtime/debug"
	"strings"
)

const (
	// toolchainDevel is what the toolchain reports for a main module with no
	// version of its own.
	toolchainDevel = "(devel)"

	// Devel is reported when there is no version and no commit to fall back
	// on: `go run`, which does not stamp the repository state, a build with
	// -buildvcs=false, or a source tree that is not a repository at all. It is
	// deliberately not a version, so a release that reports it is a bug and
	// the build workflow fails on it.
	Devel = "devel"

	// shortRevisionLen abbreviates a commit the way `git describe --always`
	// does, since that is what would have named an untagged build.
	shortRevisionLen = 7

	// pseudoRevisionLen is how much of a commit a module pseudo-version
	// carries, and so how much of one to look for in a version to recognise
	// that the toolchain derived it rather than a person choosing it.
	pseudoRevisionLen = 12

	// dirtySuffix marks a build from a tree with uncommitted changes. The
	// toolchain writes "+dirty" onto a version derived from a tag; this is the
	// same thing for one derived from a bare commit, in the shape
	// `git describe --dirty` uses.
	dirtySuffix = "-dirty"
)

// Version reports the linter's own version: the tag a release was built from,
// the short commit for a build between tags, or [Devel] when the binary
// carries neither.
func Version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return Devel
	}

	revision, modified := vcs(info.Settings)

	return render(info.Main.Version, revision, modified)
}

// vcs pulls the repository state out of the settings the toolchain stamped.
func vcs(settings []debug.BuildSetting) (string, bool) {
	var (
		revision string
		modified bool
	)

	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}

	return revision, modified
}

// render turns what the toolchain recorded into what the user sees.
func render(moduleVersion, revision string, modified bool) string {
	// Between tags the module version is a pseudo-version, which ends in the
	// commit it was derived from and so says nothing the short commit does
	// not. Anything else is a tag, or the version `go install` resolved.
	if moduleVersion != "" && moduleVersion != toolchainDevel && !derivedFrom(moduleVersion, revision) {
		return moduleVersion
	}

	if revision == "" {
		return Devel
	}

	short := revision
	if len(short) > shortRevisionLen {
		short = short[:shortRevisionLen]
	}

	if modified {
		short += dirtySuffix
	}

	return short
}

// derivedFrom reports whether a module version is a pseudo-version standing in
// for the given commit rather than a tag someone chose.
func derivedFrom(moduleVersion, revision string) bool {
	if len(revision) < pseudoRevisionLen {
		return false
	}

	return strings.Contains(moduleVersion, revision[:pseudoRevisionLen])
}
