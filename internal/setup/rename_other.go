//go:build !darwin && !linux && !windows

package setup

import (
	"errors"
)

var errExclusiveRenameUnsupported = errors.New("exclusive directory rename is unsupported on this platform")

func renameNoReplace(source, destination string) error {
	return errExclusiveRenameUnsupported
}
