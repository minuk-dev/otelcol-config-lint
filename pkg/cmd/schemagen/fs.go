package schemagen

import (
	"fmt"
	"os"

	"github.com/spf13/afero"
)

func (o *options) fs() afero.Fs {
	if o.fsys == nil {
		return afero.NewOsFs()
	}

	return o.fsys
}

func (o *options) createRoot(dir string) (afero.Fs, func(), error) {
	err := o.fs().MkdirAll(dir, dirPerm)
	if err != nil {
		return nil, nil, fmt.Errorf("create %s: %w", dir, err)
	}

	return o.openRoot(dir)
}

func (o *options) openRoot(dir string) (afero.Fs, func(), error) {
	if _, isOS := o.fs().(*afero.OsFs); !isOS {
		return afero.NewBasePathFs(o.fs(), dir), func() {}, nil
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("open %s: %w", dir, err)
	}

	return rootFS{Root: root}, func() { _ = root.Close() }, nil
}

// rootFS preserves os.Root's symlink confinement behind afero's file interface.
type rootFS struct {
	*os.Root
}

//nolint:ireturn // afero.Fs requires an afero.File result
func (r rootFS) Create(name string) (afero.File, error) {
	f, err := r.Root.Create(name)
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", name, err)
	}

	return f, nil
}

//nolint:ireturn // afero.Fs requires an afero.File result
func (r rootFS) Open(name string) (afero.File, error) {
	f, err := r.Root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", name, err)
	}

	return f, nil
}

//nolint:ireturn // afero.Fs requires an afero.File result
func (r rootFS) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	f, err := r.Root.OpenFile(name, flag, perm)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", name, err)
	}

	return f, nil
}
