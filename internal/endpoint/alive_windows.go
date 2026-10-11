package endpoint

import (
	"errors"
	"math"

	"golang.org/x/sys/windows"
)

// stillActive is the exit code that Windows reports for a running process.
const stillActive = 259

// alive reports whether the process runs. A process that another account
// owns refuses the query and still exists.
func alive(pid int) bool {
	if pid <= 0 || pid > math.MaxUint32 {
		return false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	var code uint32
	if err := windows.GetExitCodeProcess(handle, &code); err != nil {
		return false
	}
	return code == stillActive
}
