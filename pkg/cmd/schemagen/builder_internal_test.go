package schemagen

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateDistribution(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"core", "otelcol-contrib", "custom_dist", "a", "_custom", "vendor.collector"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.NoError(t, validateDistribution(name))
		})
	}
}

func TestValidateVersion(t *testing.T) {
	t.Parallel()

	tests := map[string]bool{
		"0.157.0": true, "v0.157.0": true, "1.2.3-rc.1": true,
		"1.2.3-0.alpha-1+build.001": true, "1.2.3+build.001": true,
		"": false, "latest": false, "1.2": false, "v1": false, "v01.2.3": false,
		"1.2.03": false, "1.2.3-01": false, "1.2.3-": false, "1.2.3+": false,
		"1.2.3-rc..1": false, "1.2.3+build..1": false, "1.2.3\nother": false,
	}
	for version, valid := range tests {
		t.Run(version, func(t *testing.T) {
			t.Parallel()

			err := validateVersion(version)
			if valid {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, errInvalidVersion)
			}
		})
	}
}
