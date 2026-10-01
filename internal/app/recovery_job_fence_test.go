package app

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/files"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/protocol/openai"
	"github.com/agentstation/starport/internal/providers"
	"github.com/agentstation/starport/internal/providers/keyring"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestRecoveryQueuedBatchLineRefusesDispatchAfterClosure(t *testing.T) {
	binary := populatedBinary(t)
	f := populatedAdoptionFixture(t)
	f.private = filepath.Join(t.TempDir(), "private")
	require.NoError(t, os.Mkdir(f.private, 0700))
	measurementConfigureListener(t, f)

	const queued = "queued-after-close"
	arrivals := make(chan string, jobs.DefaultBatchConcurrency+2)
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	var calls, callsAfterClose atomic.Int64
	var closed atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if closed.Load() {
			callsAfterClose.Add(1)
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer sk-test-key" {
			t.Error("unexpected fixture destination or credential")
			http.Error(w, "unexpected fixture request", http.StatusBadRequest)
			return
		}
		var request struct {
			Model    string `json:"model"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if json.UnmarshalRead(r.Body, &request) != nil || request.Model != "opaque/chat@001" || len(request.Messages) != 1 {
			t.Error("unexpected fixture inference request")
			http.Error(w, "unexpected fixture request", http.StatusBadRequest)
			return
		}
		select {
		case arrivals <- request.Messages[0].Content:
		default:
			t.Error("provider call count exceeded the fixture bound")
		}
		select {
		case <-release:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"fence-fixture","object":"chat.completion","model":"opaque/chat@001","choices":[{"index":0,"message":{"role":"assistant","content":"completed"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":3,"total_tokens":11}}`)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(upstream.Close)

	// Use the fixture's selected catalog and approve only the local destination.
	// The normal resolver, connector, router, and line governor remain in use.
	payload, err := os.ReadFile(f.cfg.Catalog.SourceURL)
	require.NoError(t, err)
	snapshot, err := catalogs.DecodeCatalogPayload(payload)
	require.NoError(t, err)
	provider, err := snapshot.Provider("acme")
	require.NoError(t, err)
	policy, err := providers.CompileDestinationPolicy(provider, string(keyring.SourceEnvironment), provider.Credentials.Inference.Alternatives[0], upstream.URL, nil)
	require.NoError(t, err)
	environment := maps.Clone(f.environment)
	environment["ACME_API_KEY"] = "sk-test-key"
	gatewayConfig, err := config.NewLoader().WithEnvironment(environment).Load(t.Context())
	require.NoError(t, err)
	gatewayConfig.Providers = config.ProvidersConfig{
		provider.ID: {BaseURL: upstream.URL, Timeout: time.Minute, MaxConnections: 8, Enabled: true},
	}
	gatewayConfig.InferenceDestinationApprovals, err = credentials.NewDestinationApprovals(nil, policy)
	require.NoError(t, err)
	gatewayConfig.Cache.Enabled = false

	store, err := storage.OpenValkey(f.cfg.RuntimeStorage().Valkey)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	accounts, err := account.Open(store)
	require.NoError(t, err)
	keys, err := apikey.Open(store)
	require.NoError(t, err)
	issuer, err := apikey.NewIssuer(keys, apikey.WithAccountChecker(accounts))
	require.NoError(t, err)
	issued, err := issuer.Issue(t.Context(), apikey.IssueRequest{
		Name: "job-fence", AccountID: populatedAccount, Scopes: []string{"batches:write", "chat:write"},
	})
	require.NoError(t, err)
	require.Nil(t, issued.APIKey.Limits)
	owner, err := accounts.GetByID(t.Context(), populatedAccount)
	require.NoError(t, err)
	require.Nil(t, owner.Account.Limits, "closure must stop dispatch without a budget operation forcing observation")

	application, err := New(gatewayConfig)
	require.NoError(t, err)
	runCtx, stop := context.WithCancel(context.WithoutCancel(t.Context()))
	done := make(chan error, 1)
	go func() { done <- application.Run(runCtx) }()
	t.Cleanup(func() {
		unblock()
		stop()
		select {
		case err := <-done:
			require.NoError(t, err, "running gateway did not stop cleanly")
		case <-time.After(30 * time.Second):
			t.Error("running gateway did not stop within its shutdown bound")
		}
	})
	base := fmt.Sprintf("http://127.0.0.1:%d", gatewayConfig.Server.Port)
	client := &http.Client{Timeout: 5 * time.Second}
	require.Eventually(t, func() bool {
		response, err := client.Get(base + "/health/ready")
		if err != nil {
			return false
		}
		_ = response.Body.Close()
		return response.StatusCode == http.StatusOK
	}, budgetFleetReadinessTimeout, 25*time.Millisecond)

	var input strings.Builder
	for index := range jobs.DefaultBatchConcurrency + 1 {
		id := fmt.Sprintf("held-%d", index)
		if index == jobs.DefaultBatchConcurrency {
			id = queued
		}
		line, err := json.Marshal(map[string]any{
			"custom_id": id, "method": "POST", "url": "/v1/chat/completions",
			"body": map[string]any{"model": "acme/opaque/chat@001", "max_tokens": 16,
				"messages": []map[string]string{{"role": "user", "content": id}}},
		})
		require.NoError(t, err)
		input.Write(line)
		input.WriteByte('\n')
	}
	file, err := application.files.Upload(t.Context(), files.UploadRequest{
		Account: populatedAccount, Filename: "fence.jsonl", Purpose: files.PurposeBatch, Size: int64(input.Len()),
	}, strings.NewReader(input.String()))
	require.NoError(t, err)
	body, err := json.Marshal(map[string]string{"input_file_id": file.ID, "endpoint": "/v1/chat/completions", "completion_window": "24h"})
	require.NoError(t, err)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, base+"/v1/batches", strings.NewReader(string(body)))
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+issued.Secret)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	require.True(t, err == nil, "batch submission HTTP request failed")
	body, err = io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	operatorPrivateJSON(t, f.private, "batch-submission.json", map[string][]byte{"body": body})
	require.Equal(t, http.StatusOK, response.StatusCode)
	var accepted openai.Batch
	require.NoError(t, json.Unmarshal(body, &accepted))
	require.NotEmpty(t, accepted.ID)

	seen := make(map[string]bool)
	for range jobs.DefaultBatchConcurrency {
		select {
		case id := <-arrivals:
			require.NotEqual(t, queued, id)
			require.False(t, seen[id], "each held line dispatches once")
			seen[id] = true
		case <-time.After(10 * time.Second):
			t.Fatal("batch worker did not reach the local provider before closure")
		}
	}
	batch, err := application.batches.Get(t.Context(), populatedAccount, accepted.ID)
	require.NoError(t, err)
	require.Equal(t, jobs.JobStateRunning, batch.State)
	require.Equal(t, jobs.DefaultBatchConcurrency+1, batch.TotalLines)
	require.Equal(t, jobs.DefaultBatchConcurrency, batch.ClaimedLines, "the last line still awaits a worker slot")
	require.EqualValues(t, jobs.DefaultBatchConcurrency, calls.Load())
	output, err := populatedBinaryRun(t, binary, f, "job-close", []string{"starport", "backup", "close", operatorJSONFlag})
	require.NoError(t, err, "shipping close failed, private output retained")
	closed.Store(true)
	var boundary recovery.Record
	require.NoError(t, json.Unmarshal(output, &boundary))
	require.False(t, boundary.Open)
	require.Equal(t, f.prior.Epoch+1, boundary.Epoch)
	require.NoError(t, store.Ping(t.Context()), "the old primary remains reachable")
	// Read only the retained flag. App.Run's observation worker performs the
	// independent approval check while all provider calls remain held.
	require.Eventually(t, func() bool { return application.recoveryAdmission() != nil },
		5*(recoveryObservationInterval+recoveryObservationTimeout), 25*time.Millisecond)
	require.Zero(t, callsAfterClose.Load())
	unblock()

	require.Eventually(t, func() bool {
		var err error
		batch, err = application.batches.Get(t.Context(), populatedAccount, accepted.ID)
		return err == nil && batch.RunFinished && batch.ResultsReleased
	}, 15*time.Second, 25*time.Millisecond)
	require.Equal(t, jobs.JobStateCompleted, batch.State)
	require.Equal(t, jobs.DefaultBatchConcurrency, batch.CompletedLines)
	require.Equal(t, 1, batch.FailedLines)
	require.EqualValues(t, jobs.DefaultBatchConcurrency, calls.Load(), "the queued line must never reach the provider")
	require.Zero(t, callsAfterClose.Load(), "closure forbids every later provider dispatch")
	require.NotEmpty(t, batch.ErrorFileID)
	_, reader, err := application.files.Open(t.Context(), populatedAccount, batch.ErrorFileID)
	require.NoError(t, err)
	defer func() { require.NoError(t, reader.Close()) }()
	body, err = io.ReadAll(reader)
	require.NoError(t, err)
	var refusal openai.BatchLineResponse
	require.NoError(t, json.Unmarshal(body, &refusal))
	require.Equal(t, queued, refusal.CustomID)
	require.NotNil(t, refusal.Response)
	require.Equal(t, http.StatusServiceUnavailable, refusal.Response.StatusCode)
	require.JSONEq(t, `{"error":{"message":"Batch authorization is unavailable.","type":"service_unavailable"}}`, string(refusal.Response.Body))
	select {
	case <-arrivals:
		t.Fatal("provider received an additional call after closure")
	default:
	}
}
