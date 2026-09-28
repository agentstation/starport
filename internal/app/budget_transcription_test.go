package app

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"testing"

	"github.com/agentstation/starport/internal/limits"
	"github.com/stretchr/testify/require"
)

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
