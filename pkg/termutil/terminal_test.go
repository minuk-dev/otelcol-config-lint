package termutil_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/minuk-dev/otelcol-config-lint/pkg/termutil"
)

func TestIsTerminalNonFile(t *testing.T) {
	t.Parallel()

	if termutil.IsTerminal(&bytes.Buffer{}) {
		t.Error("a buffer is not a terminal")
	}
}

func TestIsTerminalRegularFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "out.txt")

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = f.Close()
	})

	if termutil.IsTerminal(f) {
		t.Error("a regular file is not a terminal")
	}
}

func TestIsTerminalCharDevice(t *testing.T) {
	t.Parallel()

	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = f.Close()
	})

	if !termutil.IsTerminal(f) {
		t.Errorf("%s is a character device", os.DevNull)
	}
}
