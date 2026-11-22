package catalog

import (
	"context"

	"github.com/agentstation/starmap"
	catalogconfig "github.com/agentstation/starmap/pkg/catalogs/config"
	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starmap/runtime"
)

// BaselineRecoveryDirectoryName identifies the journal directory owned by Starmap.
const BaselineRecoveryDirectoryName = starmap.BaselineRecoveryDirectoryName

// RetainedFile identifies bytes in a verified runtime backup.
type RetainedFile = runtime.RetainedFile

// InspectBaselineExports validates completed exports without activation or acquisition.
// The caller must fence writers and verify the complete inventory around inspection.
func InspectBaselineExports(ctx context.Context, directory string) error {
	return starmap.InspectBaselineExports(ctx, directory)
}

// InspectRetainedBaselinePublications selects verified baseline stages to keep inactive.
// Include the separate recovery records and preserve them as historical evidence.
func InspectRetainedBaselinePublications(ctx context.Context, files map[string]productfiles.RetainedFile, read productfiles.RetainedRecordReader) ([]string, error) {
	return starmap.InspectRetainedBaselinePublications(ctx, files, read)
}

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

// InspectRetainedPublications selects native staging evidence that must remain inactive.
// The caller preserves the selected bytes and validates all remaining runtime records.
func (s Settings) InspectRetainedPublications(ctx context.Context, files map[string]RetainedFile, read func(context.Context, string, int64) ([]byte, error)) ([]string, error) {
	return runtime.InspectRetainedPublications(ctx, files, read)
}
