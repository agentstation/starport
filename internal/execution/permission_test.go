package execution

import (
	"context"
	"errors"
	"testing"

	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/routing"
	"github.com/stretchr/testify/require"
)

type testRequestPermission struct{ revoked bool }

func (p *testRequestPermission) Check() error {
	if p.revoked {
		return errors.New("withdrawn")
	}
	return nil
}

func TestRequestPermissionStopsRetryAndFallback(t *testing.T) {
	for _, retry := range []bool{false, true} {
		t.Run(map[bool]string{false: "fallback", true: "retry"}[retry], func(t *testing.T) {
			config := testConfig()
			if !retry {
				config.MaxRetriesPerRoute = 0
			}
			executor := newTestExecutor(t, newFakeClock(), config)
			permission := &testRequestPermission{}
			ctx := inference.WithPermission(t.Context(), permission)
			calls := 0
			_, err := executor.ExecuteChat(ctx, testPlan(t, "provider-a/model", "provider-b/model"), func(_ context.Context, attempt routing.Attempt) (*inference.ChatResponse, *failure.Failure, AttemptAction) {
				calls++
				permission.revoked = true
				return nil, retryableFailure(attempt.Route.ProviderID), AttemptActionDefault
			})
			require.Error(t, err)
			require.Equal(t, 1, calls)
			var refused *failure.Failure
			require.ErrorAs(t, err, &refused)
			require.Equal(t, failure.GatewayUnavailable, refused.Kind())
		})
	}
}
