package configrevision

// Request identifies one operator change of the shared configuration.
// Preview validates the change and writes nothing.
type Request struct {
	OperationID string
	Preview     bool
}

// ApplyRequest identifies one apply. Resume repeats only the fleet phase of
// the revision that OperationID committed.
type ApplyRequest struct {
	OperationID string
	Resume      bool
}

// Fence states of an apply.
const (
	// FenceApplied reports that the fleet policy record names the revision.
	FenceApplied = "applied"
	// FenceNotShared reports a deployment without a shared catalog lease. Each
	// process applies the revision when it restarts.
	FenceNotShared = "not_shared"
)

// Result reports one operator change. Written is false for a preview. Fence
// is empty when the change has no fleet phase.
type Result struct {
	Revision Revision `json:"revision"`
	Written  bool     `json:"written"`
	Fence    string   `json:"fence,omitempty"`
}
