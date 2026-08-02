// Package termutil holds stateless helpers for the terminal the command
// writes to.
package termutil

import (
	"io"
	"os"
)

// IsTerminal reports whether w is a character device, so colour is only used
// when a human is watching.
func IsTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}

	info, err := f.Stat()

	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
