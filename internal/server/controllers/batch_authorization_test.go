package controllers

import (
	"context"
	"testing"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/proxy"
	"github.com/agentstation/starport/internal/server/requestctx"
	"github.com/stretchr/testify/require"
)

type currentBatchGovernor struct{ current context.Context }

func (g currentBatchGovernor) AdmitLine(context.Context, BatchAdmission) (context.Context, error) {
	return g.current, nil
}

type batchProjectionProxy struct {
	proxy.Proxy
	request    *proxy.ChatCompletionRequest
	permission inference.Permission
}

func (p *batchProjectionProxy) ProcessChatCompletion(ctx context.Context, request *proxy.ChatCompletionRequest) (*proxy.ChatCompletionResponse, error) {
	p.request = request
	p.permission = inference.RequestPermission(ctx)
	return &proxy.ChatCompletionResponse{Response: inference.ChatResponse{Model: "current/model"}}, nil
}

type batchTestPermission struct{}

func (*batchTestPermission) Check() error { return nil }

func TestBatchLineUsesCurrentRoutingPolicyAndPermit(t *testing.T) {
	key := &apikey.APIKey{ID: "current-key", AccountID: "current-account", TeamID: "current-team", AllowedModels: []string{"current/model"}, Active: true}
	owner := &account.Account{ID: "current-account", Active: true, CredentialStrategy: account.StrategyBYOKOnly}
	ctx := requestctx.WithAPIKeyModel(t.Context(), key)
	ctx = requestctx.WithAPIKeyID(ctx, key.ID)
	ctx = requestctx.WithAccountRecord(ctx, owner)
	ctx = requestctx.WithAccountID(ctx, owner.ID)
	permission := &batchTestPermission{}
	ctx = inference.WithPermission(ctx, permission)
	service := &batchProjectionProxy{}
	runner := &batchLineRunner{service: service, governor: currentBatchGovernor{current: ctx}, endpoint: "/v1/chat/completions", batchID: "batch-test", accountID: "old-account", keyID: "old-key", teamID: "old-team", apiKeyConfig: &proxy.APIKeyRoutingConfig{AllowedModels: []string{"old/model"}}}
	_, failed := runner.RunLine(t.Context(), 0, []byte(`{"custom_id":"line-1","method":"POST","url":"/v1/chat/completions","body":{"model":"current/model","messages":[{"role":"user","content":"hello"}]}}`))
	require.False(t, failed)
	require.NotNil(t, service.request)
	require.Equal(t, key.ID, service.request.KeyID)
	require.Equal(t, owner.ID, service.request.AccountID)
	require.Equal(t, key.TeamID, service.request.TeamID)
	require.Equal(t, key.AllowedModels, service.request.APIKeyConfig.AllowedModels)
	require.Same(t, permission, service.permission)
	require.Empty(t, service.request.APIKey)
	require.Equal(t, []string{"old/model"}, runner.apiKeyConfig.AllowedModels)
}
