package ruleset_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/diag"
	"github.com/minuk-dev/otelcol-config-lint/pkg/ruleset"
)

// aRule is a rule that exists, borrowed from the set itself so the tests below
// cannot go stale by naming a rule that is later renamed or removed.
func aRule(t *testing.T) string {
	t.Helper()

	all := ruleset.All()
	require.NotEmpty(t, all, "the set should carry rules")

	return all[0].Name()
}

//nolint:exhaustruct // a selection states only what the caller asked for
func TestResolveHoldsEveryNameToTheSet(t *testing.T) {
	t.Parallel()

	name := aRule(t)

	tests := map[string]struct {
		in   ruleset.Selection
		want error
	}{
		"an unknown starting set": {
			in:   ruleset.Selection{Default: "some"},
			want: ruleset.ErrUnknownDefault,
		},
		"enabling a rule that does not exist": {
			in:   ruleset.Selection{Enable: []string{"no-such-rule"}},
			want: ruleset.ErrUnknownRule,
		},
		"disabling a rule that does not exist": {
			in:   ruleset.Selection{Disable: []string{"no-such-rule"}},
			want: ruleset.ErrUnknownRule,
		},
		"a rule named on both sides": {
			in:   ruleset.Selection{Enable: []string{name}, Disable: []string{name}},
			want: ruleset.ErrEnabledAndDisabled,
		},
		"a severity that is not a pair": {
			in:   ruleset.Selection{Severity: []string{name}},
			want: ruleset.ErrBadSeverityPair,
		},
		"a severity for a rule that does not exist": {
			in:   ruleset.Selection{Severity: []string{"no-such-rule=warning"}},
			want: ruleset.ErrUnknownRule,
		},
	}

	for label, tt := range tests {
		t.Run(label, func(t *testing.T) {
			t.Parallel()

			_, err := ruleset.Resolve(tt.in)
			require.ErrorIs(t, err, tt.want)
		})
	}
}

// TestNoneRunsOnlyWhatIsEnabled pins the order the fields resolve in, which is
// what makes writing --default none -E <rule> mean one rule and not none.
//
//nolint:exhaustruct // a selection states only what the caller asked for
func TestNoneRunsOnlyWhatIsEnabled(t *testing.T) {
	t.Parallel()

	name := aRule(t)

	resolved, err := ruleset.Resolve(ruleset.Selection{
		Default: ruleset.DefaultNone,
		Enable:  []string{name},
	})
	require.NoError(t, err)

	assert.Len(t, resolved.Rules, len(ruleset.All()), "every rule is still carried, at the level it resolved to")
	assert.NotEqual(t, diag.Off, resolved.Severities[name], "the enabled rule should run")

	off := 0

	for _, r := range ruleset.All() {
		if resolved.Severities[r.Name()] == diag.Off {
			off++
		}
	}

	assert.Equal(t, len(ruleset.All())-1, off, "everything else should be off")
}

// TestAnExplicitLevelIsTheLastWord keeps a severity from being undone by the
// enable or disable it was written next to.
//
//nolint:exhaustruct // a selection states only what the caller asked for
func TestAnExplicitLevelIsTheLastWord(t *testing.T) {
	t.Parallel()

	name := aRule(t)

	resolved, err := ruleset.Resolve(ruleset.Selection{
		Disable:  []string{name},
		Severity: []string{name + "=warning"},
	})
	require.NoError(t, err)

	assert.Equal(t, diag.Warning, resolved.Severities[name])
}
