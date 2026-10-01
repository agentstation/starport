package catalog

import (
	"context"

	"github.com/agentstation/starmap/runtime"
)

// PrepareRecoveryTopologyDirectory establishes stopped native owner state before the host seals activation.
// It reads no external catalog and issues no permission. Sealed restart paths must use passive inspection.
func (s Settings) PrepareRecoveryTopologyDirectory(ctx context.Context) error {
	target, _, err := s.RecoveryTopologyTarget()
	if err != nil {
		return err
	}
	return runtime.PrepareCatalogRecoveryDirectory(ctx, target.Directory, target.Owner, target.SchedulerIdentity)
}
