package proxy

import (
	"context"
	"testing"

	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/router"
	"github.com/stretchr/testify/require"
)

func TestOperationPreservesRequestIdentity(t *testing.T) {
	request := &OperationRequest[inference.ModerationRequest]{RequestID: "parent-guardrail", AccountID: "account"}
	var received *router.OperationRequest[inference.ModerationRequest]
	_, err := processOperation(t.Context(), request, "model", func(_ context.Context, call *router.OperationRequest[inference.ModerationRequest]) (*router.OperationResponse[inference.ModerationResponse], error) {
		received = call
		return &router.OperationResponse[inference.ModerationResponse]{}, nil
	})
	require.NoError(t, err)
	require.NotNil(t, received)
	require.Equal(t, request.RequestID, received.RequestID)
	require.Equal(t, request.AccountID, received.AccountID)
}
