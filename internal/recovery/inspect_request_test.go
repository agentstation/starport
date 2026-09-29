package recovery

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInspectImportRequestBounds(t *testing.T) {
	original := InspectImportRequest{VerifyRequest: VerifyRequest{Directory: t.TempDir(), ManifestSHA256: strings.Repeat("a", 64)}, Operation: RestoreOperation{ID: "restore", FencingEvidence: "fenced"}, ExpectedBoundary: Record{DeploymentID: "fixture", Epoch: 2, Evidence: "prepared"}, Destination: filepath.Join(t.TempDir(), "inspection")}
	require.NoError(t, original.Validate())
	for name, change := range map[string]func(*InspectImportRequest){
		"open":              func(r *InspectImportRequest) { r.ExpectedBoundary.Open = true },
		"no-epoch":          func(r *InspectImportRequest) { r.ExpectedBoundary.Epoch = 0 },
		"no-evidence":       func(r *InspectImportRequest) { r.ExpectedBoundary.Evidence = "" },
		"long-evidence":     func(r *InspectImportRequest) { r.ExpectedBoundary.Evidence = strings.Repeat("x", 4097) },
		"control-backend":   func(r *InspectImportRequest) { r.ExpectedBoundary.BackendID = "x\n" },
		"blank-incarnation": func(r *InspectImportRequest) { r.ValkeyIncarnation = " " },
		"long-incarnation":  func(r *InspectImportRequest) { r.ValkeyIncarnation = strings.Repeat("x", 257) },
		"zero-digest":       func(r *InspectImportRequest) { r.SQLPosition.ReceiptSHA256 = strings.Repeat("a", 64) },
		"negative":          func(r *InspectImportRequest) { r.SQLPosition.Sequence = -1 },
		"missing-digest":    func(r *InspectImportRequest) { r.KVPosition.Sequence = 1 },
		"uppercase-digest": func(r *InspectImportRequest) {
			r.KVPosition.Sequence = 1
			r.KVPosition.ReceiptSHA256 = strings.Repeat("A", 64)
		},
	} {
		t.Run(name, func(t *testing.T) { candidate := original; change(&candidate); require.Error(t, candidate.Validate()) })
	}
	original.KVPosition.Sequence = 1
	original.KVPosition.ReceiptSHA256 = strings.Repeat("b", 64)
	require.NoError(t, original.Validate())
}
