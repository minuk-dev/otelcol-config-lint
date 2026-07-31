package cli

import "strings"

// multiFlag collects a flag that may be repeated, e.g. -catalog-location.
// Commas inside a single occurrence are also treated as separators, so both
// forms work.
type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }

func (m *multiFlag) Set(v string) error {
	*m = append(*m, splitList(v)...)
	return nil
}
