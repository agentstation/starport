package reservation

import (
	"context"
	"encoding/json/v2"
	"errors"
	"time"

	"github.com/agentstation/starport/internal/storage"
)

// History identifies a verified accounting boundary and its audit evidence.
// A policy owner preserves ID across limit edits. Removing a budget or changing
// its interval requires a new ID and reconciliation of the resulting history gap.
type History struct {
	ID    string `json:"id"`
	Proof string `json:"proof"`
}

type historyState struct {
	Version int                `json:"version"`
	Meter   Meter              `json:"meter"`
	History History            `json:"history"`
	Current storage.TimeWindow `json:"current"`
}

// FreshHistoryMutation belongs in the same atomic batch that creates a new holder.
// The owning repository must prove holder absence in that batch. This function
// cannot establish zero consumption for an existing holder or replace lost state.
func FreshHistoryMutation(meter Meter, id string) (storage.CompareAndSwapMutation, error) {
	if !meter.valid() || !validID(id) {
		return storage.CompareAndSwapMutation{}, ErrInvalid
	}
	state := historyState{Version: recordVersion, Meter: meter, History: History{ID: id, Proof: "new-holder:" + id}}
	if !validID(state.History.Proof) {
		return storage.CompareAndSwapMutation{}, ErrInvalid
	}
	return encodeMutation(storageKey("history", meter), nil, state)
}

func (r *Repository) readHistory(ctx context.Context, meter Meter, id string) (*historyState, []byte, error) {
	data, err := r.read(ctx, storageKey("history", meter))
	if errors.Is(err, storage.ErrNotFound) {
		return nil, nil, ErrHistoryUnknown
	}
	if err != nil {
		return nil, nil, err
	}
	var state historyState
	if json.Unmarshal(data, &state) != nil || state.Version != recordVersion || state.Meter != meter || !validID(state.History.ID) || !validID(state.History.Proof) {
		return nil, nil, ErrUnavailable
	}
	if state.History.ID != id {
		return nil, nil, ErrHistoryUnknown
	}
	if state.Current != (storage.TimeWindow{}) && state.Current != windowFor(meter.Interval, state.Current.Start) {
		return nil, nil, ErrUnavailable
	}
	return &state, data, nil
}

// admissionWindow opens a later window only under intact continuous history.
// The returned history mutation fences creation of its counter in the same batch.
func (r *Repository) admissionWindow(ctx context.Context, rule Rule, now time.Time) (*WindowState, []byte, storage.CompareAndSwapMutation, error) {
	head, previous, err := r.readHistory(ctx, rule.Meter, rule.HistoryID)
	if err != nil {
		return nil, nil, storage.CompareAndSwapMutation{}, err
	}
	window := windowFor(rule.Meter.Interval, now)
	if window.Start.Before(head.Current.Start) {
		return nil, nil, storage.CompareAndSwapMutation{}, storage.ErrTimeWindowChanged
	}
	state, data, err := r.readWindow(ctx, rule.Meter, window)
	if head.Current == window {
		if err != nil {
			return nil, nil, storage.CompareAndSwapMutation{}, err
		}
		if state.HistoryID != rule.HistoryID {
			return nil, nil, storage.CompareAndSwapMutation{}, ErrHistoryUnknown
		}
	} else {
		if head.Current != (storage.TimeWindow{}) {
			prior, _, priorErr := r.readWindow(ctx, rule.Meter, head.Current)
			if priorErr != nil {
				return nil, nil, storage.CompareAndSwapMutation{}, priorErr
			}
			if prior.HistoryID != rule.HistoryID {
				return nil, nil, storage.CompareAndSwapMutation{}, ErrHistoryUnknown
			}
		}
		if !errors.Is(err, ErrHistoryUnknown) {
			if err == nil {
				err = ErrHistoryUnknown
			}
			return nil, nil, storage.CompareAndSwapMutation{}, err
		}
		state = &WindowState{Version: windowRecordVersion, Meter: rule.Meter, Window: window, HistoryProof: head.History.Proof, HistoryID: head.History.ID}
		data = nil
		head.Current = window
	}
	mutation, err := encodeMutation(storageKey("history", rule.Meter), previous, head)
	return state, data, mutation, err
}
