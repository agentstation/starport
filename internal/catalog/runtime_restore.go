package catalog

import (
	"context"

	catalogconfig "github.com/agentstation/starmap/pkg/catalogs/config"
	"github.com/agentstation/starmap/runtime"
)

// InspectRetainedDirectory validates runtime identity and retained inputs without activation.
// The caller must fence writers and verify the complete inventory around inspection.
// This check does not approve replica reuse, current permission, or admission.
func (s Settings) InspectRetainedDirectory(ctx context.Context, directory string) error {
	parsed, err := catalogconfig.Parse(s.catalogValues())
	if err != nil {
		return err
	}
	return runtime.InspectRetainedDirectory(ctx, directory, s.directoryOwner(), parsed.SchedulerIdentity)
}
