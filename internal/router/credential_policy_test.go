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
	"github.com/agentstation/starport/internal/providers/byok"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/routing"
	"github.com/stretchr/testify/require"
)

func TestUserOnlyCredentialPolicyDoesNotProbeOperatorMaterial(t *testing.T) {
	route := routing.Route{ProviderID: "acme", ProviderModelID: "opaque/model", ModelID: "author/model"}
	messages := make([]string, 0, 2)
	for _, operatorErr := range []error{nil, errors.New("operator material exists")} {
		runtime := &credentialPolicyRuntime{operatorErr: operatorErr}
		policy, err := newCredentialPolicy(byok.UserOnly, "tenant-a", runtime, nil)
		require.NoError(t, err)
		_, providerFailure, action := policy.resolve(t.Context(), route)
		require.NotNil(t, providerFailure)
		require.Equal(t, byok.UnavailableFailure("acme", nil).Kind(), providerFailure.Kind())
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
	policy, err := newCredentialPolicy(byok.OperatorFirst, "tenant-a", runtime, nil)
	require.NoError(t, err)

	_, providerFailure, action := policy.resolve(t.Context(), routing.Route{
		ProviderID: "acme", ProviderModelID: "opaque/model", ModelID: "author/model",
	})
	require.NotNil(t, providerFailure)
	require.Equal(t, failure.Permission, providerFailure.Kind())
	require.Equal(t, execution.AttemptActionStop, action)
}

type credentialPolicyRuntime struct {
	operatorErr   error
	operatorCalls atomic.Int64
}

func (*credentialPolicyRuntime) Snapshot() *runtimecatalog.RoutableSnapshot { return nil }
func (*credentialPolicyRuntime) Get(string) connectors.Connector            { return nil }
func (r *credentialPolicyRuntime) ResolveMaterial(context.Context, string) (credentials.Material, error) {
	r.operatorCalls.Add(1)
	return routerTestMaterial(), r.operatorErr
}
func (*credentialPolicyRuntime) Release() {}
