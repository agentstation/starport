package catalog

import "github.com/agentstation/starmap/pkg/catalogs/permission"

// PermissionClock shares the connected runtime's qualified cached time with its host.
// It reads no storage and calls no native clock API.
func (r *Runtime) PermissionClock() permission.ClockReading {
	if r == nil {
		return permission.ClockReading{}
	}
	return r.runtime.PermissionClock()
}
