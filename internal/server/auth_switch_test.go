package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/authmode"
	"github.com/agentstation/starport/internal/server/controllers"
	"github.com/agentstation/starport/internal/storage"
)

// switchServer is a gateway the console could legitimately switch: it requires
// a key, nobody stated the mode for this process, and it binds loopback.
func switchServer(t *testing.T, store storage.KVStore) *Server {
	t.Helper()
	return newTestServer(t, &Config{
		Port: 8080, Host: "127.0.0.1", MaxRequestSize: 1 << 20,
	}, withTestStore(store))
}

// setMode sends one switch request the way the console does: from this
// machine, with an admin key, from a page served by this gateway.
func setMode(t *testing.T, server *Server, secret, mode string, options ...func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/admin/auth/mode",
		strings.NewReader(`{"mode":"`+mode+`"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://127.0.0.1:8080")
	request.RemoteAddr = "127.0.0.1:54321"
	if secret != "" {
		request.Header.Set("Authorization", "Bearer "+secret)
	}
	for _, option := range options {
		option(request)
	}
	recorder := httptest.NewRecorder()
	server.Router().ServeHTTP(recorder, request)
	return recorder
}

// keylessInference is the observation the whole task is about: can somebody
// with no credentials at all get an answer out of this gateway.
func keylessInference(t *testing.T, server *Server) int {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"mock/test-model","messages":[{"role":"user","content":"hello"}]}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.Router().ServeHTTP(recorder, request)
	return recorder.Code
}

// TestSwitchChangesInferenceWithoutARestart is the AON7 acceptance case. The
// middleware reads the running policy per request, so the next request after
// the switch already sees it.
func TestSwitchChangesInferenceWithoutARestart(t *testing.T) {
	server := switchServer(t, storage.NewMockStore())
	secret := createServerIdentity(t, server, "switch-admin", []string{"admin"})

	require.Equal(t, http.StatusUnauthorized, keylessInference(t, server))

	response := setMode(t, server, secret, "disabled")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var body controllers.AuthModeResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	assert.Equal(t, string(authmode.Disabled), body.Mode)
	assert.Equal(t, string(authmode.SourceConsole), body.Source)

	assert.Equal(t, http.StatusOK, keylessInference(t, server))

	// Re-enabling has to work from the open state too. An operator who can
	// only turn the lock off has a one-way switch.
	require.Equal(t, http.StatusOK, setMode(t, server, secret, "required").Code)
	assert.Equal(t, http.StatusUnauthorized, keylessInference(t, server))
}

// TestSwitchClosesAnOpenGatewayWithoutAKey is the one-way-door bug this task
// could have shipped. A gateway with authentication off issues no key that
// could carry the admin scope, so guarding the switch with that scope alone
// would let an operator open the gateway and never close it from the console
// again. Loopback is the guard that remains, and it is the strict one: the
// request has to come from the machine that runs the gateway.
func TestSwitchClosesAnOpenGatewayWithoutAKey(t *testing.T) {
	server := newTestServer(t, &Config{
		Port: 8080, Host: "127.0.0.1", MaxRequestSize: 1 << 20,
		AuthMode: authmode.Disabled,
	})
	require.Equal(t, http.StatusOK, keylessInference(t, server))

	// Nobody on the network may reach the switch, key or no key.
	remote := setMode(t, server, "", "required", func(r *http.Request) {
		r.RemoteAddr = "203.0.113.7:44321"
	})
	require.Equal(t, http.StatusForbidden, remote.Code, remote.Body.String())

	response := setMode(t, server, "", "required")

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, http.StatusUnauthorized, keylessInference(t, server))
	// Now that a key is required again, the switch is back inside the admin
	// plane and an anonymous caller cannot reopen the gateway.
	assert.Equal(t, http.StatusUnauthorized, setMode(t, server, "", "disabled").Code)
}

// TestSwitchSurvivesARestart proves the half a running policy cannot: what a
// second process reads. It replays the real startup resolution over the record
// the first process stored, because a switch a restart silently undoes is
// worse than a switch that was refused.
func TestSwitchSurvivesARestart(t *testing.T) {
	store := storage.NewMockStore()
	first := switchServer(t, store)
	secret := createServerIdentity(t, first, "switch-admin", []string{"admin"})
	require.Equal(t, http.StatusOK, setMode(t, first, secret, "disabled").Code)

	modes, err := authmode.Open(store)
	require.NoError(t, err)
	record, err := modes.Get(context.Background())
	require.NoError(t, err)
	// Nobody stated a mode for the second process, which is the case a stored
	// mode exists to serve.
	resolved := authmode.Resolve("", authmode.SourceUnset, record.Setting)
	require.Equal(t, authmode.Disabled, resolved.Mode)

	second := newTestServer(t, &Config{
		Port: 8080, Host: "127.0.0.1", MaxRequestSize: 1 << 20,
		AuthMode: resolved.Mode, AuthModeSource: resolved.Source,
	}, withTestStore(store))

	assert.Equal(t, http.StatusOK, keylessInference(t, second))
}

// TestSwitchRefusesANonAdminCaller keeps the switch inside the admin plane. A
// key scoped for inference must not be able to open the gateway to everyone.
func TestSwitchRefusesANonAdminCaller(t *testing.T) {
	server := switchServer(t, storage.NewMockStore())
	secret := createServerIdentity(t, server, "switch-user", []string{"chat:write"})

	response := setMode(t, server, secret, "disabled")

	require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	assert.Equal(t, http.StatusUnauthorized, keylessInference(t, server))
}

// TestSwitchRefusesARemoteOrigin is why holding admin is not enough. An
// operator whose key leaked should not have handed anyone on the network the
// power to turn the lock off, and a page on another site driving the
// operator's own browser is the same attack without the leak.
func TestSwitchRefusesARemoteOrigin(t *testing.T) {
	server := switchServer(t, storage.NewMockStore())
	secret := createServerIdentity(t, server, "switch-admin", []string{"admin"})

	remoteOrigin := setMode(t, server, secret, "disabled", func(r *http.Request) {
		r.Header.Set("Origin", "https://evil.example")
	})
	require.Equal(t, http.StatusForbidden, remoteOrigin.Code, remoteOrigin.Body.String())

	remoteCaller := setMode(t, server, secret, "disabled", func(r *http.Request) {
		r.RemoteAddr = "203.0.113.7:44321"
	})
	require.Equal(t, http.StatusForbidden, remoteCaller.Code, remoteCaller.Body.String())

	assert.Equal(t, http.StatusUnauthorized, keylessInference(t, server))
}

// TestSwitchRefusesToOpenAReachableGateway is the exposure tripwire at
// runtime. Startup refuses this combination, and the switch has to refuse the
// same one or the runtime path is a way around the startup check.
func TestSwitchRefusesToOpenAReachableGateway(t *testing.T) {
	server := newTestServer(t, &Config{
		Port: 8080, Host: "0.0.0.0", MaxRequestSize: 1 << 20,
	})
	secret := createServerIdentity(t, server, "switch-admin", []string{"admin"})

	refused := setMode(t, server, secret, "disabled")
	require.Equal(t, http.StatusConflict, refused.Code, refused.Body.String())
	assert.Equal(t, http.StatusUnauthorized, keylessInference(t, server))

	// Locking a reachable gateway is always allowed. The tripwire is about the
	// direction that opens it, not about the switch.
	require.Equal(t, http.StatusOK, setMode(t, server, secret, "required").Code)
}

// TestSwitchRefusesAModeStatedForThisProcess keeps the console from
// contradicting the operator. A flag or an environment variable is a statement
// about this process, and startup resolution honors it over anything stored,
// so accepting the change here would store a mode that never applies.
func TestSwitchRefusesAModeStatedForThisProcess(t *testing.T) {
	tests := []struct {
		name   string
		source authmode.Source
		want   string
	}{
		{name: "flag", source: authmode.SourceFlag, want: "command line flag"},
		{name: "config", source: authmode.SourceConfig, want: "STARPORT_SECURITY_AUTH_MODE"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newTestServer(t, &Config{
				Port: 8080, Host: "127.0.0.1", MaxRequestSize: 1 << 20,
				AuthMode: authmode.Required, AuthModeSource: test.source,
			})
			secret := createServerIdentity(t, server, "switch-admin", []string{"admin"})

			response := setMode(t, server, secret, "disabled")

			require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
			assert.Contains(t, response.Body.String(), test.want,
				"the refusal has to name the thing an operator would edit")
		})
	}
}

// TestSwitchRejectsAnUnknownMode keeps the switch from storing a mode nothing
// can read back. The repository refuses it too; refusing here is what turns it
// into a 400 an operator can act on rather than a 500.
func TestSwitchRejectsAnUnknownMode(t *testing.T) {
	server := switchServer(t, storage.NewMockStore())
	secret := createServerIdentity(t, server, "switch-admin", []string{"admin"})

	for _, mode := range []string{"", "off", "REQUIRED"} {
		response := setMode(t, server, secret, mode)
		require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
	}
}

// TestModeReadAnswersTheConsoleQuestion covers what the Settings control
// renders from. The console asks one unauthenticated question and gets back
// the mode, whether this caller may change it, and why not.
func TestModeReadAnswersTheConsoleQuestion(t *testing.T) {
	server := switchServer(t, storage.NewMockStore())

	local := httptest.NewRequest(http.MethodGet, "/api/v1/auth/mode", nil)
	local.RemoteAddr = "127.0.0.1:54321"
	local.Header.Set("Origin", "http://127.0.0.1:8080")
	recorder := httptest.NewRecorder()
	server.Router().ServeHTTP(recorder, local)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var body controllers.AuthModeResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	assert.Equal(t, string(authmode.Required), body.Mode)
	assert.True(t, body.CanChange)
	assert.Empty(t, body.Reason)

	remote := httptest.NewRequest(http.MethodGet, "/api/v1/auth/mode", nil)
	remote.RemoteAddr = "203.0.113.7:44321"
	remoteRecorder := httptest.NewRecorder()
	server.Router().ServeHTTP(remoteRecorder, remote)

	require.Equal(t, http.StatusOK, remoteRecorder.Code)
	var remoteBody controllers.AuthModeResponse
	require.NoError(t, json.Unmarshal(remoteRecorder.Body.Bytes(), &remoteBody))
	// The read and the write share one refusal, so a control the console
	// renders as available cannot be one the switch would reject.
	assert.False(t, remoteBody.CanChange)
	assert.NotEmpty(t, remoteBody.Reason)
}
