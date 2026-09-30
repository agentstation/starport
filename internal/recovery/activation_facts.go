package recovery

import (
	"bytes"
	"encoding/json/v2"
)

// ActivationFacts names original checked native positions and the closed target boundary.
// These facts require the owning history capability and separate current permission checks.
type ActivationFacts struct {
	Boundary  Record
	Positions HistoryReplayPositions
}

// Record exports original bounded evidence for the application activation decision.
// Diagnostic reports cannot replace this record or the required Check operation.
func (c *ClosedFinalHistory) Record() ([]byte, error) {
	if c == nil || c.state == nil || len(c.state.body) == 0 || len(c.state.body) > historyManifestMaxBytes {
		return nil, ErrConflict
	}
	canonical, err := json.Marshal(c.state.record, json.Deterministic(true))
	if err != nil || !bytes.Equal(canonical, c.state.body) {
		return nil, ErrConflict
	}
	return bytes.Clone(c.state.body), nil
}

// ActivationFacts returns original positions from private checked history.
// The factory checks the retained graph and native positions before returning this capability.
// Application sealing still requires current owner checks and closed native guards.
func (c *ClosedFinalHistory) ActivationFacts() (ActivationFacts, error) {
	if _, err := c.Record(); err != nil {
		return ActivationFacts{}, err
	}
	return ActivationFacts{c.state.record.GraphRequest.Boundary, c.state.record.Replay.Positions}, nil
}

// Record exports the original retained record without replay, repair, or recapture.
func (h *RetainedActivationHistory) Record() ([]byte, error) {
	if h == nil || h.state == nil || len(h.state.body) == 0 || len(h.state.body) > historyManifestMaxBytes {
		return nil, ErrConflict
	}
	canonical, err := json.Marshal(h.state.record, json.Deterministic(true))
	if err != nil || !bytes.Equal(canonical, h.state.body) {
		return nil, ErrConflict
	}
	return bytes.Clone(h.state.body), nil
}

// ActivationFacts returns the original positions after passive owner inspection.
// The historical record grants no current admission or component release permission.
func (h *RetainedActivationHistory) ActivationFacts() (ActivationFacts, error) {
	if _, err := h.Record(); err != nil {
		return ActivationFacts{}, err
	}
	return ActivationFacts{h.state.record.GraphRequest.Boundary, h.state.record.Replay.Positions}, nil
}

// Boundary returns the accepted closed boundary, never an approval to serve traffic.
func (a *AcceptedHistory) Boundary() (Record, error) {
	if a == nil || a.state == nil || a.state.boundary.Open {
		return Record{}, ErrConflict
	}
	return a.state.boundary, nil
}
