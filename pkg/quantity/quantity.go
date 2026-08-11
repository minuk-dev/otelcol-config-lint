// Package quantity parses the memory sizes Kubernetes manifests are written
// in, so a limit can be copied out of a pod spec and given to the linter as it
// stands. Only the suffixes that make sense for memory are accepted; anything
// else is an error rather than a silently different number.
package quantity

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/samber/lo"
	"github.com/samber/mo"
)

// ErrInvalid reports a string that is not a memory quantity.
var ErrInvalid = errors.New("not a memory quantity")

// The binary multipliers Kubernetes writes as Ki, Mi, Gi and so on.
const (
	Ki = 1 << 10
	Mi = Ki << 10
	Gi = Mi << 10
	Ti = Gi << 10
	Pi = Ti << 10
	Ei = Pi << 10
)

// The decimal multipliers Kubernetes writes as k, M, G and so on.
const (
	kilo = 1e3
	mega = 1e6
	giga = 1e9
	tera = 1e12
	peta = 1e15
	exa  = 1e18
)

// overflow is the first byte count that does not fit. A float64 cannot hold
// MaxInt64 exactly -- the nearest value it has is 2^63, one above -- so the
// bound has to be that value rather than MaxInt64, which would round up to it
// and let 8Ei through. Converting an out-of-range float to an int64 is
// implementation-defined in Go: it saturates on arm64 and wraps to the most
// negative int64 on amd64, so a number past the bound has to be an error here
// rather than a different answer per architecture.
const overflow = 1 << 63

// unit is one accepted suffix and what it multiplies by. The suffixes are
// listed longest first so "Mi" is not read as "M" with a stray "i".
type unit struct {
	suffix string
	factor float64
}

// units are the suffixes Parse accepts, in the order they are tried.
func units() []unit {
	return []unit{
		{suffix: "Ki", factor: Ki},
		{suffix: "Mi", factor: Mi},
		{suffix: "Gi", factor: Gi},
		{suffix: "Ti", factor: Ti},
		{suffix: "Pi", factor: Pi},
		{suffix: "Ei", factor: Ei},
		{suffix: "k", factor: kilo},
		{suffix: "M", factor: mega},
		{suffix: "G", factor: giga},
		{suffix: "T", factor: tera},
		{suffix: "P", factor: peta},
		{suffix: "E", factor: exa},
		{suffix: "", factor: 1},
	}
}

// Parse converts a Kubernetes memory quantity such as "512Mi", "1Gi", "2G" or
// a bare byte count into bytes.
func Parse(text string) (int64, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0, fmt.Errorf("%q is %w", text, ErrInvalid)
	}

	digits, suffix := split(trimmed)

	factor, ok := factorOf(suffix).Get()
	if !ok {
		return 0, fmt.Errorf("%q is %w: %q is not a memory suffix (want Ki, Mi, Gi, Ti, Pi, Ei, k, M, G, T, P or E)",
			text, ErrInvalid, suffix)
	}

	value, err := strconv.ParseFloat(digits, 64)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%q is %w: want a non-negative number, optionally with a suffix", text, ErrInvalid)
	}

	bytes := math.Round(value * factor)
	if bytes >= overflow {
		return 0, fmt.Errorf("%q is %w: it does not fit in a byte count", text, ErrInvalid)
	}

	return int64(bytes), nil
}

// split cuts a quantity into its number and its suffix.
func split(text string) (string, string) {
	end := 0
	for end < len(text) && (text[end] >= '0' && text[end] <= '9' || text[end] == '.') {
		end++
	}

	return text[:end], text[end:]
}

// factorOf returns what a suffix multiplies by, and nothing at all when it is
// not one of the accepted suffixes.
func factorOf(suffix string) mo.Option[float64] {
	found, ok := lo.Find(units(), func(candidate unit) bool { return candidate.suffix == suffix })

	return mo.TupleToOption(found.factor, ok)
}

// Format renders a byte count the way a manifest would state it, so a
// diagnostic quotes the number back in the units it was given in.
func Format(bytes int64) string {
	if bytes < Ki {
		return strconv.FormatInt(bytes, 10)
	}

	binary := []unit{
		{suffix: "Ei", factor: Ei},
		{suffix: "Pi", factor: Pi},
		{suffix: "Ti", factor: Ti},
		{suffix: "Gi", factor: Gi},
		{suffix: "Mi", factor: Mi},
		{suffix: "Ki", factor: Ki},
	}

	whole, ok := lo.Find(binary, func(candidate unit) bool {
		size := int64(candidate.factor)

		return bytes >= size && bytes%size == 0
	})
	if ok {
		return strconv.FormatInt(bytes/int64(whole.factor), 10) + whole.suffix
	}

	// Not a whole number of any unit: one decimal place of the largest unit
	// that does not round to zero says more than the exact byte count.
	if bytes >= Mi {
		return strconv.FormatFloat(float64(bytes)/Mi, 'f', 1, 64) + "Mi"
	}

	return strconv.FormatFloat(float64(bytes)/Ki, 'f', 1, 64) + "Ki"
}
