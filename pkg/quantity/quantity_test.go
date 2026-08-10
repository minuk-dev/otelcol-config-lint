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

	for _, in := range []string{"", "  ", "512MB", "512 Mi", "lots", "-1Mi", "1m", "Mi", "1e30Ei"} {
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
