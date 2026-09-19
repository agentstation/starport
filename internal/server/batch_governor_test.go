package server

import (
	"context"
	"errors"
	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/authorization"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/server/requestctx"
	"github.com/agentstation/starport/internal/storage"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/ratelimit"
	"github.com/agentstation/starport/internal/server/controllers"
	"github.com/stretchr/testify/require"
)

func TestBatchBudgetUnknownRequiredStateRefuses(t *testing.T) {
	budget := &limits.Limits{Spend: &limits.Budget{Limit: 10, Interval: limits.IntervalDay}}
	for _, tc := range []struct {
		name      string
		governor  batchGovernor
		admission controllers.BatchAdmission
	}{
		{name: "missing usage", admission: controllers.BatchAdmission{KeyLimits: budget}},
		{name: "unavailable usage", governor: batchGovernor{usage: stubUsageTotals{err: errors.New("unavailable")}}, admission: controllers.BatchAdmission{AccountLimits: budget}},
		{name: "missing team reader", admission: controllers.BatchAdmission{TeamID: "platform"}},
		{name: "unavailable team", governor: batchGovernor{teamBudget: func(context.Context, string) (*limits.TeamBudget, error) { return nil, errors.New("unavailable") }}, admission: controllers.BatchAdmission{TeamID: "platform"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.governor.admitBudget(t.Context(), tc.admission)
			var refused *failure.Failure
			require.ErrorAs(t, err, &refused)
			require.Equal(t, failure.GatewayUnavailable, refused.Kind())
		})
	}
}

func TestBatchBudgetConfirmedAbsenceNeedsNoUsageReader(t *testing.T) {
	governor := &batchGovernor{}
	require.NoError(t, governor.admitBudget(t.Context(), controllers.BatchAdmission{}))
}

func TestBatchGovernorRejectsMissingAuthorization(t *testing.T) {
	governor := &batchGovernor{}
	_, err := governor.AdmitLine(t.Context(), controllers.BatchAdmission{})
	require.Error(t, err)
}

func useCachedBatchAuthorization(t *testing.T, server *Server, store storage.KVStore) {
	t.Helper()
	clock := func() (time.Time, bool) { return time.Now(), true }
	middleware, keys, accounts := cachedAuthFixture(t, store, clock)
	server.apiKeys, server.accounts = keys, accounts
	server.auth.UseAuthorization(middleware.authorization, clock)
}

func queuedAdmissionFixture(t *testing.T) (*batchGovernor, controllers.BatchAdmission, apikey.Repository, account.Repository) {
	t.Helper()
	store := storage.NewMockStore()
	_, _, secret := authorizationFixture(t, store)
	middleware, keys, accounts := cachedAuthFixture(t, store, func() (time.Time, bool) { return time.Now(), true })
	key, err := keys.GetByHash(t.Context(), hashSecret(secret))
	require.NoError(t, err)
	key.APIKey.Scopes = []string{"batches:write"}
	_, err = keys.Update(t.Context(), key.APIKey, key.Revision)
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, "/v1/batches", nil)
	request.Header.Set("Authorization", "Bearer "+secret)
	var admitted context.Context
	middleware.RequireAPIKey(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { admitted = r.Context(); w.WriteHeader(http.StatusOK) })).ServeHTTP(httptest.NewRecorder(), request)
	require.NotNil(t, admitted)
	admission := controllers.BatchAdmission{AccountID: key.APIKey.AccountID, KeyID: key.APIKey.ID, Reauthorize: requestctx.GetAuthorizationRefresh(admitted)}
	return &batchGovernor{}, admission, keys, accounts
}

func TestQueuedAdmissionUsesCurrentPolicy(t *testing.T) {
	governor, admission, keys, accounts := queuedAdmissionFixture(t)
	first, err := governor.AdmitLine(t.Context(), admission)
	require.NoError(t, err)
	key, err := keys.GetByID(t.Context(), admission.KeyID)
	require.NoError(t, err)
	key.APIKey.AllowedModels = []string{"allowed/current"}
	_, err = keys.Update(t.Context(), key.APIKey, key.Revision)
	require.NoError(t, err)
	owner, err := accounts.GetByID(t.Context(), admission.AccountID)
	require.NoError(t, err)
	owner.Account.CredentialStrategy = account.StrategyOperatorFirst
	_, err = accounts.Update(t.Context(), owner.Account, owner.Revision)
	require.NoError(t, err)
	require.ErrorIs(t, inference.CheckPermission(first), authorization.ErrWithdrawn)
	next, err := governor.AdmitLine(t.Context(), admission)
	require.NoError(t, err)
	current, ok := requestctx.GetAPIKeyModel(next)
	require.True(t, ok)
	require.Equal(t, []string{"allowed/current"}, current.AllowedModels)
	require.Equal(t, account.StrategyOperatorFirst, requestctx.AccountCredentialStrategyOrDefault(next))
	require.NoError(t, inference.CheckPermission(next))
	require.ErrorIs(t, inference.CheckPermission(first), authorization.ErrWithdrawn)
}

func TestQueuedAdmissionRejectsChangedOwnershipOrScope(t *testing.T) {
	for _, change := range []string{"disabled", "scope", "account", "missing", "budget"} {
		t.Run(change, func(t *testing.T) {
			governor, admission, keys, _ := queuedAdmissionFixture(t)
			key, err := keys.GetByID(t.Context(), admission.KeyID)
			require.NoError(t, err)
			switch change {
			case "disabled":
				key.APIKey.Active = false
			case "scope":
				key.APIKey.Scopes = []string{"chat:write"}
			case "account":
				key.APIKey.AccountID = account.DefaultID
			case "budget":
				key.APIKey.Limits = &limits.Limits{Spend: &limits.Budget{Limit: 1, Interval: limits.IntervalDay}}
			case "missing":
				require.NoError(t, keys.Delete(t.Context(), key.APIKey.ID, key.Revision))
			}
			if change != "missing" {
				_, err = keys.Update(t.Context(), key.APIKey, key.Revision)
				require.NoError(t, err)
			}
			_, err = governor.AdmitLine(t.Context(), admission)
			require.Error(t, err)
		})
	}
}

type mutatingBatchRate struct {
	mutate func()
	calls  int
}

func (r *mutatingBatchRate) Consume(context.Context, string, int64, time.Duration) (ratelimit.Decision, error) {
	r.calls++
	r.mutate()
	return ratelimit.Decision{Allowed: true}, nil
}

func TestQueuedAdmissionRechecksAfterRateAdmission(t *testing.T) {
	governor, admission, keys, _ := queuedAdmissionFixture(t)
	key, err := keys.GetByID(t.Context(), admission.KeyID)
	require.NoError(t, err)
	key.APIKey.Limits = &limits.Limits{Requests: &limits.RequestLimit{Limit: 10, WindowSeconds: 1}}
	key, err = keys.Update(t.Context(), key.APIKey, key.Revision)
	require.NoError(t, err)
	rate := &mutatingBatchRate{mutate: func() {
		key.APIKey.Active = false
		_, err := keys.Update(t.Context(), key.APIKey, key.Revision)
		require.NoError(t, err)
	}}
	governor.rateLimits = rate
	_, err = governor.AdmitLine(t.Context(), admission)
	require.Error(t, err)
	require.Equal(t, 1, rate.calls)
}
