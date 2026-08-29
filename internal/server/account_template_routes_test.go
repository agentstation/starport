package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/identity"
)

// newTemplateTestKey mints one active admin identity and returns its bearer
// token. Templates are an operator surface, so every route needs admin.
func newTemplateTestKey(t *testing.T, server *Server, id string) string {
	t.Helper()
	token := "test-" + id
	hash := sha256.Sum256([]byte(token))
	_, err := server.identities.Create(context.Background(), identity.APIKey{
		ID:        id,
		Name:      strings.ReplaceAll(id, "-", "_"),
		Hash:      hex.EncodeToString(hash[:]),
		Scopes:    []string{"*"},
		Active:    true,
		CreatedAt: time.Now(),
	})
	require.NoError(t, err)
	return token
}

func templateJSONRequest(method, path, token, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	return request
}

// TestAccountTemplateCRUDRoutes proves the template management surface:
// create, list, read, edit, and delete, with a duplicate create refused.
func TestAccountTemplateCRUDRoutes(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20})
	token := newTemplateTestKey(t, server, "template-admin")

	// Create a template that names creation defaults.
	recorder := httptest.NewRecorder()
	server.router.ServeHTTP(recorder, templateJSONRequest(http.MethodPost,
		"/api/v1/admin/account-templates", token,
		`{"id":"team-default","name":"Team default",`+
			`"credential_strategy":"byok_first",`+
			`"limits":{"requests":{"limit":60,"window_seconds":60}},`+
			`"byok_policy":{"mode":"selected","providers":["groq"]},`+
			`"access":[{"provider":"groq","models":["groq/compound"]}]}`))
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	var created struct {
		ID                 string `json:"id"`
		Name               string `json:"name"`
		CredentialStrategy string `json:"credential_strategy"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &created))
	require.Equal(t, "team-default", created.ID)
	require.Equal(t, "Team default", created.Name)
	require.Equal(t, "byok_first", created.CredentialStrategy)

	// A duplicate create conflicts instead of overwriting.
	recorder = httptest.NewRecorder()
	server.router.ServeHTTP(recorder, templateJSONRequest(http.MethodPost,
		"/api/v1/admin/account-templates", token, `{"id":"team-default"}`))
	require.Equal(t, http.StatusConflict, recorder.Code, recorder.Body.String())

	// The list holds the one template.
	recorder = httptest.NewRecorder()
	server.router.ServeHTTP(recorder, templateJSONRequest(http.MethodGet,
		"/api/v1/admin/account-templates", token, ""))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var listed struct {
		Templates []struct {
			ID string `json:"id"`
		} `json:"templates"`
		Count int `json:"count"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &listed))
	require.Equal(t, 1, listed.Count)
	require.Equal(t, "team-default", listed.Templates[0].ID)

	// Read one.
	recorder = httptest.NewRecorder()
	server.router.ServeHTTP(recorder, templateJSONRequest(http.MethodGet,
		"/api/v1/admin/account-templates/team-default", token, ""))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	// Edit the name and one default.
	recorder = httptest.NewRecorder()
	server.router.ServeHTTP(recorder, templateJSONRequest(http.MethodPut,
		"/api/v1/admin/account-templates/team-default", token,
		`{"name":"Team default v2","credential_strategy":"operator_first"}`))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var edited struct {
		Name               string `json:"name"`
		CredentialStrategy string `json:"credential_strategy"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &edited))
	require.Equal(t, "Team default v2", edited.Name)
	require.Equal(t, "operator_first", edited.CredentialStrategy)

	// Delete it, and a second read says gone.
	recorder = httptest.NewRecorder()
	server.router.ServeHTTP(recorder, templateJSONRequest(http.MethodDelete,
		"/api/v1/admin/account-templates/team-default", token, ""))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	recorder = httptest.NewRecorder()
	server.router.ServeHTTP(recorder, templateJSONRequest(http.MethodGet,
		"/api/v1/admin/account-templates/team-default", token, ""))
	require.Equal(t, http.StatusNotFound, recorder.Code, recorder.Body.String())
}

// TestAccountTemplateRoutesRefuseInvalid proves the refusals: a template
// with a policy outside the account contract, a read of a template that does
// not exist, and a create without an ID.
func TestAccountTemplateRoutesRefuseInvalid(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20})
	token := newTemplateTestKey(t, server, "template-refusals")

	recorder := httptest.NewRecorder()
	server.router.ServeHTTP(recorder, templateJSONRequest(http.MethodPost,
		"/api/v1/admin/account-templates", token, `{"name":"anonymous"}`))
	require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())

	// A selected BYOK policy with no providers is invalid on a template the
	// same way it is on an account.
	recorder = httptest.NewRecorder()
	server.router.ServeHTTP(recorder, templateJSONRequest(http.MethodPost,
		"/api/v1/admin/account-templates", token,
		`{"id":"broken","byok_policy":{"mode":"selected"}}`))
	require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())

	recorder = httptest.NewRecorder()
	server.router.ServeHTTP(recorder, templateJSONRequest(http.MethodGet,
		"/api/v1/admin/account-templates/absent", token, ""))
	require.Equal(t, http.StatusNotFound, recorder.Code, recorder.Body.String())
}

// TestAccountCreateFromTemplate proves the reason templates exist: creating
// an account from one stamps the template's defaults onto the account, an
// explicit field in the create request still wins, and editing the template
// afterward never rewrites the account it stamped.
func TestAccountCreateFromTemplate(t *testing.T) {
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20})
	token := newTemplateTestKey(t, server, "template-stamper")

	recorder := httptest.NewRecorder()
	server.router.ServeHTTP(recorder, templateJSONRequest(http.MethodPost,
		"/api/v1/admin/account-templates", token,
		`{"id":"org-default","credential_strategy":"byok_first",`+
			`"limits":{"requests":{"limit":60,"window_seconds":60}},`+
			`"byok_policy":{"mode":"selected","providers":["groq"]},`+
			`"access":[{"provider":"groq"}]}`))
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())

	// Create an account from the template. The explicit name wins over the
	// stamped defaults; everything unnamed comes from the template.
	recorder = httptest.NewRecorder()
	server.router.ServeHTTP(recorder, templateJSONRequest(http.MethodPost,
		"/api/v1/admin/accounts", token,
		`{"id":"acme","name":"Acme","template":"org-default"}`))
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())

	type accountBody struct {
		ID                 string `json:"id"`
		Name               string `json:"name"`
		CredentialStrategy string `json:"credential_strategy"`
		Limits             *struct {
			Requests *struct {
				Limit int64 `json:"limit"`
			} `json:"requests"`
		} `json:"limits"`
		BYOKPolicy *struct {
			Mode      string   `json:"mode"`
			Providers []string `json:"providers"`
		} `json:"byok_policy"`
		Access []struct {
			Provider string `json:"provider"`
		} `json:"access"`
	}
	var stamped accountBody
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &stamped))
	require.Equal(t, "Acme", stamped.Name)
	require.Equal(t, "byok_first", stamped.CredentialStrategy)
	require.NotNil(t, stamped.Limits)
	require.NotNil(t, stamped.Limits.Requests)
	require.Equal(t, int64(60), stamped.Limits.Requests.Limit)
	require.NotNil(t, stamped.BYOKPolicy)
	require.Equal(t, "selected", stamped.BYOKPolicy.Mode)
	require.Equal(t, []string{"groq"}, stamped.BYOKPolicy.Providers)
	require.Len(t, stamped.Access, 1)
	require.Equal(t, "groq", stamped.Access[0].Provider)

	// An explicit field in the create request overrides the template.
	recorder = httptest.NewRecorder()
	server.router.ServeHTTP(recorder, templateJSONRequest(http.MethodPost,
		"/api/v1/admin/accounts", token,
		`{"id":"acme-open","template":"org-default","credential_strategy":"operator_first"}`))
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	var overridden accountBody
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &overridden))
	require.Equal(t, "operator_first", overridden.CredentialStrategy)
	require.NotNil(t, overridden.Limits)

	// Edit the template. The stamped account must not move: a template names
	// creation defaults, not a live policy.
	recorder = httptest.NewRecorder()
	server.router.ServeHTTP(recorder, templateJSONRequest(http.MethodPut,
		"/api/v1/admin/account-templates/org-default", token,
		`{"credential_strategy":"operator_first","limits":{"requests":{"limit":5,"window_seconds":60}}}`))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	recorder = httptest.NewRecorder()
	server.router.ServeHTTP(recorder, templateJSONRequest(http.MethodGet,
		"/api/v1/admin/accounts/acme", token, ""))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var after accountBody
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &after))
	require.Equal(t, "byok_first", after.CredentialStrategy)
	require.NotNil(t, after.Limits)
	require.NotNil(t, after.Limits.Requests)
	require.Equal(t, int64(60), after.Limits.Requests.Limit)

	// A create naming a template that does not exist is refused, not
	// silently created without defaults.
	recorder = httptest.NewRecorder()
	server.router.ServeHTTP(recorder, templateJSONRequest(http.MethodPost,
		"/api/v1/admin/accounts", token, `{"id":"lost","template":"absent"}`))
	require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
}
