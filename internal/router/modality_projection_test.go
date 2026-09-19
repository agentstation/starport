package router

import (
	"errors"
	"fmt"
	"testing"

	"github.com/agentstation/starport/internal/routing"
	"github.com/stretchr/testify/require"
)

// TestPlanningModalitiesCarryRequestNames proves the request side of the same
// boundary. The proxy derives modality names from the message content, and
// the planner receives them as its own type without a second translation.
func TestPlanningModalitiesCarryRequestNames(t *testing.T) {
	require.Equal(
		t,
		[]routing.Modality{routing.ModalityAudio, routing.ModalityDocument},
		planningModalities([]string{"audio", "document"}),
	)
	require.Nil(t, planningModalities(nil))
}

// TestRoutePlanFailureKeepsTheModalityRefusal holds the classification the
// caller depends on. Collapsing every planner failure onto ErrNoModelsAvailable
// turned a caller mistake into a 503, which told the caller to retry a request
// that can never succeed against that model.
func TestRoutePlanFailureKeepsTheModalityRefusal(t *testing.T) {
	refusal := fmt.Errorf(
		"%w: %w: openai/gpt-4o@openai: model does not read audio input",
		routing.ErrNoCandidate, routing.ErrModalityUnsupported,
	)

	mapped := routePlanFailure(refusal)
	require.ErrorIs(t, mapped, routing.ErrModalityUnsupported)
	require.Contains(t, mapped.Error(), "audio")

	mapped = routePlanFailure(fmt.Errorf("%w: 3 route(s) rejected", routing.ErrNoCandidate))
	require.ErrorIs(t, mapped, ErrNoModelsAvailable)
	require.NotErrorIs(t, mapped, routing.ErrModalityUnsupported)

	require.Nil(t, routePlanFailure(errors.New("snapshot unavailable")))
}
