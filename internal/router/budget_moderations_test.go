package router

import (
	"errors"
	"github.com/agentstation/starmap"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/limits/admission"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/routing"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestModerationChargeBoundsAndEvidence(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(client)
	require.NoError(t, err)
	snapshot := plane.Current()
	route := routing.Route{ProviderID: "openai", ProviderModelID: "omni-moderation-latest", CatalogGenerationID: snapshot.GenerationID()}
	request := &connectors.ModerationRequest{Inputs: []string{"one", "two"}}
	charge := moderationCharge(snapshot, route, request)
	quote, err := charge.quote(admission.Requirements{Spend: true})
	require.NoError(t, err)
	require.Nil(t, quote.Tokens)
	require.Equal(t, reservation.Quantities{"request": 1}, quote.Units)
	cost, err := quote.Valuation.NanoUSD(quote.Units)
	require.NoError(t, err)
	require.Zero(t, cost)
	_, err = charge.quote(admission.Requirements{Tokens: true, Spend: true})
	require.ErrorIs(t, err, admission.ErrBoundUnknown)
	evidence := charge.evidence(&connectors.ModerationResponse{}, nil)
	require.Equal(t, reservation.Quantities{"request": 1}, evidence.Quantities)
	require.Nil(t, charge.evidence(nil, nil))
	require.Nil(t, charge.evidence(&connectors.ModerationResponse{}, errors.New("ambiguous response")))
	route.CatalogGenerationID = "other"
	_, err = moderationCharge(snapshot, route, request).quote(admission.Requirements{Spend: true})
	require.ErrorIs(t, err, admission.ErrBoundUnknown)
	route.CatalogGenerationID = snapshot.GenerationID()
	route.ProviderModelID = "gpt-4o-mini"
	_, err = moderationCharge(snapshot, route, request).quote(admission.Requirements{Spend: true})
	require.ErrorIs(t, err, admission.ErrBoundUnknown)
	_, err = moderationCharge(nil, route, request).quote(admission.Requirements{Spend: true})
	require.ErrorIs(t, err, admission.ErrBoundUnknown)
}
