package config

import (
	"fmt"
	"strings"
	"unicode"
)

// Configuration operation statuses. A saved revision that this process does
// not serve yet is saved. A revision that this process serves is applied.
const (
	OperationApplied = "applied"
	OperationSaved   = "saved"
	OperationRefused = "refused"
)

// ActionSave is the audit action of a configuration field save.
const ActionSave = "config.save"

// maxAuditSubjectBytes matches the audit subject column.
const maxAuditSubjectBytes = 191

// Refusal reasons of configuration operations.
const (
	RefusalStaleRevision      = "stale_revision"
	RefusalOperationConflict  = "operation_conflict"
	RefusalExternalManagement = "external_management"
	RefusalForeignDeployment  = "foreign_deployment"
	RefusalInvalidEdit        = "invalid_edit"
	RefusalMigrationBoundary  = "migration_boundary"
	RefusalPolicyChange       = "policy_change"
	RefusalSchemaBehind       = "schema_behind"
	RefusalUnavailable        = "unavailable"
	RefusalBusy               = "busy"
	RefusalIncomplete         = "incomplete"
)

// maxOperationIDBytes bounds an operation ID. It matches the revision store column.
const maxOperationIDBytes = 191

// FieldSave is one field-level configuration change. ExpectedRevision is the
// local file checksum or the decimal shared head sequence. Edits map a
// field-save key to a value. A null value removes the setting.
type FieldSave struct {
	OperationID      string             `json:"operation_id"`
	DeploymentID     string             `json:"deployment_id"`
	ExpectedRevision string             `json:"expected_revision"`
	Edits            map[string]*string `json:"edits"`
}

// ReceiptRevision identifies one configuration revision. A local revision is
// the file checksum with sequence zero. A shared revision is the decimal head
// sequence with the policy checksum.
type ReceiptRevision struct {
	Revision string `json:"revision"`
	Sequence int64  `json:"sequence"`
	Checksum string `json:"checksum"`
}

// Receipt reports one configuration operation. Saved is the revision that the
// write produced. Applied is the revision that this process serves. A receipt
// never carries setting values.
type Receipt struct {
	OperationID     string          `json:"operation_id"`
	DeploymentID    string          `json:"deployment_id"`
	Management      string          `json:"management"`
	Status          string          `json:"status"`
	Saved           ReceiptRevision `json:"saved"`
	Applied         ReceiptRevision `json:"applied"`
	ActivationError string          `json:"activation_error,omitempty"`
	AuditError      string          `json:"audit_error,omitempty"`
	Settings        []string        `json:"settings,omitempty"`
	Actor           string          `json:"actor"`
	CreatedAt       string          `json:"created_at"`
	Refusal         *Refusal        `json:"refusal,omitempty"`
}

// Validation reports a field save that was checked and not written. Preview
// is the revision that the save would write.
type Validation struct {
	Valid    bool            `json:"valid"`
	Current  ReceiptRevision `json:"current"`
	Preview  ReceiptRevision `json:"preview"`
	Settings []string        `json:"settings,omitempty"`
	Refusal  *Refusal        `json:"refusal,omitempty"`
}

// ConnectionResult reports a catalog source reachability test. Error never
// holds a credential or a source URL.
type ConnectionResult struct {
	Reachable bool   `json:"reachable"`
	Source    string `json:"source"`
	Error     string `json:"error,omitempty"`
}

// Refusal is a typed configuration operation refusal. Message, Expected, and
// Current never hold setting values.
type Refusal struct {
	Reason   string `json:"reason"`
	Message  string `json:"message"`
	Setting  string `json:"setting,omitempty"`
	Expected string `json:"expected,omitempty"`
	Current  string `json:"current,omitempty"`
}

func (r *Refusal) Error() string {
	if r.Setting != "" {
		return fmt.Sprintf("configuration %s (%s): %s", strings.ReplaceAll(r.Reason, "_", " "), r.Setting, r.Message)
	}
	return fmt.Sprintf("configuration %s: %s", strings.ReplaceAll(r.Reason, "_", " "), r.Message)
}

// StaleRevision returns the refusal of a save whose expected revision is not current.
func StaleRevision(expected, current string) *Refusal {
	return &Refusal{
		Reason: RefusalStaleRevision, Expected: expected, Current: current,
		Message: "the expected configuration revision is not the current revision. Read the current revision and save again",
	}
}

// OperationConflict returns the refusal of a reused operation ID.
func OperationConflict() *Refusal {
	return &Refusal{Reason: RefusalOperationConflict, Message: "the operation ID already names a different configuration change"}
}

// AuditSubject names a saved revision and its setting keys for an audit
// record. It never holds a value. When the keys do not fit the subject column,
// the subject counts the keys that it omits.
func AuditSubject(revision string, settings []string) string {
	for kept := len(settings); kept >= 0; kept-- {
		subject := revision + " settings=" + strings.Join(settings[:kept], ",")
		if kept < len(settings) {
			subject += fmt.Sprintf(" +%d", len(settings)-kept)
		}
		if len(subject) <= maxAuditSubjectBytes {
			return subject
		}
	}
	return revision
}

// CheckOperationRequest refuses a malformed operation ID, a foreign
// deployment, and external management. Every write checks these before it
// reads a store or a file.
func (c *Config) CheckOperationRequest(request FieldSave) error {
	if request.OperationID == "" || len(request.OperationID) > maxOperationIDBytes ||
		strings.TrimSpace(request.OperationID) != request.OperationID || strings.IndexFunc(request.OperationID, unicode.IsControl) >= 0 {
		return &Refusal{Reason: RefusalInvalidEdit, Message: fmt.Sprintf("operation_id requires 1 to %d bytes without surrounding whitespace or control characters", maxOperationIDBytes)}
	}
	return c.CheckWriteRequest(request.DeploymentID)
}

// CheckWriteRequest refuses a foreign deployment and external management.
func (c *Config) CheckWriteRequest(deploymentID string) error {
	if deploymentID != c.EffectivePaths().DeploymentID {
		return &Refusal{Reason: RefusalForeignDeployment, Message: "deployment_id does not name the deployment of this gateway"}
	}
	if c.ManagementMode() == ManagementExternal {
		return &Refusal{Reason: RefusalExternalManagement, Message: "an external controller manages this configuration. Change it through that controller"}
	}
	return nil
}
