package blob

import (
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestPassiveBlobActivationChecksOriginalNativeRelease(t *testing.T) {
	for _, kind := range []string{"filesystem", "objectstore"} {
		t.Run(kind, func(t *testing.T) {
			target, _, _, original := activationFixture(t, kind)
			closed := target.(ImportUnreleasedInspector)
			passive := target.(ImportActivationInspector)
			decision := strings.Repeat("b", 64)
			require.NoError(t, closed.CheckUnreleasedImport(t.Context(), "operation", original))
			require.Error(t, passive.CheckActivatedImportAt(t.Context(), "operation", original, ImportReplayPosition{}, decision))
			// The passive check must not activate the import.
			require.NoError(t, closed.CheckUnreleasedImport(t.Context(), "operation", original))
			step := publicationStep()
			receipt, err := target.(ImportPublicationReplayer).ReplayPublication(t.Context(), "operation", original, step, strings.NewReader("bytes"), snapshotDirectory(t))
			require.NoError(t, err)
			position := ImportReplayPosition{Sequence: 1, ReceiptSHA256: receipt}
			require.NoError(t, closed.CheckUnreleasedImport(t.Context(), "operation", original), "closed check permits original replay progress")
			require.NoError(t, target.(ImportReplayActivator).ActivateImportAt(t.Context(), "operation", original, position, decision))
			require.Error(t, closed.CheckUnreleasedImport(t.Context(), "operation", original))
			require.NoError(t, passive.CheckActivatedImportAt(t.Context(), "operation", original, position, decision))
			require.Error(t, passive.CheckActivatedImportAt(t.Context(), "operation", original, position, strings.Repeat("c", 64)))
			require.Error(t, passive.CheckActivatedImportAt(t.Context(), "operation", original, ImportReplayPosition{}, decision))
			require.NoError(t, passive.CheckActivatedImportAt(t.Context(), "operation", original, position, decision))
		})
	}
}
