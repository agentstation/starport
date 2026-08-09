//go:build !darwin && !linux && !windows

package setup

import (
	"io/fs"
	"os"
)

func renameNoReplace(source, destination string) error {
	if _, err := os.Lstat(destination); err == nil {
		return fs.ErrExist
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.Rename(source, destination)
}
