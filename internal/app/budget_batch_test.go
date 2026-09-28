package app

import (
	"encoding/json/v2"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/files"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/stretchr/testify/require"
)

func TestProductionBatchBudget(t *testing.T) {
	for _, endpoint := range []string{"/v1/chat/completions", "/v1/responses", "/v1/embeddings"} {
		for _, mode := range []string{"measured", "missing usage", "insufficient capacity"} {
			t.Run(endpoint+"/"+mode, func(t *testing.T) {
				embeddings := endpoint == "/v1/embeddings"
				tokenLimit := int64(400_000)
				if mode == "missing usage" {
					tokenLimit = 200_000
					if embeddings {
						tokenLimit = 10_000
					}
				}
				if mode == "insufficient capacity" {
					tokenLimit = 1
				}
				fixture := newPerformanceFixtureForOperation(t, 0, nil, true,
					&limits.Limits{Tokens: &limits.Budget{Limit: tokenLimit, Interval: limits.IntervalDay}, Spend: &limits.Budget{Limit: 100_000_000, Interval: limits.IntervalDay}},
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						if embeddings {
							require.Equal(t, "/v1/embeddings", r.URL.Path)
							usage := `,"usage":{"prompt_tokens":8,"total_tokens":8}`
							if mode == "missing usage" {
								usage = ""
							}
							_, _ = io.WriteString(w, `{"model":"text-embedding-3-small","data":[{"index":0,"embedding":[0.1,0.2]}]`+usage+`}`)
							return
						}
						require.Equal(t, "/v1/chat/completions", r.URL.Path)
						usage := `,"usage":{"prompt_tokens":8,"completion_tokens":3,"total_tokens":11,"prompt_tokens_details":{"cached_tokens":0}}`
						if mode == "missing usage" {
							usage = ""
						}
						_, _ = io.WriteString(w, `{"model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]`+usage+`}`)
					}), []string{"/v1/chat/completions", "/v1/embeddings"})
				body := map[string]any{"model": "openai/gpt-4o-mini", "max_tokens": 32, "messages": []map[string]string{{"role": "user", "content": "hello"}}}
				if endpoint == "/v1/responses" {
					body = map[string]any{"model": "openai/gpt-4o-mini", "max_output_tokens": 32, "input": "hello"}
				} else if embeddings {
					body = map[string]any{"model": "openai/text-embedding-3-small", "input": "hello"}
				}
				var input strings.Builder
				for _, id := range []string{"first", "second"} {
					line, err := json.Marshal(map[string]any{"custom_id": id, "method": "POST", "url": endpoint, "body": body})
					require.NoError(t, err)
					input.Write(line)
					input.WriteByte('\n')
				}
				file, err := fixture.application.files.Upload(t.Context(), files.UploadRequest{Account: "default", Filename: "budget.jsonl", Purpose: files.PurposeBatch, Size: int64(input.Len())}, strings.NewReader(input.String()))
				require.NoError(t, err)
				payload, err := json.Marshal(map[string]string{"input_file_id": file.ID, "endpoint": endpoint, "completion_window": "24h"})
				require.NoError(t, err)
				req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, fixture.gateway.URL+"/v1/batches", strings.NewReader(string(payload)))
				require.NoError(t, err)
				req.Header.Set("Authorization", "Bearer "+performanceGatewayKey)
				req.Header.Set("Content-Type", "application/json")
				response, err := fixture.client.Do(req)
				require.NoError(t, err)
				raw, err := io.ReadAll(response.Body)
				require.NoError(t, err)
				require.NoError(t, response.Body.Close())
				require.Equal(t, http.StatusOK, response.StatusCode, string(raw))
				var accepted struct {
					ID string `json:"id"`
				}
				require.NoError(t, json.Unmarshal(raw, &accepted))
				require.NotEmpty(t, accepted.ID)
				var batch jobs.Batch
				require.Eventually(t, func() bool {
					var err error
					batch, err = fixture.application.batches.Get(t.Context(), "default", accepted.ID)
					return err == nil && batch.RunFinished && batch.ResultsReleased
				}, 10*time.Second, 10*time.Millisecond)
				require.Equal(t, jobs.JobStateCompleted, batch.State)
				require.Equal(t, 2, batch.TotalLines)
				wantCalls := 2
				if mode == "missing usage" {
					wantCalls = 1
				} else if mode == "insufficient capacity" {
					wantCalls = 0
				}
				require.EqualValues(t, wantCalls, fixture.calls.Load())
				require.Equal(t, wantCalls, batch.CompletedLines)
				require.Equal(t, 2-wantCalls, batch.FailedLines)
				keys, err := fixture.application.store.ScanWithPrefix(t.Context(), "budget:v1:attempt:", 10)
				require.NoError(t, err)
				require.Len(t, keys, wantCalls)
				lineStore, err := jobs.OpenBatchRepository(fixture.application.store)
				require.NoError(t, err)
				requestIDs := make(map[string]bool)
				for number := 1; number <= 2; number++ {
					line, err := lineStore.ReadLine(t.Context(), "default", batch.ID, number)
					require.NoError(t, err)
					require.True(t, line.ResultReady)
					require.False(t, requestIDs[line.RequestID])
					requestIDs[line.RequestID] = true
				}
				for _, key := range keys {
					data, err := fixture.application.store.Get(t.Context(), key)
					require.NoError(t, err)
					var record reservation.Record
					require.NoError(t, json.Unmarshal(data, &record))
					require.True(t, requestIDs[record.Attempt.RequestID])
					require.Equal(t, "default", record.Attempt.AccountID)
					require.Equal(t, "STARPORT_TEST", record.Attempt.KeyID)
					require.Len(t, record.Bindings, 2)
					if mode == "missing usage" {
						require.Equal(t, reservation.Uncertain, record.State)
						require.Nil(t, record.Evidence)
					} else {
						require.Equal(t, reservation.Settled, record.State)
						cost, tokens := int64(3000), int64(11)
						if embeddings {
							cost, tokens = 160, 8
						}
						require.Equal(t, cost, *record.NanoUSD)
						require.Equal(t, tokens, record.Evidence.Tokens)
					}
				}
				if batch.ErrorFileID != "" {
					_, content, err := fixture.application.files.Open(t.Context(), "default", batch.ErrorFileID)
					require.NoError(t, err)
					data, err := io.ReadAll(content)
					require.NoError(t, err)
					require.NoError(t, content.Close())
					require.Equal(t, 2-wantCalls, strings.Count(string(data), `"status_code":402`), string(data))
				}
			})
		}
	}
}
