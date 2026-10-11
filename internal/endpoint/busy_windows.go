package endpoint

import (
	"errors"
	"syscall"

	"golang.org/x/sys/windows"
)

func addressInUse(err error) bool {
	return errors.Is(err, windows.WSAEADDRINUSE) || errors.Is(err, syscall.EADDRINUSE)
}
