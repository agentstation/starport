package server

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"github.com/agentstation/starport/internal/storage"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/registry"
	"github.com/stretchr/testify/require"
)

type batchPermissionSource struct {
	aliasHTTPSource
	allowed atomic.Bool
}

func (s *batchPermissionSource) AllowsCatalogAttempt(catalogs.CatalogAuthorityHead) bool {
	return s.allowed.Load()
}

type heldBatchConnector struct {
	*connectors.MockConnector
	entered chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (c *heldBatchConnector) Chat(ctx context.Context, request *connectors.ChatRequest) (*connectors.ChatResponse, error) {
	c.calls.Add(1)
	select {
	case c.entered <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case <-c.release:
		return c.MockConnector.Chat(ctx, request)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestQueuedBatchLineRechecksCatalogPermission(t *testing.T) {
	testQueuedBatchLineRechecksPermission(t, false)
}

func TestQueuedBatchLineRechecksKeyPermission(t *testing.T) {
	testQueuedBatchLineRechecksPermission(t, true)
}

func testQueuedBatchLineRechecksPermission(t *testing.T, revokeKey bool) {
	builder := catalogs.NewEmpty()
	author := catalogs.Author{ID: "author", Name: "Author"}
	require.NoError(t, builder.SetAuthor(author))
	features := &catalogs.ModelFeatures{Modalities: catalogs.ModelModalities{Input: []catalogs.ModelModality{catalogs.ModelModalityText}, Output: []catalogs.ModelModality{catalogs.ModelModalityText}}}
	require.NoError(t, builder.SetAuthorModel("author", catalogs.Model{ID: "current", Name: "Current", Authors: []catalogs.Author{author}, Features: features}))
	require.NoError(t, builder.SetProvider(catalogs.Provider{ID: "acme", Name: "Acme", Inference: &catalogs.ProviderInference{BaseURL: "https://provider.test/v1", Endpoints: []catalogs.ProviderInferenceEndpoint{{Operation: catalogs.ProviderOperationChatCompletions, Type: catalogs.EndpointTypeOpenAI, Path: "/chat/completions"}}}, Models: map[string]*catalogs.Model{"opaque/model@002": {ID: "opaque/model@002", ModelRef: "author/current", Limits: &catalogs.ModelLimits{ContextWindow: 4096}, Status: catalogs.ModelStatusActive, Features: features}}}))

	accepted, err := builder.Build()
	require.NoError(t, err)
	source := &batchPermissionSource{aliasHTTPSource: aliasHTTPSource{state: starmap.CatalogState{Catalog: accepted, GenerationID: "batch-accepted", Sequence: 1}}}
	source.allowed.Store(true)
	plane, err := runtimecatalog.Open(source)
	require.NoError(t, err)
	connector := &heldBatchConnector{MockConnector: connectors.NewMockConnector(connectors.ProviderConfig{}), entered: make(chan struct{}, jobs.DefaultBatchConcurrency), release: make(chan struct{})}
	release := sync.OnceFunc(func() { close(connector.release) })
	reg, err := registry.Open(plane, []registry.Registration{{Provider: "acme", Connector: connector, Operations: []catalogs.ProviderOperation{catalogs.ProviderOperationChatCompletions}, EndpointTypes: []catalogs.EndpointType{catalogs.EndpointTypeOpenAI}, Anonymous: credentials.NewMaterial(catalogs.ProviderCredentialProfile{ID: "none", Primitive: catalogs.ProviderAuthenticationNone}, nil, credentials.MaterialMetadata{})}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reg.Close()) })
	store := storage.NewMockStore()
	server := newTestServer(t, &Config{MaxRequestSize: 1 << 20}, withTestStore(store), func(config *testServerConfig) { config.runtimeRegistry = reg })
	useCachedBatchAuthorization(t, server, store)
	defer release()
	key := storeMediaTestKey(t, server, "permission-batch", "batches:write", "files:write", "files:read")
	var input strings.Builder
	for index := range jobs.DefaultBatchConcurrency + 1 {
		fmt.Fprintf(&input, `{"custom_id":"line-%d","method":"POST","url":"/v1/chat/completions","body":{"model":"author/current","messages":[{"role":"user","content":"hello"}]}}`+"\n", index)
	}
	fileID := uploadBatchTestFile(t, server, key, "batch", input.String())
	created := postBatchJSON(server, batchesPath, key, `{"input_file_id":"`+fileID+`","endpoint":"/v1/chat/completions","completion_window":"24h"}`)
	require.Equal(t, http.StatusOK, created.Code, created.Body.String())
	var submitted struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &submitted))
	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()
	for range jobs.DefaultBatchConcurrency {
		select {
		case <-connector.entered:
		case <-timeout.C:
			t.Fatal("batch workers did not enter the provider")
		}
	}
	readerKey := key
	if revokeKey {
		readerKey = storeMediaTestKey(t, server, "batch-reader", "batches:write", "files:read")
		record, err := server.apiKeys.GetByHash(t.Context(), hashSecret(key))
		require.NoError(t, err)
		record.APIKey.Active = false
		_, err = server.apiKeys.Update(t.Context(), record.APIKey, record.Revision)
		require.NoError(t, err)
	} else {
		source.allowed.Store(false)
	}
	release()
	var completed struct {
		Status      string `json:"status"`
		ErrorFileID string `json:"error_file_id"`
		Counts      struct {
			Total     int `json:"total"`
			Completed int `json:"completed"`
			Failed    int `json:"failed"`
		} `json:"request_counts"`
	}
	require.Eventually(t, func() bool {
		response := getBatchRequest(server, batchesPath+"/"+submitted.ID, readerKey)
		if response.Code != http.StatusOK {
			return false
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &completed))
		return completed.Status == "completed"
	}, 10*time.Second, time.Millisecond)
	require.Equal(t, jobs.DefaultBatchConcurrency+1, completed.Counts.Total)
	require.Equal(t, jobs.DefaultBatchConcurrency, completed.Counts.Completed)
	require.Equal(t, 1, completed.Counts.Failed)
	require.Equal(t, int32(jobs.DefaultBatchConcurrency), connector.calls.Load())
	response := getBatchRequest(server, "/v1/files/"+completed.ErrorFileID+"/content", readerKey)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var failure struct {
		CustomID string `json:"custom_id"`
		Response struct {
			StatusCode int `json:"status_code"`
		} `json:"response"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &failure))
	require.Equal(t, fmt.Sprintf("line-%d", jobs.DefaultBatchConcurrency), failure.CustomID)
	require.Equal(t, http.StatusServiceUnavailable, failure.Response.StatusCode)
	require.Same(t, accepted, plane.Current().Catalog())
}
