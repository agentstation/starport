package router

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/execution"
	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/providers/keyring"
	"github.com/agentstation/starport/internal/routing"
	"github.com/stretchr/testify/require"
)

func TestUserOnlySkipsOperatorResolution(t *testing.T) {
	route := routing.Route{ProviderID: "acme", ProviderModelID: "opaque/model", ModelID: "author/model"}
	messages := make([]string, 0, 2)
	for _, operatorErr := range []error{nil, errors.New("operator material exists")} {
		runtime := &credentialPolicyRuntime{operatorErr: operatorErr}
		policy, err := newCredentialPolicy(keyring.BYOKOnly, "tenant-a", runtime, nil, nil)
		require.NoError(t, err)
		_, providerFailure, action := policy.resolve(t.Context(), route)
		require.NotNil(t, providerFailure)
		require.Equal(t, keyring.UnavailableFailure("acme", nil).Kind(), providerFailure.Kind())
		require.Equal(t, execution.AttemptActionFallbackRoute, action)
		require.Zero(t, runtime.operatorCalls.Load())
		messages = append(messages, providerFailure.SafeMessage())
	}
	require.Equal(t, messages[0], messages[1])
}

func TestCredentialResolutionTerminalFailureStopsWithoutProviderHealth(t *testing.T) {
	runtime := &credentialPolicyRuntime{
		operatorErr: credentials.NewSourceError(credentials.SourceErrorDenied, "test"),
	}
	policy, err := newCredentialPolicy(keyring.OperatorFirst, "tenant-a", runtime, nil, nil)
	require.NoError(t, err)

	_, providerFailure, action := policy.resolve(t.Context(), routing.Route{
		ProviderID: "acme", ProviderModelID: "opaque/model", ModelID: "author/model",
	})
	require.NotNil(t, providerFailure)
	require.Equal(t, failure.Permission, providerFailure.Kind())
	require.Equal(t, execution.AttemptActionStop, action)
}

func TestCredentialPolicyPublishesExactSelectedMaterialVersion(t *testing.T) {
	runtime := &credentialPolicyRuntime{}
	policy, err := newCredentialPolicy(keyring.OperatorFirst, "tenant-a", runtime, nil, nil)
	require.NoError(t, err)
	route := routing.Route{
		CatalogGenerationID: "generation-1",
		ProviderID:          "acme", ProviderModelID: "opaque/model", ModelID: "author/model",
	}
	plan, err := routing.NewPlan(
		"generation-1", 1, []routing.Attempt{{Route: route}}, nil,
	)
	require.NoError(t, err)
	outcomes := &routerOutcomeCapture{}
	executor, err := execution.New(execution.DefaultConfig(), nil, nil, outcomes)
	require.NoError(t, err)
	_, err = executor.ExecuteChat(
		t.Context(),
		plan,
		func(ctx context.Context, attempt routing.Attempt) (*inference.ChatResponse, *failure.Failure, execution.AttemptAction) {
			selected, providerFailure, action := policy.resolve(ctx, attempt.Route)
			require.Nil(t, providerFailure)
			require.Equal(t, execution.AttemptActionDefault, action)
			execution.RecordCredentialAccepted(ctx)
			return &inference.ChatResponse{ID: selected.material.Version()}, nil, action
		},
	)
	require.NoError(t, err)
	require.Len(t, outcomes.outcomes, 1)
	require.Equal(t, execution.CredentialOwnerOperator, outcomes.outcomes[0].Credential.Owner)
	require.Equal(t, "test", outcomes.outcomes[0].Credential.MaterialVersion)
	require.True(t, outcomes.outcomes[0].Credential.Accepted)
}

func TestCredentialPolicySkipsRejectedOperatorMaterialVersion(t *testing.T) {
	runtime := &credentialPolicyRuntime{}
	gate := rejectingCredentialGate{providerID: "acme", version: "test"}
	policy, err := newCredentialPolicy(
		keyring.OperatorFirst, "tenant-a", runtime, nil, gate,
	)
	require.NoError(t, err)
	_, providerFailure, action := policy.resolve(t.Context(), routing.Route{
		ProviderID: "acme", ProviderModelID: "opaque/model", ModelID: "author/model",
	})
	require.NotNil(t, providerFailure)
	require.Equal(t, failure.Authentication, providerFailure.Kind())
	require.Equal(t, execution.AttemptActionFallbackRoute, action)
	require.Equal(t, int64(1), runtime.operatorCalls.Load())
}

type credentialPolicyRuntime struct {
	operatorErr   error
	operatorCalls atomic.Int64
}

type routerOutcomeCapture struct{ outcomes []execution.AttemptOutcome }

func (c *routerOutcomeCapture) PublishOutcome(outcome execution.AttemptOutcome) {
	c.outcomes = append(c.outcomes, outcome)
}

type rejectingCredentialGate struct {
	providerID string
	version    string
}

func (g rejectingCredentialGate) OperatorMaterialReady(providerID string, version string) bool {
	return providerID != g.providerID || version != g.version
}

func (*credentialPolicyRuntime) Snapshot() *runtimecatalog.RoutableSnapshot { return nil }
func (*credentialPolicyRuntime) Get(string) connectors.Connector            { return nil }
func (*credentialPolicyRuntime) RequiresAuthentication(string) bool         { return false }
func (r *credentialPolicyRuntime) ResolveMaterial(context.Context, string) (credentials.Material, error) {
	r.operatorCalls.Add(1)
	return routerTestMaterial(), r.operatorErr
}
func (*credentialPolicyRuntime) Release() {}
