package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	starmaperrors "github.com/agentstation/starmap/pkg/errors"
	"github.com/agentstation/starmap/runtime"

	"github.com/agentstation/starport/internal/storage"
)

// fleetGrant excludes the advisory host expiry. Native PTTL owns lease lifetime.
type fleetGrant struct {
	Holder   string                `json:"holder"`
	Session  string                `json:"session"`
	Epoch    uint64                `json:"epoch"`
	Identity runtime.FleetIdentity `json:"identity"`
}

func encodeFleetGrant(lease runtime.Lease) ([]byte, error) {
	return json.Marshal(fleetGrant{Holder: lease.Holder, Session: lease.SessionID, Epoch: lease.Epoch, Identity: lease.Identity})
}

// AcquireLease advances a durable epoch and creates a separate expiring native grant.
func (s *FleetStore) AcquireLease(ctx context.Context, holder string, ttl time.Duration) (runtime.Lease, error) {
	if strings.TrimSpace(holder) == "" || len(holder) > 1024 || ttl <= 0 {
		return runtime.Lease{}, errors.New("fleet lease requires a bounded holder and positive lifetime")
	}
	if err := s.checkApproval(ctx); err != nil {
		return runtime.Lease{}, err
	}
	head, err := s.CurrentHead(ctx)
	if err != nil && !starmaperrors.IsNotFound(err) {
		return runtime.Lease{}, err
	}
	var headValue []byte
	if head != (runtime.FleetHead{}) {
		headValue, err = json.Marshal(head)
		if err != nil {
			return runtime.Lease{}, err
		}
	}
	current, _, err := s.store.ReadWithLifetime(ctx, s.prefix+"lease", 4096)
	if err == nil {
		var held fleetGrant
		if err := json.Unmarshal(current, &held); err != nil {
			return runtime.Lease{}, err
		}
		if held.Holder == holder && held.Session != s.session {
			return runtime.Lease{}, &starmaperrors.ConfigError{Component: "fleet instance identity", Message: "another process uses this instance ID"}
		}
		return runtime.Lease{}, fleetStoreConflict("a process already owns the catalog refresh lease")
	}
	if !errors.Is(err, storage.ErrNotFound) {
		return runtime.Lease{}, err
	}
	previous, _, err := s.store.ReadWithLifetime(ctx, s.prefix+"epoch", 32)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return runtime.Lease{}, err
	}
	var epoch uint64
	if previous != nil {
		epoch, err = strconv.ParseUint(string(previous), 10, 64)
		if err != nil || epoch == 0 || epoch == math.MaxUint64 {
			return runtime.Lease{}, errors.New("fleet lease epoch is invalid or exhausted")
		}
	} else {
		_, _, err := s.store.ReadWithLifetime(ctx, s.prefix+"head", 4096)
		if err == nil {
			return runtime.Lease{}, fleetStoreConflict("the durable lease epoch is missing from a populated store")
		}
		if !errors.Is(err, storage.ErrNotFound) {
			return runtime.Lease{}, err
		}
	}
	lease := runtime.Lease{Holder: holder, SessionID: s.session, Epoch: epoch + 1, Identity: s.identity, ExpiresAt: time.Now().Add(ttl).UTC()}
	encoded, err := encodeFleetGrant(lease)
	if err != nil {
		return runtime.Lease{}, err
	}
	err = s.store.CompareAndSwap(ctx, []storage.CompareAndSwapMutation{
		{Key: s.prefix + "head", ExpectedValue: headValue, NewValue: headValue},
		{Key: s.prefix + "epoch", ExpectedValue: previous, NewValue: []byte(strconv.FormatUint(lease.Epoch, 10))},
		{Key: s.prefix + "lease", NewValue: encoded, TTL: ttl},
	})
	if errors.Is(err, storage.ErrConflict) {
		return runtime.Lease{}, fleetStoreConflict("another process acquired the catalog refresh lease")
	}
	if err != nil {
		return runtime.Lease{}, err
	}
	return lease, nil
}

// Renew extends only the original live native grant.
func (s *FleetStore) Renew(ctx context.Context, lease runtime.Lease, ttl time.Duration) (runtime.Lease, error) {
	if ttl <= 0 {
		return runtime.Lease{}, errors.New("fleet lease renewal requires a positive lifetime")
	}
	if err := s.checkLeaseOwner(ctx, lease); err != nil {
		return runtime.Lease{}, err
	}
	encoded, err := encodeFleetGrant(lease)
	if err != nil {
		return runtime.Lease{}, err
	}
	if err := s.store.CompareAndSwap(ctx, []storage.CompareAndSwapMutation{
		{Key: s.prefix + "lease", ExpectedValue: encoded, NewValue: encoded, TTL: ttl},
	}, s.prefix+"lease"); err != nil {
		if errors.Is(err, storage.ErrConflict) {
			return runtime.Lease{}, fleetStoreConflict("the original refresh lease expired or changed")
		}
		return runtime.Lease{}, err
	}
	lease.ExpiresAt = time.Now().Add(ttl).UTC()
	return lease, nil
}

// Release removes only this process's original grant and preserves the durable epoch.
func (s *FleetStore) Release(ctx context.Context, lease runtime.Lease) error {
	if err := s.checkLeaseOwner(ctx, lease); err != nil {
		return err
	}
	encoded, err := encodeFleetGrant(lease)
	if err != nil {
		return err
	}
	err = s.store.CompareAndSwap(ctx, []storage.CompareAndSwapMutation{{Key: s.prefix + "lease", ExpectedValue: encoded}}, s.prefix+"lease")
	if errors.Is(err, storage.ErrConflict) {
		return nil
	}
	return err
}

func (s *FleetStore) checkLeaseOwner(ctx context.Context, lease runtime.Lease) error {
	if lease.Holder == "" || lease.Epoch == 0 || lease.SessionID != s.session || lease.Identity != s.identity {
		return fleetStoreConflict("the refresh lease does not belong to this process and recovery identity")
	}
	return s.checkApproval(ctx)
}
