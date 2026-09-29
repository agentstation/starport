package recovery

import (
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
)

// InspectImportRequest selects a verified source and explicitly expected closed target state.
// Replay positions come from retained replay receipts. Zero explicitly selects prepared state.
// The expected boundary may name a later independently accepted recovery epoch.
type InspectImportRequest struct {
	VerifyRequest
	Operation         RestoreOperation
	ExpectedBoundary  Record
	KVPosition        storage.ImportReplayPosition
	SQLPosition       sqlstore.RelationalReplayPosition
	ValkeyIncarnation string
	Destination       string
}

// ImportInspectionResult retains the complete request binding and checked graph receipts.
// It records an inspection only. It does not prove complete later history or permit activation.
type ImportInspectionResult struct {
	Directory         string                   `json:"directory"`
	ManifestSHA256    string                   `json:"manifest_sha256"`
	Operation         RestoreOperation         `json:"operation"`
	ValkeyIncarnation string                   `json:"valkey_incarnation,omitempty"`
	Request           ImportedReferenceRequest `json:"request"`
	Inspection        ImportedReferences       `json:"inspection"`
}

// Validate checks complete operator input before configuration or target access.
func (r InspectImportRequest) Validate() error {
	if err := r.VerifyRequest.Validate(); err != nil {
		return err
	}
	if err := r.Operation.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(r.Operation.FencingEvidence) == "" {
		return errors.New("import inspection requires the unchanged external fencing evidence reference")
	}
	if validDeployment(r.ExpectedBoundary.DeploymentID) != nil || r.ExpectedBoundary.Open || r.ExpectedBoundary.Epoch <= 0 {
		return errors.New("import inspection requires an explicit closed recovery boundary")
	}
	for _, value := range []struct {
		text string
		max  int
	}{{r.ExpectedBoundary.Evidence, 4096}, {r.ExpectedBoundary.BackendID, 256}, {r.ValkeyIncarnation, 256}} {
		if len(value.text) > value.max || strings.ContainsFunc(value.text, unicode.IsControl) {
			return errors.New("import inspection identities exceed their bounds or contain control characters")
		}
	}
	if r.ValkeyIncarnation != "" && strings.TrimSpace(r.ValkeyIncarnation) == "" {
		return errors.New("import inspection requires a nonblank Valkey incarnation")
	}
	if strings.TrimSpace(r.ExpectedBoundary.Evidence) == "" {
		return errors.New("import inspection requires the expected recovery evidence reference")
	}
	if !validInspectionPosition(r.KVPosition.Sequence, r.KVPosition.ReceiptSHA256) || !validInspectionPosition(r.SQLPosition.Sequence, r.SQLPosition.ReceiptSHA256) {
		return errors.New("import inspection requires exact replay sequences and matching receipt digests")
	}
	if !filepath.IsAbs(r.Destination) || filepath.Clean(r.Destination) != r.Destination {
		return errors.New("import inspection requires a new clean absolute output directory")
	}
	return nil
}

func validInspectionPosition(sequence int64, digest string) bool {
	if sequence == 0 {
		return digest == ""
	}
	decoded, err := hex.DecodeString(digest)
	return sequence > 0 && err == nil && len(decoded) == 32 && digest == strings.ToLower(digest)
}

// CheckInspectionScratch refuses materialization inside the retained source backup.
// A common private parent remains valid because materialization creates fresh children.
func CheckInspectionScratch(source, scratch string) error {
	return checkRestorePathPairs([][2]string{{source, scratch}})
}
