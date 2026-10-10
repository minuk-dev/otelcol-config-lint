package settings_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/otelcol-config-lint/pkg/settings"
)

func TestParseTakesOneDocument(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name, src, wantError string
	}{
		{name: "empty", src: "", wantError: ""},
		{name: "single", src: "version: \"1\"\n", wantError: ""},
		{name: "comments", src: "version: \"1\"\n# trailing comment\n", wantError: ""},
		{name: "document end", src: "version: \"1\"\n...\n", wantError: ""},
		{name: "valid second", src: "version: \"1\"\n---\nversion: \"1\"\n", wantError: "more than one document"},
		{name: "empty second", src: "version: \"1\"\n---\n", wantError: "more than one document"},
		{name: "malformed second", src: "version: \"1\"\n---\nthis is: [broken\n", wantError: "yaml: line"},
		{name: "empty first", src: "---\n---\nversion: \"1\"\n", wantError: "more than one document"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s, err := settings.Parse([]byte(tt.src))
			if tt.wantError == "" {
				require.NoError(t, err)
				require.NotNil(t, s)

				if tt.src != "" {
					assert.Equal(t, settings.Version, s.Version)
				}

				return
			}

			require.ErrorContains(t, err, tt.wantError)
			assert.Nil(t, s)
		})
	}
}
