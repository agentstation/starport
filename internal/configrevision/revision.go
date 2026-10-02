// Package configrevision owns the shared deployment configuration authority:
// the head that selects one revision per deployment, the append-only revision
// history, and the audit record of each change. A revision holds only
// deployment-scope catalog settings. Bootstrap inputs never enter it.
//
// The head and each revision commit with their audit record in one SQL
// transaction. A stale writer, a second initializer, and a reused operation ID
// each receive a typed refusal.
package configrevision

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/agentstation/starport/internal/config"
)

// Audit actions of the revision store.
const (
	ActionInitialize = "config.initialize"
	ActionCommit     = "config.commit"
)

// Authorities that a revision records. A local revision releases the shared
// authority: a shared-management process refuses to start against it.
const (
	AuthorityShared = config.ManagementShared
	AuthorityLocal  = config.ManagementLocal
)

// maxIdentifierBytes bounds the actor and operation ID columns.
const maxIdentifierBytes = 191

// ErrNotInitialized reports a store that holds no head for the deployment.
var ErrNotInitialized = errors.New("shared configuration is not initialized. Run starport config init --shared")

// maxListedDeployments bounds the other deployment IDs that a
// *NotInitializedError names.
const maxListedDeployments = 10

// NotInitializedError reports a store that holds no head for the deployment
// while it holds the heads of other deployments. It names their stored
// deployment IDs, so an operator sees a mis-pointed database before an
// initialization. errors.Is matches ErrNotInitialized.
type NotInitializedError struct {
	DeploymentID string
	// Stored holds at most maxListedDeployments other deployment IDs.
	Stored []string
	// Others counts every other stored head.
	Others int
}

func (e *NotInitializedError) Error() string {
	quoted := make([]string, len(e.Stored))
	for index, id := range e.Stored {
		quoted[index] = strconv.Quote(id)
	}
	listed := strings.Join(quoted, ", ")
	if more := e.Others - len(e.Stored); more > 0 {
		listed += fmt.Sprintf(", and %d more", more)
	}
	return fmt.Sprintf("shared configuration of deployment %q is not initialized. This store holds the shared configuration of deployments %s. Check STARPORT_DEPLOYMENT_ID and the relational store before you run starport config init --shared", e.DeploymentID, listed)
}

// Is matches ErrNotInitialized.
func (e *NotInitializedError) Is(target error) bool { return target == ErrNotInitialized }

// UnsealError reports a sealed source credential that the master key cannot
// open. It names the setting and never the value. It is not ErrCorrupt: the
// checksum of the revision matched.
type UnsealError struct {
	Setting string
	Err     error
}

func (e *UnsealError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("shared configuration revision holds a sealed %s. Reading it requires STARPORT_SECURITY_MASTER_KEY", e.Setting)
	}
	return fmt.Sprintf("shared configuration revision holds a sealed %s that STARPORT_SECURITY_MASTER_KEY cannot open: %v", e.Setting, e.Err)
}

func (e *UnsealError) Unwrap() error { return e.Err }

// ErrOperationConflict reports an operation ID that already committed a
// different target.
var ErrOperationConflict = errors.New("operation ID already committed a different configuration revision")

// ErrCorrupt reports a stored head or revision that fails its contract.
var ErrCorrupt = errors.New("stored shared configuration revision is corrupt")

// UnavailableError reports a store that could not answer. It is never an
// absent head: a caller must not seed or fall back to local values.
type UnavailableError struct {
	Err error
}

func (e *UnavailableError) Error() string {
	return fmt.Sprintf("shared configuration store is unavailable: %v", e.Err)
}

func (e *UnavailableError) Unwrap() error { return e.Err }

// InitializedError reports a second initializer. It carries the stored head.
type InitializedError struct {
	Head Head
}

func (e *InitializedError) Error() string {
	return fmt.Sprintf("shared configuration is already initialized at revision %d in namespace %q", e.Head.Sequence, e.Head.Namespace)
}

// StaleRevisionError reports a commit whose expected sequence is no longer the head.
type StaleRevisionError struct {
	Expected int64
	Current  int64
}

func (e *StaleRevisionError) Error() string {
	return fmt.Sprintf("shared configuration revision %d is stale. The head is revision %d", e.Expected, e.Current)
}

// Head identifies the selected revision of one deployment.
type Head struct {
	DeploymentID string `json:"deployment_id"`
	Namespace    string `json:"namespace"`
	Sequence     int64  `json:"sequence"`
	RevisionID   string `json:"revision_id"`
	Authority    string `json:"authority"`
	Checksum     string `json:"checksum"`
}

// Revision is one stored revision with its values. Values can hold source
// credentials.
type Revision struct {
	Head
	Values      map[string]string `json:"-"`
	OperationID string            `json:"operation_id"`
	Actor       string            `json:"actor"`
	CreatedAt   string            `json:"created_at"`
}

// Shared returns the revision in the form that the configuration applies.
func (r Revision) Shared() config.SharedRevision {
	return config.SharedRevision{
		DeploymentID: r.DeploymentID, Namespace: r.Namespace, Sequence: r.Sequence,
		RevisionID: r.RevisionID, Checksum: r.Checksum, Values: r.Values,
	}
}

// Sealer encrypts source credentials before they enter a revision record.
// The credential encryption service of the master key implements it.
type Sealer interface {
	EncryptCredential(plaintext string) (string, error)
	DecryptCredential(encrypted string) (string, error)
}

func validIdentifier(field, value string) error {
	if value == "" || len(value) > maxIdentifierBytes || strings.TrimSpace(value) != value || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return fmt.Errorf("%s requires 1 to %d bytes without surrounding whitespace or control characters", field, maxIdentifierBytes)
	}
	return nil
}
