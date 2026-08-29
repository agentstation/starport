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

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/providers/keyring"
)

// credentialTestProvider is the catalog provider these tests apply credentials
// for. Its credential field is api-key, declared by the catalog, so a value
// stored through the HTTP surface is the same shape resolution expects.
const credentialTestProvider = "openai"

// credentialTestField is the catalog-declared field id for that provider.
const credentialTestField = "api-key"

// sharedCredentialValue and byokCredentialValue are the two secrets under
// test. They are distinct so a response body leaking either one names which
// plane leaked it.
const (
	sharedCredentialValue = "shared-plane-value-under-test"
	byokCredentialValue   = "account-plane-value-under-test"
)

// credentialGateway is a running gateway plus the three identities a
// credential test needs: an operator who holds admin and owns the deployment,
// and two accounts who own only their own BYOK.
type credentialGateway struct {
	server   *Server
	operator string
	acme     string
	globex   string
}

// newCredentialGateway builds that deployment. The operator holds admin and
// nothing else, because AON4's claim is that applying a deployment credential
// needs no gateway API key of the operator's own beyond the admin scope.
func newCredentialGateway(t *testing.T) *credentialGateway {
	t.Helper()

	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20})
	ctx := context.Background()
	for _, id := range []string{"acme", "globex"} {
		_, err := server.accounts.Create(ctx, account.Account{ID: id, Name: id, Active: true})
		require.NoError(t, err)
	}

	return &credentialGateway{
		server:   server,
		operator: mintCredentialKey(t, server, "operator", account.DefaultID, "admin"),
		acme:     mintCredentialKey(t, server, "acme-key", "acme", "provider_keys:read", "provider_keys:write"),
		globex:   mintCredentialKey(t, server, "globex-key", "globex", "provider_keys:read", "provider_keys:write"),
	}
}

// mintCredentialKey issues one active gateway API key in one account and
// returns its bearer token.
func mintCredentialKey(t *testing.T, server *Server, id, accountID string, scopes ...string) string {
	t.Helper()

	token := "token-" + id
	hash := sha256.Sum256([]byte(token))
	_, err := server.identities.Create(context.Background(), apikey.APIKey{
		ID:        id,
		Name:      strings.ReplaceAll(id, "-", "_"),
		Hash:      hex.EncodeToString(hash[:]),
		AccountID: accountID,
		Scopes:    scopes,
		Active:    true,
		CreatedAt: time.Now(),
	})
	require.NoError(t, err)
	return token
}

// call runs one authenticated request through the real route table and returns
// the recorder, so every assertion below is about the shipped surface.
func (g *credentialGateway) call(t *testing.T, token, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	var request *http.Request
	if body == "" {
		request = httptest.NewRequest(method, path, nil)
	} else {
		request = httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Authorization", "Bearer "+token)

	recorder := httptest.NewRecorder()
	g.server.router.ServeHTTP(recorder, request)
	return recorder
}

// credentialBody is the request body both surfaces accept.
func credentialBody(value string) string {
	return `{"credentials":{"` + credentialTestField + `":"` + value + `"}}`
}

// TestKeyNestedCredentialRoutesAreGone is the AON4 fail-before case. On the
// baseline a provider credential was addressed as a property of a gateway API
// key, which is the route shape that taught the conflation the campaign is
// removing. There is no alias: the paths are gone, not redirected.
func TestKeyNestedCredentialRoutesAreGone(t *testing.T) {
	gateway := newCredentialGateway(t)

	retired := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/keys/acme-key/provider-keys"},
		{http.MethodPost, "/api/v1/keys/acme-key/provider-keys"},
		{http.MethodGet, "/api/v1/keys/acme-key/provider-keys/" + credentialTestProvider},
		{http.MethodPut, "/api/v1/keys/acme-key/provider-keys/" + credentialTestProvider},
		{http.MethodDelete, "/api/v1/keys/acme-key/provider-keys/" + credentialTestProvider},
		{http.MethodPost, "/api/v1/keys/acme-key/provider-keys/" + credentialTestProvider + "/validate"},
		{http.MethodGet, "/api/v1/keys/acme-key/usage/provider-keys"},
		{http.MethodGet, "/api/v1/keys/acme-key/usage/comparison"},
	}

	for _, route := range retired {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			recorder := gateway.call(t, gateway.acme, route.method, route.path, credentialBody("unused"))
			require.Equal(t, http.StatusNotFound, recorder.Code, recorder.Body.String())

			// The route must be absent, not merely resolve to a missing
			// resource. A controller answering "provider key not found" on a
			// path that still exists would pass a bare status assertion while
			// leaving the conflation in the route table.
			assert.Contains(t, recorder.Body.String(), "The requested endpoint does not exist",
				"the path must be gone from the route table, not answered by a controller")
		})
	}
}

// TestOperatorAppliesASharedCredentialResolutionCanRead is the route half of
// the shared plane. AON3 proved resolution reads scope *; this proves the
// operator route writes the value resolution reads, so the two halves meet.
// Resolution runs as an anonymous caller because a credential applied through
// this route is open to every account by default.
func TestOperatorAppliesASharedCredentialResolutionCanRead(t *testing.T) {
	gateway := newCredentialGateway(t)
	path := "/api/v1/providers/" + credentialTestProvider + "/credentials"

	recorder := gateway.call(t, gateway.operator, http.MethodPost, path, credentialBody(sharedCredentialValue))
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	var created map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &created))
	credentialID, _ := created["id"].(string)
	require.NotEmpty(t, credentialID)

	material, err := gateway.server.providerKeys.ResolveSharedMaterial(
		context.Background(), "", catalogProvider(t, credentialTestProvider),
	)
	require.NoError(t, err, "the operator route must write where resolution reads")
	value, present := material.Value(credentialTestField)
	require.True(t, present)
	assert.Equal(t, sharedCredentialValue, value)

	// A rotation addresses the credential it rotates, so the operator states
	// which value changes rather than which happens to be first.
	recorder = gateway.call(t, gateway.operator, http.MethodPut, path+"/"+credentialID,
		credentialBody(sharedCredentialValue+"-rotated"))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	material, err = gateway.server.providerKeys.ResolveSharedMaterial(
		context.Background(), "", catalogProvider(t, credentialTestProvider),
	)
	require.NoError(t, err)
	value, present = material.Value(credentialTestField)
	require.True(t, present)
	assert.Equal(t, sharedCredentialValue+"-rotated", value)

	recorder = gateway.call(t, gateway.operator, http.MethodDelete, path+"/"+credentialID, "")
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	shared, err := gateway.server.providerKeys.GetSharedCredentials(context.Background(), credentialTestProvider)
	require.NoError(t, err)
	assert.Empty(t, shared)
}

// TestSharedCredentialRouteRefusesANonAdmin holds the boundary that makes the
// deployment credential the operator's. An account with full BYOK scopes still
// has no say over what the whole deployment spends.
func TestSharedCredentialRouteRefusesANonAdmin(t *testing.T) {
	gateway := newCredentialGateway(t)
	path := "/api/v1/providers/" + credentialTestProvider + "/credentials"

	for _, route := range []struct {
		method string
		suffix string
	}{
		{http.MethodGet, ""},
		{http.MethodPost, ""},
		{http.MethodGet, "/some-credential"},
		{http.MethodPut, "/some-credential"},
		{http.MethodDelete, "/some-credential"},
		{http.MethodPost, "/some-credential/validate"},
	} {
		t.Run(route.method+path+route.suffix, func(t *testing.T) {
			recorder := gateway.call(t, gateway.acme, route.method, path+route.suffix, credentialBody(sharedCredentialValue))
			assert.Equal(t, http.StatusForbidden, recorder.Code, recorder.Body.String())
		})
	}

	// The refusal must be a refusal, not a silent no-op that stored nothing.
	shared, err := gateway.server.providerKeys.GetSharedCredentials(context.Background(), credentialTestProvider)
	require.NoError(t, err)
	assert.Empty(t, shared)
}

// TestBYOKBelongsToItsAccount is the account half. An account reaches its own
// credentials and no others; an operator reaches any account's, because
// applying a credential on an account's behalf is a support operation an
// operator has to be able to perform.
func TestBYOKBelongsToItsAccount(t *testing.T) {
	gateway := newCredentialGateway(t)
	acmePath := "/api/v1/accounts/acme/byok"

	recorder := gateway.call(t, gateway.acme, http.MethodPut,
		acmePath+"/"+credentialTestProvider, credentialBody(byokCredentialValue))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	// The account's own key reads it back.
	recorder = gateway.call(t, gateway.acme, http.MethodGet, acmePath+"/"+credentialTestProvider, "")
	assert.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	// Another account does not, on any method.
	for _, route := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, acmePath},
		{http.MethodGet, acmePath + "/" + credentialTestProvider},
		{http.MethodPut, acmePath + "/" + credentialTestProvider},
		{http.MethodDelete, acmePath + "/" + credentialTestProvider},
	} {
		t.Run("globex "+route.method+" "+route.path, func(t *testing.T) {
			recorder := gateway.call(t, gateway.globex, route.method, route.path, credentialBody("intruder"))
			assert.Equal(t, http.StatusForbidden, recorder.Code, recorder.Body.String())
		})
	}

	// The intruder changed nothing.
	stored, err := gateway.server.providerKeys.ResolveStoredMaterial(
		context.Background(), keyring.AccountScope("acme"), catalogProvider(t, credentialTestProvider),
	)
	require.NoError(t, err)
	value, present := stored.Value(credentialTestField)
	require.True(t, present)
	assert.Equal(t, byokCredentialValue, value)

	// The operator reaches it, so a support operation is possible.
	recorder = gateway.call(t, gateway.operator, http.MethodGet, acmePath+"/"+credentialTestProvider, "")
	assert.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
}

// TestBYOKAndSharedCredentialsAreSeparateStores proves the two planes do not
// alias. Writing one must never be readable as the other, or the vocabulary is
// only a naming convention.
func TestBYOKAndSharedCredentialsAreSeparateStores(t *testing.T) {
	gateway := newCredentialGateway(t)

	recorder := gateway.call(t, gateway.acme, http.MethodPut,
		"/api/v1/accounts/acme/byok/"+credentialTestProvider, credentialBody(byokCredentialValue))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	// The shared plane is still an empty list.
	recorder = gateway.call(t, gateway.operator, http.MethodGet,
		"/api/v1/providers/"+credentialTestProvider+"/credentials", "")
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var listing struct {
		Count int `json:"count"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &listing))
	assert.Zero(t, listing.Count)

	// Creating the shared credential does not disturb the account's.
	recorder = gateway.call(t, gateway.operator, http.MethodPost,
		"/api/v1/providers/"+credentialTestProvider+"/credentials", credentialBody(sharedCredentialValue))
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())

	accountMaterial, err := gateway.server.providerKeys.ResolveStoredMaterial(
		context.Background(), keyring.AccountScope("acme"), catalogProvider(t, credentialTestProvider),
	)
	require.NoError(t, err)
	accountValue, present := accountMaterial.Value(credentialTestField)
	require.True(t, present)
	assert.Equal(t, byokCredentialValue, accountValue)

	sharedMaterial, err := gateway.server.providerKeys.ResolveSharedMaterial(
		context.Background(), "", catalogProvider(t, credentialTestProvider),
	)
	require.NoError(t, err)
	sharedValue, present := sharedMaterial.Value(credentialTestField)
	require.True(t, present)
	assert.Equal(t, sharedCredentialValue, sharedValue)
}

// TestNoCredentialResponseCarriesItsSecret extends the existing security
// property to the new paths. Every response on both surfaces is scanned, not
// only the ones a reviewer would expect to carry a value.
func TestNoCredentialResponseCarriesItsSecret(t *testing.T) {
	gateway := newCredentialGateway(t)
	providerPath := "/api/v1/providers/" + credentialTestProvider + "/credentials"
	byokPath := "/api/v1/accounts/acme/byok"

	createRecorder := gateway.call(t, gateway.operator, http.MethodPost, providerPath, credentialBody(sharedCredentialValue))
	require.Equal(t, http.StatusCreated, createRecorder.Code, createRecorder.Body.String())
	var created map[string]any
	require.NoError(t, json.Unmarshal(createRecorder.Body.Bytes(), &created))
	credentialID, _ := created["id"].(string)
	require.NotEmpty(t, credentialID)
	itemPath := providerPath + "/" + credentialID

	responses := []*httptest.ResponseRecorder{
		createRecorder,
		gateway.call(t, gateway.operator, http.MethodGet, providerPath, ""),
		gateway.call(t, gateway.operator, http.MethodGet, itemPath, ""),
		gateway.call(t, gateway.operator, http.MethodPut, itemPath, credentialBody(sharedCredentialValue)),
		gateway.call(t, gateway.operator, http.MethodPost, itemPath+"/validate", ""),
		gateway.call(t, gateway.acme, http.MethodPut, byokPath+"/"+credentialTestProvider, credentialBody(byokCredentialValue)),
		gateway.call(t, gateway.acme, http.MethodGet, byokPath, ""),
		gateway.call(t, gateway.acme, http.MethodGet, byokPath+"/"+credentialTestProvider, ""),
		gateway.call(t, gateway.acme, http.MethodPost, byokPath+"/"+credentialTestProvider+"/validate", ""),
	}

	for _, recorder := range responses {
		body := recorder.Body.String()
		assert.NotContains(t, body, sharedCredentialValue)
		assert.NotContains(t, body, byokCredentialValue)
	}

	// The read paths still report that a credential exists, or the surface
	// would be useless to an operator deciding whether to apply one.
	var detail map[string]any
	require.NoError(t, json.Unmarshal(responses[2].Body.Bytes(), &detail))
	assert.Equal(t, true, detail["has_credentials"])
}

// TestSharedCredentialListAndItemRoutes is the CSH-C2 fail-before case. The
// shared plane holds a list, so the admin surface must address it as one: the
// collection lists and creates, and each credential is reachable by its id.
// On the baseline the collection answered only for its first entry and no
// item path existed.
func TestSharedCredentialListAndItemRoutes(t *testing.T) {
	gateway := newCredentialGateway(t)
	collection := "/api/v1/providers/" + credentialTestProvider + "/credentials"

	// An empty plane is an empty collection, not a missing resource. "No
	// credential is stored" is an answer about the list, not a lookup failure.
	recorder := gateway.call(t, gateway.operator, http.MethodGet, collection, "")
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var listing struct {
		Credentials []map[string]any `json:"credentials"`
		Count       int              `json:"count"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &listing))
	assert.Zero(t, listing.Count)

	// A create with no access statement is open: the sharing default.
	recorder = gateway.call(t, gateway.operator, http.MethodPost, collection,
		`{"credentials":{"`+credentialTestField+`":"`+sharedCredentialValue+`"},"label":"team-a"}`)
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	var created map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &created))
	openID, _ := created["id"].(string)
	require.NotEmpty(t, openID, "a created credential must report the id that addresses it")
	assert.Equal(t, "open", created["access"])

	// A second create carries its access in the body.
	recorder = gateway.call(t, gateway.operator, http.MethodPost, collection,
		`{"credentials":{"`+credentialTestField+`":"`+sharedCredentialValue+`-granted"},"label":"team-b","access":"granted","grants":["acme"]}`)
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	var granted map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &granted))
	grantedID, _ := granted["id"].(string)
	require.NotEmpty(t, grantedID)
	assert.Equal(t, "granted", granted["access"])
	assert.Equal(t, []any{"acme"}, granted["grants"])

	// The collection lists both, in stored order, each naming its id.
	recorder = gateway.call(t, gateway.operator, http.MethodGet, collection, "")
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &listing))
	require.Equal(t, 2, listing.Count)
	assert.Equal(t, openID, listing.Credentials[0]["id"])
	assert.Equal(t, grantedID, listing.Credentials[1]["id"])

	// An item is read, validated, and updated by its id.
	recorder = gateway.call(t, gateway.operator, http.MethodGet, collection+"/"+grantedID, "")
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var item map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &item))
	assert.Equal(t, "team-b", item["label"])

	recorder = gateway.call(t, gateway.operator, http.MethodPost, collection+"/"+grantedID+"/validate", "")
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	recorder = gateway.call(t, gateway.operator, http.MethodPut, collection+"/"+grantedID,
		`{"grants":["acme","globex"]}`)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &item))
	assert.Equal(t, []any{"acme", "globex"}, item["grants"],
		"a grant update replaces the whole list")

	// An unknown id is a missing resource on every item verb.
	for _, route := range []struct {
		method string
		suffix string
	}{
		{http.MethodGet, ""},
		{http.MethodPut, ""},
		{http.MethodDelete, ""},
		{http.MethodPost, "/validate"},
	} {
		recorder = gateway.call(t, gateway.operator, route.method,
			collection+"/no-such-credential"+route.suffix, `{"label":"x"}`)
		assert.Equal(t, http.StatusNotFound, recorder.Code, route.method+" "+route.suffix+": "+recorder.Body.String())
	}

	// Deleting one credential leaves the other serving.
	recorder = gateway.call(t, gateway.operator, http.MethodDelete, collection+"/"+openID, "")
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	recorder = gateway.call(t, gateway.operator, http.MethodGet, collection, "")
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &listing))
	require.Equal(t, 1, listing.Count)
	assert.Equal(t, grantedID, listing.Credentials[0]["id"])
}

// TestSharedCredentialAccessInTheBodyReachesResolution proves the route body's
// access facts are the ones resolution obeys: a credential granted to one
// account through the HTTP surface serves that account and nobody else.
func TestSharedCredentialAccessInTheBodyReachesResolution(t *testing.T) {
	gateway := newCredentialGateway(t)
	collection := "/api/v1/providers/" + credentialTestProvider + "/credentials"

	recorder := gateway.call(t, gateway.operator, http.MethodPost, collection,
		`{"credentials":{"`+credentialTestField+`":"`+sharedCredentialValue+`"},"access":"granted","grants":["acme"]}`)
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())

	material, err := gateway.server.providerKeys.ResolveSharedMaterial(
		context.Background(), "acme", catalogProvider(t, credentialTestProvider),
	)
	require.NoError(t, err, "the grantee must resolve the granted credential")
	value, present := material.Value(credentialTestField)
	require.True(t, present)
	assert.Equal(t, sharedCredentialValue, value)

	_, err = gateway.server.providerKeys.ResolveSharedMaterial(
		context.Background(), "globex", catalogProvider(t, credentialTestProvider),
	)
	require.ErrorIs(t, err, keyring.ErrKeyNotFound, "a non-grantee never resolves it")

	// An access word outside the vocabulary is the caller's mistake.
	recorder = gateway.call(t, gateway.operator, http.MethodPost, collection,
		`{"credentials":{"`+credentialTestField+`":"x"},"access":"everyone"}`)
	assert.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
}

// catalogProvider reads one provider record from the same catalog the gateway
// composes with, so a test credential is validated by production rules.
func catalogProvider(t *testing.T, id string) catalogs.Provider {
	t.Helper()

	client, err := starmap.NewContext(context.Background())
	require.NoError(t, err)
	provider, err := client.CurrentCatalogState().Catalog.Provider(catalogs.ProviderID(id))
	require.NoError(t, err)
	return provider
}
