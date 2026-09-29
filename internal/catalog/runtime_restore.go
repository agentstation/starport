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

// InspectRetainedMigration selects completed path-bound records to retain as inactive history.
// Paths identify the captured host only. The caller must verify the remaining runtime tree.
func (s Settings) InspectRetainedMigration(ctx context.Context, originalDirectory string, read func(context.Context, string, int64) ([]byte, error)) ([]string, error) {
	parsed, err := catalogconfig.Parse(s.catalogValues())
	if err != nil {
		return nil, err
	}
	return runtime.InspectRetainedMigration(ctx, originalDirectory, s.directoryOwner(), parsed.SchedulerIdentity, read)
}
