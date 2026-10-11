//go:build !windows

package endpoint

import (
	"errors"
	"syscall"
)

func addressInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE)
}
