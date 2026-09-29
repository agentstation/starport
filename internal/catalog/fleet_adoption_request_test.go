package catalog

import (
	"strings"
	"testing"

	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/stretchr/testify/require"
)

func TestFleetAdoptionRequestRequiresBoundedRecoveryEvidence(t *testing.T) {
	approved := recovery.Record{DeploymentID: "deployment", Epoch: 1, Open: true, BackendID: "source", Evidence: "original-approval"}
	closed := approved
	closed.Epoch, closed.Open = 2, false
	request := FleetAdoptionRequest{SourceApproval: approved, Closed: closed,
		Head: runtime.FleetHead{Identity: runtime.FleetIdentity{DeploymentID: "deployment", BackendID: "source", RecoveryEpoch: 1}, Revision: 1,
			GenerationID: "generation", RecoveryChecksum: strings.Repeat("a", 64)},
		BackendID: "replacement", OperationID: "restore", Evidence: "fenced-and-reconciled"}
	require.NoError(t, request.validate())
	restored := request
	restored.Closed.BackendID = ""
	restored.Closed.Evidence = "restore-prepared:verified-policy"
	require.NoError(t, restored.validate(), "restored SQL deliberately removes the previous backend identity")
	for _, scenario := range []struct {
		name   string
		change func(*FleetAdoptionRequest)
	}{
		{"open-gate", func(r *FleetAdoptionRequest) { r.Closed.Open = true }},
		{"obsolete-epoch", func(r *FleetAdoptionRequest) { r.Closed.Epoch = 1 }},
		{"foreign-deployment", func(r *FleetAdoptionRequest) { r.Closed.DeploymentID = "other" }},
		{"unapproved-source", func(r *FleetAdoptionRequest) { r.SourceApproval.Open = false }},
		{"missing-source-evidence", func(r *FleetAdoptionRequest) { r.SourceApproval.Evidence = "" }},
		{"oversized-source-evidence", func(r *FleetAdoptionRequest) { r.SourceApproval.Evidence = strings.Repeat("a", 4097) }},
		{"missing-source-backend", func(r *FleetAdoptionRequest) { r.SourceApproval.BackendID = "" }},
		{"blank-closed-backend", func(r *FleetAdoptionRequest) { r.Closed.BackendID = " " }},
		{"oversized-closed-backend", func(r *FleetAdoptionRequest) { r.Closed.BackendID = strings.Repeat("a", 257) }},
		{"missing-closed-evidence", func(r *FleetAdoptionRequest) { r.Closed.Evidence = "" }},
		{"foreign-source", func(r *FleetAdoptionRequest) { r.SourceApproval.BackendID = "other" }},
		{"missing-operation", func(r *FleetAdoptionRequest) { r.OperationID = "" }},
		{"missing-evidence", func(r *FleetAdoptionRequest) { r.Evidence = "" }},
		{"blank-backend", func(r *FleetAdoptionRequest) { r.BackendID = " " }},
		{"oversized-backend", func(r *FleetAdoptionRequest) { r.BackendID = strings.Repeat("a", 257) }},
		{"empty-head", func(r *FleetAdoptionRequest) { r.Head = runtime.FleetHead{} }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			changed := request
			scenario.change(&changed)
			require.Error(t, changed.validate())
		})
	}
}
