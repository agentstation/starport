package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	starmaperrors "github.com/agentstation/starmap/pkg/errors"

	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
)

// Policy states that the catalog status reports. An empty state means that
// this replica has no shared policy to compare.
const (
	PolicyMatch    = "match"
	PolicyMismatch = "policy_mismatch"
)

// appliedPolicyMaxBytes bounds the applied policy record.
const appliedPolicyMaxBytes = 1024

// AppliedPolicy is the shared configuration revision that the fleet applied.
// The checksum identifies the deployment-scope values without credentials.
type AppliedPolicy struct {
	Sequence    int64  `json:"sequence"`
	Checksum    string `json:"checksum"`
	OperationID string `json:"operation_id"`
}

func (p AppliedPolicy) validate() error {
	if p.Sequence <= 0 || len(p.Checksum) != 64 || strings.TrimSpace(p.OperationID) == "" || len(p.OperationID) > 1024 {
		return errors.New("applied configuration policy requires a positive sequence, a SHA-256 checksum, and an operation ID")
	}
	return nil
}

// PolicyMismatchError refuses leadership to a replica whose applied
// configuration differs from the applied fleet policy. It is a lease conflict,
// so the replica keeps serving its accepted catalog.
type PolicyMismatchError struct {
	Applied AppliedPolicy
	Local   string
}

func (e *PolicyMismatchError) Error() string {
	return fmt.Sprintf("%s: this replica applied configuration checksum %s, and the fleet applied revision %d with checksum %s. Restart this replica on the applied revision", PolicyMismatch, e.Local, e.Applied.Sequence, e.Applied.Checksum)
}

// Unwrap reports the lease conflict that the Starmap runtime treats as a refusal.
func (e *PolicyMismatchError) Unwrap() error {
	return &starmaperrors.ConflictError{Resource: "catalog fleet", Message: "the applied configuration policy differs from this replica"}
}

// fleetPolicy is the applied configuration checksum of this replica and the
// result of its last comparison.
type fleetPolicy struct {
	mu       sync.Mutex
	checksum string
	state    string
}

// fencePolicy binds the checksum of this replica's applied shared
// configuration. An empty checksum leaves the lease unfenced.
func (s *FleetStore) fencePolicy(checksum string) {
	if s == nil {
		return
	}
	s.policy.mu.Lock()
	defer s.policy.mu.Unlock()
	s.policy.checksum = checksum
}

// PolicyState reports the last comparison with the applied fleet policy.
func (s *FleetStore) PolicyState() string {
	if s == nil {
		return ""
	}
	s.policy.mu.Lock()
	defer s.policy.mu.Unlock()
	return s.policy.state
}

func (s *FleetStore) appliedPolicyKey() string {
	return "catalog:config:{" + payloadDigest([]byte(s.identity.DeploymentID)) + "}:v1:applied"
}

// checkAppliedPolicy runs before each lease acquisition and renewal. An
// absent record fences nothing: no apply has completed.
func (s *FleetStore) checkAppliedPolicy(ctx context.Context) error {
	s.policy.mu.Lock()
	checksum := s.policy.checksum
	s.policy.mu.Unlock()
	if checksum == "" {
		return nil
	}
	applied, _, err := s.readAppliedPolicy(ctx)
	if errors.Is(err, storage.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	state := PolicyMatch
	if applied.Checksum != checksum {
		state = PolicyMismatch
	}
	s.policy.mu.Lock()
	s.policy.state = state
	s.policy.mu.Unlock()
	if state == PolicyMismatch {
		return &PolicyMismatchError{Applied: applied, Local: checksum}
	}
	return nil
}

func (s *FleetStore) readAppliedPolicy(ctx context.Context) (AppliedPolicy, []byte, error) {
	data, _, err := s.store.ReadWithLifetime(ctx, s.appliedPolicyKey(), appliedPolicyMaxBytes)
	if err != nil {
		return AppliedPolicy{}, nil, err
	}
	var applied AppliedPolicy
	if err := json.Unmarshal(data, &applied); err != nil {
		return AppliedPolicy{}, nil, fleetStoreConflict("the applied configuration policy record is invalid")
	}
	if err := applied.validate(); err != nil {
		return AppliedPolicy{}, nil, fleetStoreConflict("the applied configuration policy record is invalid")
	}
	return applied, data, nil
}

// writeAppliedPolicy records the applied policy with a compare-and-swap on
// the record alone. An older sequence never replaces a newer one. A repeated
// operation is idempotent.
func (s *FleetStore) writeAppliedPolicy(ctx context.Context, applied AppliedPolicy) error {
	if err := applied.validate(); err != nil {
		return err
	}
	if err := s.checkApproval(ctx); err != nil {
		return err
	}
	current, previous, err := s.readAppliedPolicy(ctx)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	if previous != nil {
		switch {
		case current == applied:
			return nil
		case current.Sequence > applied.Sequence:
			return fleetStoreConflict(fmt.Sprintf("the fleet already applied newer configuration revision %d", current.Sequence))
		case current.Sequence == applied.Sequence:
			return fleetStoreConflict("another operation applied this configuration revision")
		}
	}
	encoded, err := json.Marshal(applied)
	if err != nil {
		return err
	}
	err = s.store.CompareAndSwap(ctx, []storage.CompareAndSwapMutation{
		{Key: s.appliedPolicyKey(), ExpectedValue: previous, NewValue: encoded},
	})
	if errors.Is(err, storage.ErrConflict) {
		return fleetStoreConflict("the applied configuration policy changed")
	}
	return err
}

// ErrPolicyFenceUnavailable reports a deployment without shared catalog
// storage. Its single process has no lease to fence.
var ErrPolicyFenceUnavailable = errors.New("the configured storage has no shared catalog lease to fence")

// ApplyPolicy writes the applied policy record. It never takes the catalog
// refresh lease, so a sitting leader cannot block it. The next lease
// acquisition or renewal compares the record: a leader with another checksum
// loses the lease at its next renewal and keeps serving.
func ApplyPolicy(ctx context.Context, store storage.KVStore, db *sqlstore.DB, deployment string, applied AppliedPolicy) error {
	if err := applied.validate(); err != nil {
		return err
	}
	fleet, err := openRecoveryFleet(ctx, store, db, deployment)
	if err != nil {
		return err
	}
	if fleet == nil {
		return ErrPolicyFenceUnavailable
	}
	return fleet.writeAppliedPolicy(ctx, applied)
}
