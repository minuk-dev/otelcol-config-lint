package quantity_test

import (
	"errors"
	"testing"

	"github.com/minuk-dev/otelcol-config-lint/pkg/quantity"
)

func TestParse(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		in   string
		want int64
	}{
		"a bare byte count": {in: "1048576", want: quantity.Mi},
		"mebibytes":         {in: "512Mi", want: 512 * quantity.Mi},
		"gibibytes":         {in: "1Gi", want: quantity.Gi},
		"kibibytes":         {in: "256Ki", want: 256 * quantity.Ki},
		"decimal gigabytes": {in: "2G", want: 2_000_000_000},
		"decimal megabytes": {in: "700M", want: 700_000_000},
		"a fraction":        {in: "1.5Gi", want: quantity.Gi + quantity.Gi/2},
		"surrounded by space": {
			in: "  512Mi  ", want: 512 * quantity.Mi,
		},
		"zero": {in: "0", want: 0},
		// The largest whole unit that still fits, which is where the overflow
		// bound has to sit rather than one unit lower.
		"the largest size that fits": {in: "7Ei", want: 7 * quantity.Ei},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := quantity.Parse(tt.in)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tt.in, err)
			}

			if got != tt.want {
				t.Errorf("Parse(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseRejectsWhatIsNotASize(t *testing.T) {
	t.Parallel()

	// "8Ei" is 2^63, the first byte count that does not fit. It is the case a
	// bound of MaxInt64 lets through, because a float64 rounds MaxInt64 up to
	// exactly that number: the conversion that follows would then saturate on
	// one architecture and wrap to a negative size on another.
	for _, in := range []string{
		"", "  ", "512MB", "512 Mi", "lots", "-1Mi", "1m", "Mi", "1e30Ei",
		"8Ei", "9223372036854775807", "16Ei",
	} {
		t.Run(in, func(t *testing.T) {
			t.Parallel()

			_, err := quantity.Parse(in)
			if err == nil {
				t.Fatalf("Parse(%q) should have failed", in)
			}

			if !errors.Is(err, quantity.ErrInvalid) {
				t.Errorf("Parse(%q) = %v, want an ErrInvalid", in, err)
			}
		})
	}
}

func TestFormatSpeaksTheUnitsAManifestUses(t *testing.T) {
	t.Parallel()

	tests := map[int64]string{
		512:                       "512",
		512 * quantity.Ki:         "512Ki",
		512 * quantity.Mi:         "512Mi",
		quantity.Gi:               "1Gi",
		quantity.Gi + quantity.Mi: "1025Mi",
		700_000_000:               "667.6Mi",
	}

	for in, want := range tests {
		if got := quantity.Format(in); got != want {
			t.Errorf("Format(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatRoundTripsWhatParseReads(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"512Mi", "1Gi", "256Ki", "4Gi"} {
		size, err := quantity.Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", in, err)
		}

		if got := quantity.Format(size); got != in {
			t.Errorf("Format(Parse(%q)) = %q", in, got)
		}
	}
}
