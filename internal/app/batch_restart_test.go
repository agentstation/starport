package app

import (
	"context"
	"encoding/json/v2"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/files"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/stretchr/testify/require"
)

func TestProductionBatchRestart(t *testing.T) {
	for _, revoked := range []bool{false, true} {
		name := "resume"
		if revoked {
			name = "withdrawn"
		}
		t.Run(name, func(t *testing.T) {
			release := make(chan struct{})
			free := sync.OnceFunc(func() { close(release) })
			defer free()
			var arrived atomic.Int64
			budgets := &limits.Limits{Tokens: &limits.Budget{Limit: 2_000_000, Interval: limits.IntervalDay}, Spend: &limits.Budget{Limit: 1_000_000_000, Interval: limits.IntervalDay}}
			fixture := newPerformanceFixtureWithAdmission(t, 0, nil, true, budgets, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				arrived.Add(1)
				<-release
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"restart","object":"chat.completion","model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":3,"total_tokens":11,"prompt_tokens_details":{"cached_tokens":0}}}`)
			}))
			line := `{"custom_id":"line","method":"POST","url":"/v1/chat/completions","body":{"model":"openai/gpt-4o-mini","max_tokens":32,"messages":[{"role":"user","content":"Hello"}]}}`
			input := strings.Repeat(line+"\n", 5)
			file, err := fixture.application.files.Upload(t.Context(), files.UploadRequest{Account: "default", Filename: "restart.jsonl", Purpose: files.PurposeBatch, Size: int64(len(input))}, strings.NewReader(input))
			require.NoError(t, err)
			payload, err := json.Marshal(map[string]string{"input_file_id": file.ID, "endpoint": "/v1/chat/completions", "completion_window": "24h"})
			require.NoError(t, err)
			request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, fixture.gateway.URL+"/v1/batches", strings.NewReader(string(payload)))
			require.NoError(t, err)
			request.Header.Set("Authorization", "Bearer "+performanceGatewayKey)
			request.Header.Set("Content-Type", "application/json")
			response, err := fixture.client.Do(request)
			require.NoError(t, err)
			raw, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.NoError(t, response.Body.Close())
			require.Equal(t, http.StatusOK, response.StatusCode, string(raw))
			var accepted struct {
				ID string `json:"id"`
			}
			require.NoError(t, json.Unmarshal(raw, &accepted))
			require.Eventually(t, func() bool { return arrived.Load() == 4 }, 10*time.Second, 5*time.Millisecond)
			stop, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
			require.ErrorIs(t, fixture.application.batches.Close(stop), context.DeadlineExceeded)
			cancel()
			free()
			require.NoError(t, fixture.application.batches.Close(t.Context()))
			before, err := fixture.application.batches.Get(t.Context(), "default", accepted.ID)
			require.NoError(t, err)
			require.Equal(t, 4, before.ClaimedLines)
			require.False(t, before.RunFinished)
			require.NotEmpty(t, before.Authorization)
			cfg := fixture.application.config
			require.NoError(t, fixture.application.Close(t.Context()))
			reopened, err := New(cfg)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, reopened.Close(context.Background())) })
			if revoked {
				keys, err := apikey.Open(reopened.store)
				require.NoError(t, err)
				key, err := keys.GetByID(t.Context(), before.KeyID)
				require.NoError(t, err)
				key.APIKey.Active = false
				_, err = keys.Update(t.Context(), key.APIKey, key.Revision)
				require.NoError(t, err)
			}
			reopened.sweepJobAssets(t.Context())
			after, err := reopened.batches.Get(t.Context(), "default", accepted.ID)
			require.NoError(t, err)
			count := 5
			if revoked {
				count = 4
				require.False(t, after.RunFinished)
				require.Equal(t, 4, after.ClaimedLines, "withdrawal cannot consume an untouched line claim")
			} else {
				require.True(t, after.RunFinished)
				require.True(t, after.ResultsReleased)
				require.Equal(t, jobs.JobStateCompleted, after.State)
				require.Equal(t, count, after.CompletedLines)
			}
			require.EqualValues(t, count, arrived.Load())
			keys, err := reopened.store.ScanWithPrefix(t.Context(), "budget:v1:attempt:", 20)
			require.NoError(t, err)
			require.Len(t, keys, count)
			for _, key := range keys {
				data, err := reopened.store.Get(t.Context(), key)
				require.NoError(t, err)
				var record reservation.Record
				require.NoError(t, json.Unmarshal(data, &record))
				require.Equal(t, reservation.Settled, record.State)
			}
			reopened.sweepJobAssets(t.Context())
			require.EqualValues(t, count, arrived.Load())
		})
	}
}
