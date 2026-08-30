package proxy

import (
	"context"
	"testing"

	"github.com/agentstation/starmap"
	starmapcatalogs "github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/inference"
	routepkg "github.com/agentstation/starport/internal/router"
	"github.com/agentstation/starport/internal/usage"
)

// The one compiled moderation provider prices the operation at zero and its
// wire answers with no usage block at all. The meter's honest record for such
// a turn is a nil cost with the no-usage reason, not a zero-dollar bill that
// claims the provider reported what it never sent.

// shippedModerationRoute projects the shipped catalog with the moderation
// provider's adapter registered and answers one routable moderation route.
// Using the real projection rather than a hand-built snapshot proves the
// generic operation intersection actually serves the new operation.
func shippedModerationRoute(t *testing.T) (*runtimecatalog.RoutableSnapshot, string) {
	t.Helper()
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(client)
	require.NoError(t, err)

	found := ""
	for _, provider := range client.Catalog().Providers().List() {
		offerings, err := client.Catalog().ProviderOfferings(provider.ID)
		require.NoError(t, err)
		for _, offering := range offerings {
			if !offering.Supports(starmapcatalogs.ProviderOperationModerations) {
				continue
			}
			types := make([]starmapcatalogs.EndpointType, 0, len(offering.Endpoints))
			for _, endpoint := range offering.Endpoints {
				types = append(types, endpoint.Type)
			}
			require.NoError(t, plane.SetAdapter(runtimecatalog.AdapterAvailability{
				ProviderID:    provider.ID,
				Registered:    true,
				Operations:    append([]starmapcatalogs.ProviderOperation(nil), offering.Service.Operations...),
				EndpointTypes: types,
			}))
			if found == "" {
				found = string(provider.ID) + "/" + string(offering.ProviderModelID)
			}
		}
	}
	require.NotEmpty(t, found, "the shipped catalog serves no moderation offering")

	snapshot := plane.Current()
	_, routable := snapshot.ResolveRoute(found)
	require.Truef(t, routable, "%s is not routable", found)
	return snapshot, found
}

// moderationRouter answers the moderation route and counts what reached it.
type moderationRouter struct {
	*capturingRouter
	snapshot *runtimecatalog.RoutableSnapshot
	routeID  string
	calls    int
}

func (r *moderationRouter) RouteModerations(
	_ context.Context,
	req *routepkg.ModerationRequest,
) (*routepkg.ModerationResponse, error) {
	r.calls++
	return &routepkg.ModerationResponse{
		Response: inference.ModerationResponse{
			ID:    "modr-1",
			Model: req.Request.Model,
			Results: []inference.ModerationResult{{
				Flagged:    true,
				Categories: []inference.ModerationCategory{{Name: "violence", Flagged: true, Score: 0.94}},
			}},
		},
		ModelUsed:       r.routeID,
		ProviderUsed:    "openai",
		Attempts:        1,
		CatalogSnapshot: r.snapshot,
	}, nil
}

func moderationTurn() *ModerationRequest {
	return &ModerationRequest{
		Request: inference.ModerationRequest{
			Model:  "openai/omni-moderation-latest",
			Inputs: []string{"I want to hurt someone."},
		},
		AccountID: "acme",
		KeyID:     "key-1",
		RequestID: "req-1",
		Protocol:  "openai",
	}
}

// TestAModerationTurnRecordsItsOperationWithoutInventingACost pins the meter's
// answer for a free operation. The record names the operation and the route,
// so activity shows the turn happened, and the cost stays nil under the
// no-usage reason rather than becoming a zero-dollar bill.
func TestAModerationTurnRecordsItsOperationWithoutInventingACost(t *testing.T) {
	snapshot, routeID := shippedModerationRoute(t)
	router := &moderationRouter{
		capturingRouter: &capturingRouter{},
		snapshot:        snapshot,
		routeID:         routeID,
	}
	repository := &recordingUsageRepository{}
	capture := NewUsageCapture(repository)
	service := capture.Wrap(&proxy{router: router})

	response, err := service.ProcessModerations(context.Background(), moderationTurn())
	require.NoError(t, err)
	require.Equal(t, 1, router.calls)
	require.Len(t, response.Response.Results, 1)

	capture.Flush()
	records := repository.all()
	require.Len(t, records, 1)
	record := records[0]

	require.Equal(t, usage.OperationModerations, record.Operation)
	require.Equal(t, routeID, record.ModelUsed)
	require.Equal(t, "openai", record.Provider)
	require.Nil(t, record.Cost)
	require.Equal(t, usage.CostReasonNoUsage, record.CostUnavailableReason)
}

// TestModerationValidationNamesTheEmptyInputByPosition holds the validator's
// refusal shape. A provider bills the request that carries an empty string,
// and a refusal that named only "input" would leave the caller searching a
// long list for which item to fix.
func TestModerationValidationNamesTheEmptyInputByPosition(t *testing.T) {
	turn := moderationTurn()
	turn.Request.Inputs = []string{"fine", "", "also fine"}

	err := ValidateModerationRequest(turn)
	var validation *ValidationError
	require.ErrorAs(t, err, &validation)
	require.Equal(t, "input[1]", validation.Field)

	require.NoError(t, ValidateModerationRequest(moderationTurn()))
}
