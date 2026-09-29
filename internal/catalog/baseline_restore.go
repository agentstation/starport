package catalog

import (
	"context"

	"github.com/agentstation/starmap"
)

// InspectBaselineExports validates completed exports without activation or acquisition.
// The caller must fence writers and verify the complete inventory around inspection.
func InspectBaselineExports(ctx context.Context, directory string) error {
	return starmap.InspectBaselineExports(ctx, directory)
}
