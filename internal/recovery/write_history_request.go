package recovery

import "errors"

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
