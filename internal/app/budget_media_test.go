package app

import (
	"bytes"
	"encoding/json/v2"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/stretchr/testify/require"
)

func TestProductionImagesBudget(t *testing.T) {
	for _, tc := range []struct {
		name, model, size, response string
		n                           int
		limit, cost                 int64
		noBudget, tokens, truncated bool
		upstream, status            int
		calls                       int64
		state                       reservation.State
	}{
		{name: "flat images", model: "FLUX-1.1-pro", size: "1024x1024", n: 2, limit: 80000000, cost: 80000000, status: 200, calls: 1, state: reservation.Settled},
		{name: "scaled pixels", model: "FLUX-1-schnell", size: "1024x1536", n: 2, limit: 1500000, cost: 1500000, status: 200, calls: 1, state: reservation.Settled},
		{name: "omitted defaults", model: "FLUX-1-schnell", limit: 500000, cost: 500000, response: `{"data":[{"b64_json":"aW1hZ2U="}]}`, status: 200, calls: 1, state: reservation.Settled},
		{name: "fewer returned images", model: "FLUX-1.1-pro", n: 2, limit: 80000000, cost: 40000000, response: `{"data":[{"b64_json":"aW1hZ2U="}]}`, status: 200, calls: 1, state: reservation.Settled},
		{name: "empty item retains full bound", model: "FLUX-1.1-pro", n: 2, limit: 80000000, cost: 80000000, response: `{"data":[{}]}`, status: 500, calls: 1, state: reservation.Uncertain},
		{name: "no items retains full bound", model: "FLUX-1.1-pro", n: 2, limit: 80000000, cost: 80000000, response: `{"data":[]}`, status: 500, calls: 1, state: reservation.Uncertain},
		{name: "truncated response", model: "FLUX-1.1-pro", n: 2, limit: 80000000, cost: 80000000, truncated: true, status: 500, calls: 1, state: reservation.Uncertain},
		{name: "trailing response", model: "FLUX-1.1-pro", n: 2, limit: 80000000, cost: 80000000, response: `{"data":[{"b64_json":"aW1hZ2U="}]} {}`, status: 500, calls: 1, state: reservation.Uncertain},
		{name: "provider failure", model: "FLUX-1.1-pro", n: 2, limit: 80000000, cost: 80000000, upstream: 500, status: 500, calls: 1, state: reservation.Uncertain},
		{name: "insufficient capacity", model: "FLUX-1.1-pro", n: 2, limit: 79999999, status: 402},
		{name: "unknown tokens", model: "FLUX-1.1-pro", n: 2, limit: 100000000, tokens: true, status: 503},
		{name: "no budget", model: "FLUX-1.1-pro", n: 2, noBudget: true, status: 200, calls: 1},
		{name: "unknown dimensions", model: "FLUX-1-schnell", size: "auto", n: 2, limit: 100000000, status: 503},
		{name: "overflow dimensions", model: "FLUX-1-schnell", size: "9223372036854775807x2", n: 2, limit: 100000000, status: 503},
		{name: "unqualified unit", model: "FLUX-1-dev", size: "1024x1024", n: 2, limit: 100000000, status: 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			budget := &limits.Limits{Spend: &limits.Budget{Limit: tc.limit, Interval: limits.IntervalDay}}
			if tc.tokens {
				budget = &limits.Limits{Tokens: &limits.Budget{Limit: tc.limit, Interval: limits.IntervalDay}}
			}
			if tc.noBudget {
				budget = nil
			}
			model := "black-forest-labs/" + tc.model
			fixture := newPerformanceFixtureForProviders(t, 0, nil, true, budget, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var wire struct {
					Model string `json:"model"`
					Size  string `json:"size"`
					N     int    `json:"n"`
				}
				require.NoError(t, json.UnmarshalRead(r.Body, &wire))
				require.Equal(t, model, wire.Model)
				require.Equal(t, tc.size, wire.Size)
				require.Equal(t, tc.n, wire.N)
				w.Header().Set("Content-Type", "application/json")
				if tc.truncated {
					w.Header().Set("Content-Length", "10000")
				}
				upstream := tc.upstream
				if upstream == 0 {
					upstream = 200
				}
				w.WriteHeader(upstream)
				response := tc.response
				if response == "" {
					response = `{"data":[{"b64_json":"aW1hZ2U="},{"b64_json":"aW1hZ2U="}]}`
				}
				_, _ = io.WriteString(w, response)
			}), []performanceProvider{{catalogs.ProviderID("deepinfra"), "DEEPINFRA_TOKEN", "Authorization", "Bearer sk-test-key", []string{"/openai/images/generations"}}}, nil)
			raw, err := json.Marshal(map[string]any{"model": "deepinfra/" + model, "prompt": "landscape", "n": tc.n, "size": tc.size})
			require.NoError(t, err)
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, fixture.gateway.URL+"/v1/images/generations", strings.NewReader(string(raw)))
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer "+performanceGatewayKey)
			req.Header.Set("Content-Type", "application/json")
			response, err := fixture.client.Do(req)
			require.NoError(t, err)
			body, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.NoError(t, response.Body.Close())
			if tc.status >= 500 && tc.calls > 0 {
				require.GreaterOrEqual(t, response.StatusCode, 500, string(body))
			} else {
				require.Equal(t, tc.status, response.StatusCode, string(body))
			}
			require.Equal(t, tc.calls, fixture.calls.Load())
			keys, err := fixture.application.store.ScanWithPrefix(t.Context(), "budget:v1:attempt:", 10)
			require.NoError(t, err)
			if tc.calls == 0 || tc.noBudget {
				require.Empty(t, keys)
				return
			}
			require.Len(t, keys, 1)
			data, err := fixture.application.store.Get(t.Context(), keys[0])
			require.NoError(t, err)
			var record reservation.Record
			require.NoError(t, json.Unmarshal(data, &record))
			require.Equal(t, tc.state, record.State)
			require.NotNil(t, record.NanoUSD)
			require.Equal(t, tc.cost, *record.NanoUSD)
		})
	}
}

func TestProductionPageRecognitionBudget(t *testing.T) {
	for _, tc := range []struct {
		name, usage, pages string
		state              reservation.State
		cost               int64
		success            bool
	}{
		{"measured", `,"usage_info":{"pages_processed":1}`, `[{"index":0,"markdown":"Invoice"}]`, reservation.Settled, 4000000, true},
		{"duplicate index retains charge", `,"usage_info":{"pages_processed":1}`, `[{"index":0},{"index":0}]`, reservation.Settled, 4000000, false},
		{"short document retains charge", `,"usage_info":{"pages_processed":1}`, `[]`, reservation.Settled, 4000000, false},
		{"missing usage", ``, `[{"index":0,"markdown":"Invoice"}]`, reservation.Uncertain, 4000000, true},
		{"null count", `,"usage_info":{"pages_processed":null}`, `[]`, reservation.Uncertain, 4000000, false},
		{"zero count", `,"usage_info":{"pages_processed":0}`, `[]`, reservation.Settled, 0, false},
		{"negative count", `,"usage_info":{"pages_processed":-1}`, `[]`, reservation.Uncertain, 4000000, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newPageRecognitionFixture(t, &limits.Limits{Spend: &limits.Budget{Limit: 2000000000, Interval: limits.IntervalDay}}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/v1/ocr" {
					var body map[string]any
					require.NoError(t, json.UnmarshalRead(r.Body, &body))
					require.Equal(t, "mistral-ocr-4-0", body["model"])
					require.Equal(t, []any{float64(0)}, body["pages"])
					require.NotContains(t, body, "document_annotation_format")
					require.NotContains(t, body, "bbox_annotation_format")
					_, _ = io.WriteString(w, `{"model":"mistral-ocr-4-0","pages":`+tc.pages+tc.usage+`}`)
					return
				}
				_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":3,"total_tokens":11,"prompt_tokens_details":{"cached_tokens":0}}}`)
			}))
			response, body := sendRecognitionRequest(t, fixture)
			if tc.success {
				require.Equal(t, http.StatusOK, response.StatusCode, string(body))
			} else {
				require.NotEqual(t, http.StatusOK, response.StatusCode, string(body))
			}
			keys, err := fixture.application.store.ScanWithPrefix(t.Context(), "budget:v1:attempt:", 10)
			require.NoError(t, err)
			want := 1
			if tc.success {
				want++
			}
			require.Len(t, keys, want, string(body))
			require.EqualValues(t, want, fixture.calls.Load())
			found := false
			for _, key := range keys {
				raw, err := fixture.application.store.Get(t.Context(), key)
				require.NoError(t, err)
				var record reservation.Record
				require.NoError(t, json.Unmarshal(raw, &record))
				if record.Attempt.OfferingID != "mistral/mistral-ocr-4-0" {
					continue
				}
				found = true
				require.Equal(t, tc.state, record.State)
				require.Equal(t, tc.cost, *record.NanoUSD)
				require.Equal(t, "recognition-parent", record.Attempt.RequestID)
			}
			require.True(t, found)
		})
	}
}

func newPageRecognitionFixture(t *testing.T, budgets *limits.Limits, handler http.Handler) *performanceFixture {
	t.Helper()
	return newPerformanceFixtureForProviders(t, 0, nil, true, budgets, handler, []performanceProvider{
		{catalogs.ProviderIDOpenAI, "OPENAI_API_KEY", "Authorization", "Bearer sk-test-key", []string{"/v1/chat/completions"}},
		{catalogs.ProviderIDMistralAI, "MISTRAL_API_KEY", "Authorization", "Bearer sk-test-key", []string{"/v1/ocr"}},
	}, func(key *apikey.APIKey) {
		key.AllowedModels = []string{"openai/gpt-4o-mini", "mistral/mistral-ocr-4-0"}
	})
}

func TestProductionPageRecognitionRefusesUnknownTokens(t *testing.T) {
	fixture := newPageRecognitionFixture(t, &limits.Limits{Tokens: &limits.Budget{Limit: 2000000, Interval: limits.IntervalDay}}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("unknown token bound reached provider")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	response, body := sendRecognitionRequest(t, fixture)
	require.Equal(t, http.StatusServiceUnavailable, response.StatusCode, string(body))
	require.Zero(t, fixture.calls.Load())
	keys, err := fixture.application.store.ScanWithPrefix(t.Context(), "budget:v1:attempt:", 10)
	require.NoError(t, err)
	require.Empty(t, keys)
}

func TestProductionPageRecognitionRefusesInsufficientSpend(t *testing.T) {
	fixture := newPageRecognitionFixture(t, &limits.Limits{Spend: &limits.Budget{Limit: 3999999, Interval: limits.IntervalDay}}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("over-budget request reached provider")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	response, body := sendRecognitionRequest(t, fixture)
	require.Equal(t, http.StatusPaymentRequired, response.StatusCode, string(body))
	require.Zero(t, fixture.calls.Load())
	keys, err := fixture.application.store.ScanWithPrefix(t.Context(), "budget:v1:attempt:", 10)
	require.NoError(t, err)
	require.Empty(t, keys)
}

func TestProductionSpeechBudget(t *testing.T) {
	for _, tc := range []struct {
		name, model, input string
		limit, cost        int64
		tokens             bool
		noBudget           bool
		emptyAudio         bool
		truncatedAudio     bool
		upstream, status   int
		calls              int64
		state              reservation.State
	}{
		{name: "measured characters", model: "tts-1", input: "hello", limit: 75000, cost: 75000, upstream: 200, status: 200, calls: 1, state: reservation.Settled},
		{name: "unicode code points", model: "tts-1", input: "é🦊", limit: 30000, cost: 30000, upstream: 200, status: 200, calls: 1, state: reservation.Settled},
		{name: "combining mark and whitespace", model: "tts-1", input: "e\u0301 !", limit: 60000, cost: 60000, upstream: 200, status: 200, calls: 1, state: reservation.Settled},
		{name: "maximum input", model: "tts-1", input: strings.Repeat("🦊", 4096), limit: 61440000, cost: 61440000, upstream: 200, status: 200, calls: 1, state: reservation.Settled},
		{name: "input exceeds bound", model: "tts-1", input: strings.Repeat("a", 4097), limit: 100000000, upstream: 200, status: 503},
		{name: "no required budget", model: "tts-1", input: "hello", noBudget: true, upstream: 200, status: 200, calls: 1},
		{name: "empty audio retains charge", model: "tts-1", input: "hello", limit: 75000, cost: 75000, emptyAudio: true, upstream: 200, status: 500, calls: 1, state: reservation.Settled},
		{name: "truncated audio retains capacity", model: "tts-1", input: "hello", limit: 75000, cost: 75000, truncatedAudio: true, upstream: 200, status: 500, calls: 1, state: reservation.Uncertain},
		{name: "HD price", model: "tts-1-hd", input: "hello", limit: 150000, cost: 150000, upstream: 200, status: 200, calls: 1, state: reservation.Settled},
		{name: "insufficient capacity", model: "tts-1", input: "hello", limit: 74999, upstream: 200, status: 402},
		{name: "unknown tokens", model: "tts-1", input: "hello", limit: 1000000, tokens: true, upstream: 200, status: 503},
		{name: "provider failure", model: "tts-1", input: "hello", limit: 75000, cost: 75000, upstream: 500, status: 500, calls: 1, state: reservation.Uncertain},
	} {
		t.Run(tc.name, func(t *testing.T) {
			budget := &limits.Limits{Spend: &limits.Budget{Limit: tc.limit, Interval: limits.IntervalDay}}
			if tc.tokens {
				budget = &limits.Limits{Tokens: &limits.Budget{Limit: tc.limit, Interval: limits.IntervalDay}}
			}
			if tc.noBudget {
				budget = nil
			}
			fixture := newPerformanceFixtureForOperation(t, 0, nil, true, budget, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var wire struct {
					Model string `json:"model"`
					Input string `json:"input"`
				}
				require.NoError(t, json.UnmarshalRead(r.Body, &wire))
				require.Equal(t, tc.model, wire.Model)
				require.Equal(t, tc.input, wire.Input)
				w.Header().Set("Content-Type", "audio/mpeg")
				if tc.truncatedAudio {
					w.Header().Set("Content-Length", "100")
				}
				w.WriteHeader(tc.upstream)
				if !tc.emptyAudio {
					_, _ = io.WriteString(w, "audio")
				}
			}), []string{"/v1/audio/speech"})
			raw, err := json.Marshal(map[string]any{"model": "openai/" + tc.model, "input": tc.input, "voice": "alloy"})
			require.NoError(t, err)
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, fixture.gateway.URL+"/v1/audio/speech", strings.NewReader(string(raw)))
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer "+performanceGatewayKey)
			req.Header.Set("Content-Type", "application/json")
			response, err := fixture.client.Do(req)
			require.NoError(t, err)
			body, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.NoError(t, response.Body.Close())
			if tc.status >= 500 && tc.calls > 0 {
				require.GreaterOrEqual(t, response.StatusCode, 500, string(body))
			} else {
				require.Equal(t, tc.status, response.StatusCode, string(body))
			}
			require.Equal(t, tc.calls, fixture.calls.Load())
			keys, err := fixture.application.store.ScanWithPrefix(t.Context(), "budget:v1:attempt:", 10)
			require.NoError(t, err)
			if tc.calls == 0 || tc.noBudget {
				require.Empty(t, keys)
				return
			}
			require.Len(t, keys, 1)
			data, err := fixture.application.store.Get(t.Context(), keys[0])
			require.NoError(t, err)
			var record reservation.Record
			require.NoError(t, json.Unmarshal(data, &record))
			require.Equal(t, tc.state, record.State)
			require.NotNil(t, record.NanoUSD)
			require.Equal(t, tc.cost, *record.NanoUSD)
		})
	}
}

func TestProductionTranscriptionBudgetRefusal(t *testing.T) {
	for _, path := range []string{"/v1/audio/transcriptions", "/v1/audio/translations", "/api/v1/audio/transcriptions"} {
		for _, meter := range []string{"spend", "tokens", "absent"} {
			t.Run(path+"/"+meter, func(t *testing.T) {
				var budgets *limits.Limits
				switch meter {
				case "spend":
					budgets = &limits.Limits{Spend: &limits.Budget{Limit: 1_000_000_000, Interval: limits.IntervalDay}}
				case "tokens":
					budgets = &limits.Limits{Tokens: &limits.Budget{Limit: 1_000_000, Interval: limits.IntervalDay}}
				}
				fixture := newPerformanceFixtureForOperation(t, 0, nil, true, budgets,
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						require.NoError(t, r.ParseMultipartForm(1024))
						defer r.MultipartForm.RemoveAll()
						require.Equal(t, "whisper-1", r.FormValue("model"))
						file, header, err := r.FormFile("file")
						require.NoError(t, err)
						defer file.Close()
						require.Equal(t, "clip.mp3", header.Filename)
						audio, err := io.ReadAll(file)
						require.NoError(t, err)
						require.Equal(t, []byte{0xff, 0xfb, 0x90}, audio)
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, `{"text":"hello"}`)
					}), []string{"/v1/audio/transcriptions", "/v1/audio/translations"})
				var form bytes.Buffer
				writer := multipart.NewWriter(&form)
				require.NoError(t, writer.WriteField("model", "openai/whisper-1"))
				part, err := writer.CreateFormFile("file", "clip.mp3")
				require.NoError(t, err)
				_, err = part.Write([]byte{0xff, 0xfb, 0x90})
				require.NoError(t, err)
				require.NoError(t, writer.Close())
				req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, fixture.gateway.URL+path, &form)
				require.NoError(t, err)
				req.Header.Set("Authorization", "Bearer "+performanceGatewayKey)
				req.Header.Set("Content-Type", writer.FormDataContentType())
				response, err := fixture.client.Do(req)
				require.NoError(t, err)
				body, err := io.ReadAll(response.Body)
				require.NoError(t, err)
				require.NoError(t, response.Body.Close())
				if meter == "absent" {
					require.Equal(t, http.StatusOK, response.StatusCode, string(body))
					require.Contains(t, string(body), "hello")
					require.EqualValues(t, 1, fixture.calls.Load())
				} else {
					require.Equal(t, http.StatusServiceUnavailable, response.StatusCode, string(body))
					require.Contains(t, string(body), "verified billing bound")
					require.Zero(t, fixture.calls.Load(), "compressed bytes do not establish a duration or token bound")
				}
				keys, err := fixture.application.store.ScanWithPrefix(t.Context(), "budget:v1:attempt:", 10)
				require.NoError(t, err)
				require.Empty(t, keys)
			})
		}
	}
}
