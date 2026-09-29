package recovery

import (
	"errors"
	"path/filepath"
)

// ApplyHistoryRequest selects independent evidence and an existing private retry journal.
// Every attestation describes an external fact that the operator must establish.
type ApplyHistoryRequest struct {
	VerifyRequest
	Operation            RestoreOperation
	HistoryDirectory     string
	HistorySHA256        string
	ExpectedTargetSHA256 string
	JournalDirectory     string
	ValkeyIncarnation    string
	Attestation          HistoryAttestation
}

// Validate refuses incomplete input before configuration or native storage access.
func (r ApplyHistoryRequest) Validate() error {
	if err := r.VerifyRequest.Validate(); err != nil {
		return err
	}
	if err := r.Operation.Validate(); err != nil {
		return err
	}
	if !historyReference(r.Operation.FencingEvidence, 2048) || !historyDigest(r.HistorySHA256) || !historyDigest(r.ExpectedTargetSHA256) {
		return errors.New("history application requires exact digests and the unchanged fencing reference")
	}
	for _, path := range []string{r.HistoryDirectory, r.JournalDirectory} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return errors.New("history application requires clean absolute evidence and journal directories")
		}
	}
	if r.ValkeyIncarnation != "" && !historyReference(r.ValkeyIncarnation, 256) {
		return errors.New("history application requires a valid explicit Valkey incarnation")
	}
	a := r.Attestation
	if !historyReference(a.Operator, 4096) || !historyReference(a.Reference, 4096) || !a.WritersFenced || !a.AdmittedWorkAccounted {
		return errors.New("history application requires an operator, evidence reference, fenced writers, and accounted admitted work")
	}
	return nil
}
