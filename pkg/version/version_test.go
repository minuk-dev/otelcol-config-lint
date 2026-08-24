package version //nolint:testpackage // render and vcs are the parts worth pinning

import (
	"runtime/debug"
	"testing"
)

// TestRender pins what each build the toolchain can produce reports, since the
// version is now derived rather than injected and nothing else says what a
// release binary prints.
func TestRender(t *testing.T) {
	t.Parallel()

	const revision = "b7dbdd5cfc07ed516692b8b796e3f1edb63d6539"

	tests := map[string]struct {
		moduleVersion string
		revision      string
		modified      bool
		want          string
	}{
		"a tagged build reports its tag": {
			moduleVersion: "v1.2.3",
			revision:      revision,
			modified:      false,
			want:          "v1.2.3",
		},
		"a tagged build from a dirty tree keeps the toolchain's marker": {
			moduleVersion: "v1.2.3+dirty",
			revision:      revision,
			modified:      true,
			want:          "v1.2.3+dirty",
		},
		"go install pkg@version reports what it resolved, with no repository": {
			moduleVersion: "v1.2.3",
			revision:      "",
			modified:      false,
			want:          "v1.2.3",
		},
		"between tags the pseudo-version gives way to the short commit": {
			moduleVersion: "v0.0.0-20260808131440-b7dbdd5cfc07",
			revision:      revision,
			modified:      false,
			want:          "b7dbdd5",
		},
		"a pseudo-version cut after a tag gives way to the commit too": {
			moduleVersion: "v1.2.4-0.20260808131440-b7dbdd5cfc07",
			revision:      revision,
			modified:      false,
			want:          "b7dbdd5",
		},
		"a tag that happens to carry the commit is still the tag": {
			// Not a pseudo-version: no build timestamp before the commit, so
			// it is a name someone chose and what the binary should report.
			moduleVersion: "v1.2.3-b7dbdd5cfc07",
			revision:      revision,
			modified:      false,
			want:          "v1.2.3-b7dbdd5cfc07",
		},
		"a pseudo-version standing in for another commit is reported as it is": {
			moduleVersion: "v0.0.0-20260808131440-0123456789ab",
			revision:      revision,
			modified:      false,
			want:          "v0.0.0-20260808131440-0123456789ab",
		},
		"an untagged dirty tree is named the way git describe names it": {
			moduleVersion: "v0.0.0-20260808131440-b7dbdd5cfc07+dirty",
			revision:      revision,
			modified:      true,
			want:          "b7dbdd5-dirty",
		},
		"a main module with no version of its own falls back to the commit": {
			moduleVersion: toolchainDevel,
			revision:      revision,
			modified:      false,
			want:          "b7dbdd5",
		},
		"neither a version nor a commit is reported as such, not as a version": {
			moduleVersion: toolchainDevel,
			revision:      "",
			modified:      false,
			want:          Devel,
		},
		"an empty module version is treated the same as (devel)": {
			moduleVersion: "",
			revision:      revision,
			modified:      false,
			want:          "b7dbdd5",
		},
		"a short revision is reported whole": {
			moduleVersion: toolchainDevel,
			revision:      "b7dbdd",
			modified:      false,
			want:          "b7dbdd",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := render(tt.moduleVersion, tt.revision, tt.modified)
			if got != tt.want {
				t.Errorf("render(%q, %q, %t) = %q, want %q",
					tt.moduleVersion, tt.revision, tt.modified, got, tt.want)
			}
		})
	}
}

// TestVCSReadsTheRepositoryState pins the setting keys the toolchain writes:
// a typo in either is silent, and would cost every untagged build its commit.
func TestVCSReadsTheRepositoryState(t *testing.T) {
	t.Parallel()

	revision, modified := vcs([]debug.BuildSetting{
		{Key: "-buildmode", Value: "exe"},
		{Key: "vcs", Value: "git"},
		{Key: "vcs.revision", Value: "b7dbdd5cfc07ed516692b8b796e3f1edb63d6539"},
		{Key: "vcs.modified", Value: "true"},
	})

	if revision != "b7dbdd5cfc07ed516692b8b796e3f1edb63d6539" || !modified {
		t.Errorf("vcs() = %q, %t; want the revision and modified", revision, modified)
	}

	revision, modified = vcs(nil)
	if revision != "" || modified {
		t.Errorf("vcs(nil) = %q, %t; want empty and unmodified", revision, modified)
	}
}

// TestVersionSaysSomething keeps the exported entry point from returning an
// empty string, whatever the binary under test happens to carry.
func TestVersionSaysSomething(t *testing.T) {
	t.Parallel()

	if got := Version(); got == "" {
		t.Error("Version() returned an empty string")
	}
}
