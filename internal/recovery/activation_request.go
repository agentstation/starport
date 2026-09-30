package recovery

import (
	"fmt"
	"path/filepath"
)

// ActivationRequest binds the operator procedure to original source, history, and current target choices.
// Every writer must remain externally fenced until the procedure and fresh startup succeed.
type ActivationRequest struct {
	Prepare                 PrepareRequest
	History                 ApplyHistoryRequest
	ActivationDirectory     string
	PreserveTargetWorkspace bool
	// ExpectedDecisionSHA256 must identify the retained decision on every sealed retry.
	ExpectedDecisionSHA256 string
}

// ActivationResult separates historical completion from current permission.
// Fresh application startup and readiness remain required after activation.
type ActivationResult struct {
	DecisionSHA256        string `json:"decision_sha256"`
	CompletedPhases       int    `json:"completed_phases"`
	HistoricallyComplete  bool   `json:"historically_complete"`
	CurrentAdmissionValid bool   `json:"current_admission_valid"`
	Restricted            bool   `json:"restricted"`
	NextAction            string `json:"next_action"`
}

// Original removes only the caller retry selector from immutable decision inputs.
func (r ActivationRequest) Original() ActivationRequest {
	r.ExpectedDecisionSHA256 = ""
	return r
}

// Validate checks the shared source/history identity and disjoint activation directory.
func (r ActivationRequest) Validate() error {
	if r.Prepare.Validate() != nil || r.History.Validate() != nil || r.Prepare.VerifyRequest != r.History.VerifyRequest || r.Prepare.Operation != r.History.Operation || !r.History.Attestation.CompleteInterval {
		return ErrConflict
	}
	if !filepath.IsAbs(r.ActivationDirectory) || filepath.Clean(r.ActivationDirectory) != r.ActivationDirectory {
		return ErrConflict
	}
	if r.ExpectedDecisionSHA256 != "" && !activationRecordDigest(r.ExpectedDecisionSHA256) {
		return ErrConflict
	}
	for _, path := range []string{r.Prepare.Directory, r.Prepare.FilesDirectory, r.History.HistoryDirectory, r.History.JournalDirectory} {
		if CheckRestoreDestinations(r.ActivationDirectory, r.ActivationDirectory, path) != nil {
			return ErrConflict
		}
	}
	return nil
}
func activationRecordDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' && c < 'a' || c > 'f' {
			return false
		}
	}
	return true
}

// Format excludes operator evidence and private source paths.
func (ActivationRequest) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<private recovery activation request>"))
}
