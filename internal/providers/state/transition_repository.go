package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/sqlstore"
)

const (
	// transitionRetention bounds how far back the durable record reaches:
	// the same 90-day window the provider-published incident log renders,
	// so the two histories beside each other cover the same time.
	transitionRetention = 90 * 24 * time.Hour
	// maxTransitionReads caps one provider's read. Transitions record only
	// indicator changes, so a provider needs sustained flapping to reach it.
	maxTransitionReads = 100
)

// ErrTransitionStoreRequired reports a missing relational store.
var ErrTransitionStoreRequired = errors.New("incident transition storage is required")

// TransitionRepository is the durable record of the indicator changes this
// gateway observed on provider status pages. The live projection stays in
// the in-memory Store, which routing reads; this record exists so "what
// did the status page say at 3am" survives a restart, and only the console
// surface reads it.
type TransitionRepository interface {
	Record(ctx context.Context, transitions []IncidentTransition) error
	Transitions(ctx context.Context, providerID catalogs.ProviderID) ([]IncidentTransition, error)
}

type transitionRepository struct {
	db  *sqlstore.DB
	now func() time.Time
}

// OpenTransitions returns a sqlstore-backed transition repository. The
// caller has already migrated the store; this constructor only refuses a
// nil one.
func OpenTransitions(db *sqlstore.DB) (TransitionRepository, error) {
	if db == nil {
		return nil, ErrTransitionStoreRequired
	}
	return &transitionRepository{db: db, now: time.Now}, nil
}

// Record appends one pass's observed transitions and prunes entries older
// than the retention window. A transition without a provider is dropped
// rather than stored as an unaddressable row.
func (r *transitionRepository) Record(ctx context.Context, transitions []IncidentTransition) error {
	if len(transitions) == 0 {
		return nil
	}
	recorded := false
	for _, transition := range transitions {
		if transition.ProviderID == "" {
			continue
		}
		observedAt := transition.ObservedAt
		if observedAt.IsZero() {
			observedAt = r.now()
		}
		if _, err := r.db.ExecContext(ctx,
			r.db.Bind(`INSERT INTO incident_transitions (provider_id, indicator, description, observed_at) VALUES (?, ?, ?, ?)`),
			string(transition.ProviderID), transition.Indicator, transition.Description,
			observedAt.UTC().Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("record incident transition: %w", err)
		}
		recorded = true
	}
	if !recorded {
		return nil
	}
	// RFC 3339 strings in UTC order lexicographically, so the cutoff can be
	// compared as text the way the rows are stored.
	cutoff := r.now().UTC().Add(-transitionRetention).Format(time.RFC3339Nano)
	if _, err := r.db.ExecContext(ctx,
		r.db.Bind(`DELETE FROM incident_transitions WHERE observed_at < ?`), cutoff); err != nil {
		return fmt.Errorf("prune incident transitions: %w", err)
	}
	return nil
}

// Transitions returns one provider's observed transitions newest first,
// bounded at maxTransitionReads.
func (r *transitionRepository) Transitions(ctx context.Context, providerID catalogs.ProviderID) ([]IncidentTransition, error) {
	if providerID == "" {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx,
		r.db.Bind(`SELECT indicator, description, observed_at FROM incident_transitions WHERE provider_id = ? ORDER BY observed_at DESC LIMIT ?`),
		string(providerID), maxTransitionReads)
	if err != nil {
		return nil, fmt.Errorf("read incident transitions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var transitions []IncidentTransition
	for rows.Next() {
		var indicator, description, observed string
		if err := rows.Scan(&indicator, &description, &observed); err != nil {
			return nil, fmt.Errorf("scan incident transition: %w", err)
		}
		observedAt, err := time.Parse(time.RFC3339Nano, observed)
		if err != nil {
			return nil, fmt.Errorf("parse incident transition time: %w", err)
		}
		transitions = append(transitions, IncidentTransition{
			ProviderID:  providerID,
			Indicator:   indicator,
			Description: description,
			ObservedAt:  observedAt.UTC(),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read incident transitions: %w", err)
	}
	return transitions, nil
}
