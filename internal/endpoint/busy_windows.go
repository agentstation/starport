package endpoint

import (
	"errors"
	"syscall"

	"golang.org/x/sys/windows"
)

// addressInUse reports a port that another socket holds. Windows refuses a
// port in an excluded port range, or one that another socket holds for
// exclusive use, with WSAEACCES and not WSAEADDRINUSE.
func addressInUse(err error) bool {
	return errors.Is(err, windows.WSAEADDRINUSE) || errors.Is(err, windows.WSAEACCES) || errors.Is(err, syscall.EADDRINUSE)
}
