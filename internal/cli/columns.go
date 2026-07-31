package cli

import (
	"io"
	"text/tabwriter"
)

// columns renders aligned help output for -list-rules and -list-versions.
type columns struct{ w *tabwriter.Writer }

func newColumns(w io.Writer) columns {
	return columns{w: tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)}
}

func (c columns) row(cells ...string) {
	for i, cell := range cells {
		if i > 0 {
			io.WriteString(c.w, "\t")
		}
		io.WriteString(c.w, cell)
	}
	io.WriteString(c.w, "\n")
}

func (c columns) flush() { c.w.Flush() }
