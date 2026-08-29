package controllers_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/providers/keyring"
	"github.com/agentstation/starport/internal/server/controllers"
	"github.com/agentstation/starport/internal/server/requestctx"
)

// TestCredentialStrategyRefusalsAreDistinguishable pins the two ways a request
// can name a strategy it may not run under. A value the gateway cannot parse is
// the caller's own mistake and reads 400. A value it understands but the
// operator withheld reads 403, so an account on byok_only learns the operator
// denied the credential rather than that it sent a malformed request.
func TestCredentialStrategyRefusalsAreDistinguishable(t *testing.T) {
	tests := []struct {
		name       string
		account    account.CredentialStrategy
		keyValue   any
		wantStatus int
		wantType   string
	}{
		{
			name:    "a byok_only account may not be widened by its key",
			account: account.StrategyBYOKOnly, keyValue: string(keyring.OperatorFirst),
			wantStatus: http.StatusForbidden, wantType: "permission_error",
		},
		{
			name:    "byok_first widens byok_only just as much",
			account: account.StrategyBYOKOnly, keyValue: string(keyring.BYOKFirst),
			wantStatus: http.StatusForbidden, wantType: "permission_error",
		},
		{
			name:    "an unparsable strategy stays a caller error",
			account: account.StrategyOperatorFirst, keyValue: "global_first",
			wantStatus: http.StatusBadRequest, wantType: "invalid_request_error",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &mockProxy{chat: chatFixture()}
			controller := controllers.NewChatController(service)
			recorder := httptest.NewRecorder()

			controller.Create(recorder, chatRequestWithStrategy(test.account, test.keyValue))

			require.Equal(t, test.wantStatus, recorder.Code)
			body := decodeJSON(t, recorder.Body.Bytes())
			errorBody, ok := body["error"].(map[string]any)
			require.True(t, ok, "the refusal must use the OpenAI error envelope")
			assert.Equal(t, test.wantType, errorBody["type"])
			assert.Nil(t, service.lastChat, "a refused request must never reach the proxy")
		})
	}
}

// TestKeyMayNarrowItsAccountStrategy is the other half of the rule: narrowing is
// always allowed, so an account can spend only its own credentials on one key
// while its other keys still use the operator's.
func TestKeyMayNarrowItsAccountStrategy(t *testing.T) {
	service := &mockProxy{chat: chatFixture()}
	controller := controllers.NewChatController(service)
	recorder := httptest.NewRecorder()

	controller.Create(recorder, chatRequestWithStrategy(
		account.StrategyOperatorFirst, string(keyring.BYOKOnly),
	))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotNil(t, service.lastChat)
	assert.Equal(t, keyring.BYOKOnly, service.lastChat.APIKeyConfig.CredentialStrategy)
}

// TestKeyWithoutStrategyInheritsTheAccountStrategy guards the case that a naive
// narrowing rule gets wrong: most keys carry no strategy metadata at all, and
// they must inherit the account's rather than be read as a request for the
// default and refused.
func TestKeyWithoutStrategyInheritsTheAccountStrategy(t *testing.T) {
	service := &mockProxy{chat: chatFixture()}
	controller := controllers.NewChatController(service)
	recorder := httptest.NewRecorder()

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", chatBody())
	ctx := requestctx.WithAPIKeyModel(request.Context(), &apikey.APIKey{})
	ctx = requestctx.WithAccountRecord(ctx, &account.Account{
		ID: "account-a", CredentialStrategy: account.StrategyBYOKOnly,
	})
	controller.Create(recorder, request.WithContext(ctx))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotNil(t, service.lastChat)
	assert.Equal(t, keyring.BYOKOnly, service.lastChat.APIKeyConfig.CredentialStrategy)
}

func chatRequestWithStrategy(
	accountStrategy account.CredentialStrategy,
	keyValue any,
) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", chatBody())
	ctx := requestctx.WithAPIKeyModel(request.Context(), &apikey.APIKey{
		Metadata: map[string]any{keyring.StrategyMetadataKey: keyValue},
	})
	ctx = requestctx.WithAccountRecord(ctx, &account.Account{
		ID: "account-a", CredentialStrategy: accountStrategy,
	})
	return request.WithContext(ctx)
}

func chatBody() *bytes.Buffer {
	return bytes.NewBufferString(
		`{"model":"openai/gpt-4.1","messages":[{"role":"user","content":"hello"}]}`,
	)
}
