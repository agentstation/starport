package connectors

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAIAuthenticationPreservesMultipartUploads(t *testing.T) {
	for _, operation := range []string{"transcription", "translation", "image edit"} {
		t.Run(operation, func(t *testing.T) {
			field := "file"
			model := "whisper-1"
			if operation == "image edit" {
				field, model = "image", "gpt-image-1"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
				if !assert.NoError(t, r.ParseMultipartForm(1024)) {
					http.Error(w, "multipart upload required", http.StatusBadRequest)
					return
				}
				defer r.MultipartForm.RemoveAll()
				assert.Equal(t, model, r.FormValue("model"))
				file, header, err := r.FormFile(field)
				if !assert.NoError(t, err) {
					http.Error(w, "file required", http.StatusBadRequest)
					return
				}
				defer file.Close()
				assert.Equal(t, "fixture.bin", header.Filename)
				data, err := io.ReadAll(file)
				assert.NoError(t, err)
				assert.Equal(t, []byte("fixture bytes"), data)
				w.Header().Set("Content-Type", "application/json")
				if operation == "image edit" {
					_, _ = io.WriteString(w, `{"data":[{"b64_json":"aW1hZ2U="}]}`)
				} else {
					_, _ = io.WriteString(w, `{"text":"hello"}`)
				}
			}))
			t.Cleanup(server.Close)
			connector, err := NewOpenAIConnector(mediaTestConfig(server.URL))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, connector.Close()) })
			target := MediaTarget{Model: model, Endpoint: InferenceEndpoint{Type: catalogs.EndpointTypeOpenAI, URL: server.URL + "/" + map[string]string{"transcription": "audio/transcriptions", "translation": "audio/translations", "image edit": "images/edits"}[operation]}, Credential: testAPIMaterial("test-key")}
			upload := UploadedFile{Filename: "fixture.bin", Bytes: []byte("fixture bytes")}
			if operation == "image edit" {
				response, err := connector.GenerateImages(t.Context(), approveConnectorFixture(t, &ImagesRequest{MediaTarget: target, Image: upload, Prompt: "edit"}))
				require.NoError(t, err)
				require.Len(t, response.Data, 1)
			} else {
				response, err := connector.Transcribe(t.Context(), approveConnectorFixture(t, &TranscriptionRequest{MediaTarget: target, File: upload}))
				require.NoError(t, err)
				require.Equal(t, "hello", response.Text)
			}
		})
	}
}
