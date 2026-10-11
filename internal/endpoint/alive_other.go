//go:build !windows

package endpoint

import (
	"errors"
	"os"
	"syscall"
)

// alive reports whether the process exists. Signal 0 checks the process
// without delivering a signal. A process of another user refuses the signal
// and still exists.
func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
