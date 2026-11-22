package recovery

import (
	"errors"
	"path/filepath"
	"strings"
	"unicode"
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

// WriteHistoryRequest selects a verified backup, a fenced target, and a final-only history package.
// The command derives the package's target and SQL bindings. The request must leave them empty.
type WriteHistoryRequest struct {
	VerifyRequest
	History              HistoryWriteRequest
	ExpectedTargetSHA256 string
	ValkeyIncarnation    string
}

// HistoryWriteReport identifies a written package. Retain HistorySHA256 outside the package.
type HistoryWriteReport struct {
	Directory     string `json:"directory"`
	HistorySHA256 string `json:"history_sha256"`
	TargetSHA256  string `json:"target_sha256"`
	DeclaredSteps int    `json:"declared_steps"`
}

// Validate refuses incomplete input before configuration or native storage access.
func (r WriteHistoryRequest) Validate() error {
	if err := r.VerifyRequest.Validate(); err != nil {
		return err
	}
	if err := r.History.Validate(); err != nil {
		return err
	}
	if r.History.TargetSHA256 != "" || r.History.SQLExpected != nil {
		return errors.New("history writing derives the target and SQL bindings from the fenced target")
	}
	if r.ExpectedTargetSHA256 != "" && !historyDigest(r.ExpectedTargetSHA256) {
		return errors.New("history writing requires an exact expected target digest when one is selected")
	}
	if r.ValkeyIncarnation != "" && !historyReference(r.ValkeyIncarnation, 256) {
		return errors.New("history writing requires a valid explicit Valkey incarnation")
	}
	return nil
}

// PublishFilesRequest selects one canonical file role after restricted preparation.
// Role support and target paths belong to the host's file owners.
type PublishFilesRequest struct {
	PrepareRequest
	Role string
}

// Validate checks the request before configuration or target access.
func (r PublishFilesRequest) Validate() error {
	if err := r.PrepareRequest.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(r.Role) == "" || len(r.Role) > 128 || strings.ContainsFunc(r.Role, unicode.IsControl) {
		return errors.New("file publication requires a bounded canonical role")
	}
	return nil
}

// PublishFilesResult reports one published role and every remaining file disposition.
// The preparation barrier remains closed. This receipt never approves admission.
type PublishFilesResult struct {
	Preparation PrepareResult     `json:"preparation"`
	Role        string            `json:"role"`
	Tree        FileTreeResult    `json:"tree"`
	Remaining   []FileDisposition `json:"remaining"`
}
