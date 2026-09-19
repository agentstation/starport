package authorization

import "time"

const recoveryNone = "none"

// Status describes this replica's policy observations, not fleet enforcement.
// Counts describe cached bundles at observation time, not all deployed callers.
type Status struct {
	Ready              bool              `json:"ready"`
	PermissionLifetime time.Duration     `json:"permission_lifetime_ns"`
	ValidBundles       int               `json:"valid_bundles"`
	InvalidBundles     int               `json:"invalid_bundles"`
	LatestDeadline     time.Time         `json:"latest_deadline,omitempty"`
	Authorities        []PolicyStatus    `json:"authorities"`
	Observations       []AuthorityStatus `json:"observations"`
	Recovery           string            `json:"recovery"`
}

// PolicyStatus identifies a prerequisite without exposing caller or storage data.
type PolicyStatus struct {
	Authority string `json:"authority"`
	Sequence  uint64 `json:"sequence"`
	State     string `json:"state"`
	Recovery  string `json:"recovery"`
}

// Status reads bounded memory without renewing receipts or querying authorities.
func (c *Cache) Status() Status {
	status := Status{Recovery: "initialize_authorization"}
	if c == nil {
		return status
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	status.PermissionLifetime = c.limits.PermissionLifetime
	now, healthy := c.clock()
	status.Ready = !c.closed && healthy && !now.IsZero()
	status.Recovery = recoveryNone
	if c.closed {
		status.Recovery = "restart_authorization"
	} else if !healthy || now.IsZero() {
		status.Recovery = "restore_policy_clock_and_reverify"
	}
	for _, fence := range c.authorities.fences {
		item := PolicyStatus{Authority: fence.authority, State: "ready", Recovery: recoveryNone}
		state := fence.state.Load()
		switch {
		case state == nil || state.blocked:
			item.State, item.Recovery = "withdrawn", "reinitialize_authority_epoch"
		case state.mutations != 0:
			item.State, item.Recovery = "mutation_pending", "await_mutation_and_reverify"
		}
		if state != nil {
			item.Sequence = state.sequence
		}
		if item.State != "ready" {
			status.Ready = false
		}
		status.Authorities = append(status.Authorities, item)
	}
	for _, entry := range c.entries {
		if entry.bundle == nil {
			continue
		}
		if entry.bundle.receipt.Check(now, healthy) != nil {
			status.InvalidBundles++
			continue
		}
		status.ValidBundles++
		deadline := entry.bundle.receipt.Deadline()
		if deadline.After(status.LatestDeadline) {
			status.LatestDeadline = deadline
		}
	}
	return status
}
