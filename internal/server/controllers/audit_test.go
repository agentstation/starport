package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/audit"
	"github.com/agentstation/starport/internal/authmode"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/localauth"
	"github.com/agentstation/starport/internal/presets"
	"github.com/agentstation/starport/internal/server/requestctx"
	"github.com/agentstation/starport/internal/sqlstore"
	"github.com/agentstation/starport/internal/storage"
)

// recordingAuditRecorder collects what the controllers write, so a test reads
// the trail without a database behind it.
type recordingAuditRecorder struct {
	records []audit.Record
}

func (r *recordingAuditRecorder) Record(_ context.Context, record audit.Record) error {
	r.records = append(r.records, record)
	return nil
}

// actions lists what was recorded, in order, as "action outcome" pairs.
func (r *recordingAuditRecorder) actions() []string {
	actions := make([]string, len(r.records))
	for i, record := range r.records {
		actions[i] = record.Action + " " + record.Outcome
	}
	return actions
}

// fakeAuditReader serves one canned page and remembers the query it was
// asked, so a handler test proves the parameters travel.
type fakeAuditReader struct {
	page  audit.Page
	query audit.Query
	err   error
}

func (f *fakeAuditReader) List(_ context.Context, query audit.Query) (audit.Page, error) {
	f.query = query
	if f.err != nil {
		return audit.Page{}, f.err
	}
	return f.page, nil
}

// consoleContext wraps a request in the context a verified console session
// carries, the way sessionContext does for a real cookie.
func consoleContext(r *http.Request, grant localauth.GrantKind, subject string) *http.Request {
	return r.WithContext(requestctx.WithConsoleSession(r.Context(), string(grant), subject))
}

// TestKeyLifecycleRecordsTheConsoleActor is the plan's acceptance walk: a key
// created and deleted under a console session leaves two records that name
// the session's grant kind as the actor, never the synthetic operator key.
func TestKeyLifecycleRecordsTheConsoleActor(t *testing.T) {
	handler, _ := newAdminTestController(t)
	recorder := &recordingAuditRecorder{}
	handler.audit = recorder

	router := chi.NewRouter()
	router.Post("/keys", handler.CreateKey)
	router.Delete("/keys/{key_id}", handler.DeleteKey)

	create := httptest.NewRequest(http.MethodPost, "/keys",
		bytes.NewReader([]byte(`{"name":"audit-walk-key","scopes":["admin"]}`)))
	create = consoleContext(create, localauth.GrantLocalToken, "")
	created := httptest.NewRecorder()
	router.ServeHTTP(created, create)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())

	var body struct {
		Key struct {
			ID string `json:"id"`
		} `json:"key"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &body))
	require.NotEmpty(t, body.Key.ID)

	del := httptest.NewRequest(http.MethodDelete, "/keys/"+body.Key.ID, nil)
	del = consoleContext(del, localauth.GrantLocalToken, "")
	deleted := httptest.NewRecorder()
	router.ServeHTTP(deleted, del)
	require.Equal(t, http.StatusOK, deleted.Code, deleted.Body.String())

	require.Len(t, recorder.records, 2)
	assert.Equal(t, "key.create", recorder.records[0].Action)
	assert.Equal(t, body.Key.ID, recorder.records[0].Subject)
	assert.Equal(t, "key.delete", recorder.records[1].Action)
	assert.Equal(t, body.Key.ID, recorder.records[1].Subject)
	for _, record := range recorder.records {
		assert.Equal(t, "console:local-token", record.Actor)
		assert.Equal(t, audit.OutcomeOK, record.Outcome)
	}
}

// TestAuditActorNamesEachCaller pins the actor vocabulary: an identity
// session names its user, a machine-local session names its grant kind, a
// bearer key names itself, and a caller with none of those is anonymous.
func TestAuditActorNamesEachCaller(t *testing.T) {
	base := context.Background()

	identityCtx := requestctx.WithConsoleSession(base, string(localauth.GrantIdentity), "user-7")
	assert.Equal(t, "user:user-7", auditActor(identityCtx))

	ticketCtx := requestctx.WithConsoleSession(base, string(localauth.GrantTicket), "")
	assert.Equal(t, "console:ticket", auditActor(ticketCtx))

	namedKeyCtx := requestctx.WithAPIKeyModel(base, &apikey.APIKey{ID: "key_1", Name: "ci-deploy"})
	assert.Equal(t, "key:ci-deploy", auditActor(namedKeyCtx))

	bareKeyCtx := requestctx.WithAPIKeyID(base, "key_2")
	assert.Equal(t, "key:key_2", auditActor(bareKeyCtx))

	assert.Equal(t, audit.ActorAnonymous, auditActor(base))
}

// auditWalkStep drives one mutation route and names the record it must leave.
type auditWalkStep struct {
	method string
	path   string
	body   string
	status int
	action string
}

// runAuditWalk plays the steps against the router and asserts the walk left
// exactly the expected actions on the trail, in order, all with outcome ok.
func runAuditWalk(t *testing.T, router http.Handler, recorder *recordingAuditRecorder, steps []auditWalkStep) {
	t.Helper()
	start := len(recorder.records)
	expected := make([]string, len(steps))
	for i, step := range steps {
		request := httptest.NewRequest(step.method, step.path, bytes.NewReader([]byte(step.body)))
		request.RemoteAddr = "127.0.0.1:34567"
		request = consoleContext(request, localauth.GrantLocalToken, "")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, step.status, response.Code,
			"%s %s: %s", step.method, step.path, response.Body.String())
		expected[i] = step.action + " " + audit.OutcomeOK
	}
	assert.Equal(t, expected, recorder.actions()[start:])
}

// TestMutationHandlersWriteTheAuditTrail walks every audited admin mutation
// surface and proves each handler leaves its named record. Reads, validates,
// and refusals that never reach a store appear nowhere in the trail.
func TestMutationHandlersWriteTheAuditTrail(t *testing.T) {
	t.Run("accounts", func(t *testing.T) {
		controller, _, _ := newAccountsTestController(t)
		recorder := &recordingAuditRecorder{}
		controller.audit = recorder

		router := chi.NewRouter()
		router.Post("/accounts", controller.Create)
		router.Put("/accounts/{account_id}", controller.Update)
		router.Delete("/accounts/{account_id}", controller.Delete)

		runAuditWalk(t, router, recorder, []auditWalkStep{
			{http.MethodPost, "/accounts", `{"id":"walk"}`, http.StatusCreated, "account.create"},
			{http.MethodPut, "/accounts/walk", `{"name":"renamed"}`, http.StatusOK, "account.update"},
			{http.MethodDelete, "/accounts/walk", "", http.StatusOK, "account.delete"},
		})
	})

	t.Run("account templates", func(t *testing.T) {
		db, err := sqlstore.Open(sqlstore.Config{Type: sqlstore.TypeSQLite})
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		require.NoError(t, db.Migrate(context.Background()))
		templates, err := account.OpenTemplates(db)
		require.NoError(t, err)

		controller := NewAccountTemplatesController(templates)
		recorder := &recordingAuditRecorder{}
		controller.audit = recorder

		router := chi.NewRouter()
		router.Post("/account-templates", controller.Create)
		router.Put("/account-templates/{template_id}", controller.Update)
		router.Delete("/account-templates/{template_id}", controller.Delete)

		runAuditWalk(t, router, recorder, []auditWalkStep{
			{http.MethodPost, "/account-templates", `{"id":"walk"}`, http.StatusCreated, "template.create"},
			{http.MethodPut, "/account-templates/walk", `{"name":"renamed"}`, http.StatusOK, "template.update"},
			{http.MethodDelete, "/account-templates/walk", "", http.StatusOK, "template.delete"},
		})
	})

	t.Run("members", func(t *testing.T) {
		controller, repositories := newMembersTestController(t)
		recorder := &recordingAuditRecorder{}
		controller.audit = recorder

		user, err := repositories.Users.Create(context.Background(),
			identity.User{ID: "user-walk", Subject: "test:user-walk"})
		require.NoError(t, err)

		router := membersTestRouter(controller)

		create := httptest.NewRequest(http.MethodPost, "/teams",
			bytes.NewReader([]byte(`{"name":"walk team"}`)))
		create = consoleContext(create, localauth.GrantLocalToken, "")
		created := httptest.NewRecorder()
		router.ServeHTTP(created, create)
		require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
		var team identity.Team
		require.NoError(t, json.Unmarshal(created.Body.Bytes(), &team))
		require.Equal(t, []string{"team.create " + audit.OutcomeOK}, recorder.actions())

		grantQuery := "?account_id=" + account.DefaultID + "&user_id=" + user.User.ID
		runAuditWalk(t, router, recorder, []auditWalkStep{
			{http.MethodPut, "/teams/" + team.ID + "/members/" + user.User.ID, "",
				http.StatusCreated, "team_member.add"},
			{http.MethodDelete, "/teams/" + team.ID + "/members/" + user.User.ID, "",
				http.StatusOK, "team_member.remove"},
			{http.MethodPost, "/account-grants",
				`{"account_id":"` + account.DefaultID + `","user_id":"` + user.User.ID + `"}`,
				http.StatusCreated, "grant.create"},
			{http.MethodDelete, "/account-grants" + grantQuery, "",
				http.StatusOK, "grant.delete"},
			{http.MethodDelete, "/teams/" + team.ID, "",
				http.StatusOK, "team.delete"},
		})
	})

	t.Run("provider credentials", func(t *testing.T) {
		controller := NewProviderCredentialsController(&mockKeyManager{}, nil)
		recorder := &recordingAuditRecorder{}
		controller.audit = recorder

		router := chi.NewRouter()
		router.Route("/providers/{provider}/credentials", func(r chi.Router) {
			r.Post("/", controller.SharedCreate)
			r.Put("/{credential_id}", controller.SharedUpdate)
			r.Delete("/{credential_id}", controller.SharedDelete)
		})
		router.Route("/accounts/{account_id}/byok", func(r chi.Router) {
			r.Put("/{provider}", controller.BYOKPut)
			r.Delete("/{provider}", controller.BYOKDelete)
		})

		runAuditWalk(t, router, recorder, []auditWalkStep{
			{http.MethodPost, "/providers/openai/credentials",
				`{"credentials":{"api-key":"sk-test"}}`, http.StatusCreated, "credential.create"},
			{http.MethodPut, "/providers/openai/credentials/shared-1",
				`{"label":"renamed"}`, http.StatusOK, "credential.update"},
			{http.MethodDelete, "/providers/openai/credentials/shared-1", "",
				http.StatusOK, "credential.delete"},
			{http.MethodPut, "/accounts/acct_1/byok/openai",
				`{"credentials":{"api-key":"sk-test"}}`, http.StatusOK, "byok.put"},
			{http.MethodDelete, "/accounts/acct_1/byok/openai", "",
				http.StatusOK, "byok.delete"},
		})
	})

	t.Run("presets", func(t *testing.T) {
		repository, err := presets.Open(storage.NewMockStore())
		require.NoError(t, err)
		controller := NewPresetsController(repository)
		recorder := &recordingAuditRecorder{}
		controller.audit = recorder

		router := chi.NewRouter()
		router.Post("/presets", controller.Create)
		router.Delete("/presets/{name}", controller.Delete)

		runAuditWalk(t, router, recorder, []auditWalkStep{
			{http.MethodPost, "/presets",
				`{"name":"walk","config":{"model":"openai/gpt-4o"}}`, http.StatusCreated, "preset.create"},
			{http.MethodDelete, "/presets/walk", "", http.StatusNoContent, "preset.delete"},
		})
	})

	t.Run("auth mode", func(t *testing.T) {
		store, err := authmode.Open(storage.NewMockStore())
		require.NoError(t, err)
		policy := authmode.NewPolicy(authmode.Setting{
			Mode:   authmode.Required,
			Source: authmode.SourceDefault,
		})
		controller := NewAuthController(policy, store, "127.0.0.1", false)
		recorder := &recordingAuditRecorder{}
		controller.audit = recorder

		router := chi.NewRouter()
		router.Put("/auth/mode", controller.SetMode)

		runAuditWalk(t, router, recorder, []auditWalkStep{
			{http.MethodPut, "/auth/mode", `{"mode":"required"}`, http.StatusOK, "auth_mode.update"},
		})
	})

	t.Run("keys", func(t *testing.T) {
		handler, repository := newAdminTestController(t)
		recorder := &recordingAuditRecorder{}
		handler.audit = recorder
		createAdminTestAPIKey(t, repository, apikey.APIKey{ID: "key_walk", Name: "walk", Active: true})

		router := chi.NewRouter()
		router.Put("/keys/{key_id}", handler.UpdateKey)

		runAuditWalk(t, router, recorder, []auditWalkStep{
			{http.MethodPut, "/keys/key_walk", `{"name":"renamed"}`, http.StatusOK, "key.update"},
		})
	})
}

// TestAuditListDegradesWithoutTrail states what a deployment without the
// relational store answers: 503 with the reason, never an empty page.
func TestAuditListDegradesWithoutTrail(t *testing.T) {
	controller := NewAuditController(nil)
	response := httptest.NewRecorder()
	controller.List(response, httptest.NewRequest(http.MethodGet, "/audit", nil))
	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	assert.Contains(t, response.Body.String(), "audit trail is not configured")
}

// TestAuditListServesThePage proves the filters travel to the repository and
// the page comes back in the shared list envelope.
func TestAuditListServesThePage(t *testing.T) {
	reader := &fakeAuditReader{page: audit.Page{
		Records:    []audit.Record{{ID: 9, Actor: "console:local-token", Action: "key.create", Subject: "key_9", Outcome: audit.OutcomeOK}},
		NextCursor: "9",
	}}
	controller := NewAuditController(reader)

	request := httptest.NewRequest(http.MethodGet,
		"/audit?action=key.create&actor=console:local-token&limit=1&cursor=42", nil)
	response := httptest.NewRecorder()
	controller.List(response, request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	assert.Equal(t, "key.create", reader.query.Action)
	assert.Equal(t, "console:local-token", reader.query.Actor)
	assert.Equal(t, 1, reader.query.Limit)
	assert.Equal(t, "42", reader.query.Cursor)

	var body struct {
		Data       []audit.Record `json:"data"`
		NextCursor string         `json:"next_cursor"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Len(t, body.Data, 1)
	assert.Equal(t, "key.create", body.Data[0].Action)
	assert.Equal(t, "9", body.NextCursor)
}

// TestAuditListRejectsABadQuery covers both refusal seams: a parameter the
// handler cannot read and a cursor the repository cannot.
func TestAuditListRejectsABadQuery(t *testing.T) {
	reader := &fakeAuditReader{}
	controller := NewAuditController(reader)

	response := httptest.NewRecorder()
	controller.List(response, httptest.NewRequest(http.MethodGet, "/audit?limit=zero", nil))
	require.Equal(t, http.StatusBadRequest, response.Code)

	reader.err = fmt.Errorf("%w: bad cursor", audit.ErrInvalidQuery)
	response = httptest.NewRecorder()
	controller.List(response, httptest.NewRequest(http.MethodGet, "/audit?cursor=nope", nil))
	require.Equal(t, http.StatusBadRequest, response.Code)
}
