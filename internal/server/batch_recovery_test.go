package server

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/authorization"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/localauth"
	"github.com/agentstation/starport/internal/server/controllers"
	"github.com/agentstation/starport/internal/server/requestctx"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestBatchRestartUsesCurrentCallerPolicy(t *testing.T) {
	store := storage.NewMockStore()
	_, _, secret := authorizationFixture(t, store)
	clock := func() (time.Time, bool) { return time.Now(), true }
	middleware, keys, _ := cachedAuthFixture(t, store, clock)
	key, err := keys.GetByHash(t.Context(), hashSecret(secret))
	require.NoError(t, err)
	key.APIKey.Scopes = []string{"batches:write"}
	key, err = keys.Update(t.Context(), key.APIKey, key.Revision)
	require.NoError(t, err)
	var retained []byte
	request := httptest.NewRequest(http.MethodPost, "/v1/batches", nil)
	request.Header.Set("Authorization", "Bearer "+secret)
	middleware.RequireAPIKey(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		retained, err = requestctx.RetainBatchAuthorization(r.Context(), key.APIKey.AccountID, "batch-restart")
		require.NoError(t, err)
	})).ServeHTTP(httptest.NewRecorder(), request)
	require.NotEmpty(t, retained)
	require.NotContains(t, string(retained), secret)
	reopened, liveKeys, _ := cachedAuthFixture(t, store, clock)
	batch := jobs.Batch{ID: "batch-restart", Account: key.APIKey.AccountID, KeyID: key.APIKey.ID, Authorization: retained}
	admission := controllers.BatchAdmission{AccountID: batch.Account, KeyID: batch.KeyID, Reauthorize: func(ctx context.Context) (context.Context, error) { return reopened.resumeBatchCaller(ctx, batch) }}
	governor := batchGovernor{}
	_, err = governor.AdmitLine(t.Context(), admission)
	require.NoError(t, err)
	key.APIKey.Active = false
	_, err = liveKeys.Update(t.Context(), key.APIKey, key.Revision)
	require.NoError(t, err)
	_, err = governor.AdmitLine(t.Context(), admission)
	require.Error(t, err, "withdrawal survives restart and blocks another paid line")
	foreign := batch
	foreign.Account = "other"
	_, err = reopened.resumeBatchCaller(t.Context(), foreign)
	require.Error(t, err)
}

func TestBatchRestartSessionRemainsScopedAndExpiring(t *testing.T) {
	store := storage.NewMockStore()
	now := time.Now().UTC().Truncate(time.Millisecond)
	clock := func() (time.Time, bool) { return now, true }
	middleware, _, _ := cachedAuthFixture(t, store, clock)
	token, err := localauth.Mint(1, now)
	require.NoError(t, err)
	gate := localauth.NewGate(token, "127.0.0.1")
	middleware.AcceptSessions(gate)
	cookie, session, err := localauth.IssueSession(token, localauth.GrantLocalToken, now)
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, "/v1/batches", nil)
	request.AddCookie(&http.Cookie{Name: localauth.SessionCookie, Value: cookie})
	admitted, err := middleware.sessionContext(request)
	require.NoError(t, err)
	bundle, err := requestctx.Authorization(admitted)
	require.NoError(t, err)
	batch := jobs.Batch{ID: "session-batch", Account: bundle.Account().Account.ID, KeyID: bundle.Key().APIKey.ID}
	batch.Authorization, err = requestctx.RetainBatchAuthorization(admitted, batch.Account, batch.ID)
	require.NoError(t, err)
	require.NotContains(t, string(batch.Authorization), cookie)
	reopened, _, _ := cachedAuthFixture(t, store, clock)
	reopened.AcceptSessions(localauth.NewGate(token, "127.0.0.1"))
	_, err = reopened.resumeBatchCaller(t.Context(), batch)
	require.NoError(t, err)
	now = session.ExpiresAt
	_, err = reopened.resumeBatchCaller(t.Context(), batch)
	require.ErrorIs(t, err, localauth.ErrSessionExpired)
	now = session.IssuedAt
	other, err := localauth.Mint(2, now)
	require.NoError(t, err)
	reopened.AcceptSessions(localauth.NewGate(other, "127.0.0.1"))
	_, err = reopened.resumeBatchCaller(t.Context(), batch)
	require.ErrorIs(t, err, localauth.ErrBadSignature)
	forged, err := json.Marshal(retainedBatchCaller{Version: 1, Kind: "bearer", Account: batch.Account, Batch: batch.ID, Subject: authorization.OperatorSubject})
	require.NoError(t, err)
	batch.Authorization = forged
	_, err = reopened.resumeBatchCaller(t.Context(), batch)
	require.Error(t, err)
}
