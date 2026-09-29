package catalog

import (
	"context"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/productfiles"
)

// InspectBaselineExports validates completed exports without activation or acquisition.
// The caller must fence writers and verify the complete inventory around inspection.
func InspectBaselineExports(ctx context.Context, directory string) error {
	return starmap.InspectBaselineExports(ctx, directory)
}

// BaselineRecoveryDirectoryName identifies the journal directory owned by Starmap.
const BaselineRecoveryDirectoryName = starmap.BaselineRecoveryDirectoryName

// InspectRetainedBaselinePublications selects verified baseline stages to keep inactive.
// Include the separate recovery records and preserve them as historical evidence.
func InspectRetainedBaselinePublications(ctx context.Context, files map[string]productfiles.RetainedFile, read productfiles.RetainedRecordReader) ([]string, error) {
	return starmap.InspectRetainedBaselinePublications(ctx, files, read)
}
