package catalog

import (
	"context"
	"fmt"
	catalogconfig "github.com/agentstation/starmap/pkg/catalogs/config"
	"github.com/agentstation/starmap/runtime"
)

// ValidateStorageSelection refuses a conflicting catalog owner before gateway stores open.
// The runtime repeats ownership validation under its directory lock during startup.
func (s Settings) ValidateStorageSelection(ctx context.Context) error {
	parsed, err := catalogconfig.Parse(s.catalogValues())
	if err != nil {
		return err
	}
	owner := s.directoryOwner()
	if err := owner.Validate(); err != nil {
		return err
	}
	if parsed.StateDirectory == "" {
		return nil
	}
	status, err := runtime.InspectDirectoryOwnerRecord(ctx, parsed.StateDirectory, owner, parsed.SchedulerIdentity)
	if err != nil {
		return err
	}
	switch status {
	case runtime.OwnerRecordAbsent, runtime.OwnerRecordMatches:
		return nil
	default:
		return fmt.Errorf("catalog runtime ownership is %s. Verify the instance and complete ownership migration before startup", status)
	}
}
