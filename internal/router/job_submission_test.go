package router

import (
	"context"
	"errors"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/execution"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/limits/admission"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/routing"
	"github.com/stretchr/testify/require"
)

type submissionPermission struct{ revoked bool }

func (p *submissionPermission) Check() error {
	if p.revoked {
		return errors.New("permission withdrawn")
	}
	return nil
}

func TestSubmissionRechecksPermissionAfterDurableWrite(t *testing.T) {
	permission := &submissionPermission{}
	ctx := inference.WithPermission(t.Context(), permission)
	calls := 0
	operation := providerCall[*connectors.JobSubmission, *connectors.ProviderJob, connectors.ProviderJob]{
		transport: func(connectors.Connector, catalogs.EndpointType) (providerInvoke[*connectors.JobSubmission, *connectors.ProviderJob], bool) {
			return func(context.Context, *connectors.JobSubmission) (*connectors.ProviderJob, error) {
				calls++
				return &connectors.ProviderJob{}, nil
			}, true
		},
		build:          func() *connectors.JobSubmission { return &connectors.JobSubmission{} },
		convert:        providerJobAnswer,
		beforeDispatch: func(context.Context, routing.Route, admission.Ticket) error { permission.revoked = true; return nil },
	}
	_, refusal, action := operation.attempt(routing.OperationVideosGenerations)(ctx, nil, routing.Route{}, credentialSelection{}, operationBudget{})
	require.NotNil(t, refusal)
	require.Equal(t, execution.AttemptActionStop, action)
	require.Zero(t, calls)
}
