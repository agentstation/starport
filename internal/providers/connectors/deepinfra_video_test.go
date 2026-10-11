package connectors

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"
)

func TestNativeVideoWireAndMeasuredUsage(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/inference/exact/model", r.URL.Path)
		require.Equal(t, "Bearer video-key", r.Header.Get("Authorization"))
		var payload map[string]any
		require.NoError(t, json.UnmarshalRead(r.Body, &payload))
		require.Equal(t, map[string]any{"prompt": "A cloud", "negative_prompt": "text", "seconds": float64(5), "resolution": "720p", "orientation": "landscape", "seed": float64(0)}, payload)
		// The live native response omitted status. The model schema supplies succeeded.
		_, _ = w.Write([]byte(`{"request_id":"provider-request","video_url":"data:video/mp4;base64,bXA0","inference_status":{"runtime_ms":268340,"cost":0.375,"output_length":5}}`))
	}))
	defer server.Close()
	connector, err := newDeepInfraVideoConnector("fixture", mediaTestConfig(server.URL))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connector.Close()) })
	generator, ok := NativeVideoGeneratorFor(connector, catalogs.EndpointTypeDeepInfraVideo)
	require.True(t, ok)
	seed := int64(0)
	request := approveConnectorFixture(t, &NativeVideoRequest{MediaTarget: MediaTarget{Model: "exact/model", Endpoint: InferenceEndpoint{Type: catalogs.EndpointTypeDeepInfraVideo, URL: server.URL + "/inference/exact/model"}, Credential: testAPIMaterial("video-key")}, Prompt: "A cloud", NegativePrompt: "text", Size: "1280x720", Seconds: 5, Seed: &seed, MaxBytes: 3})
	result, err := generator.GenerateVideo(nativeTestContext(t), request)
	require.NoError(t, err)
	require.Equal(t, int64(1), calls.Load())
	require.Equal(t, "provider-request", result.RequestID)
	require.Equal(t, int64(5), *result.OutputSeconds)
	require.Equal(t, .375, *result.EstimatedUSD)
	require.Equal(t, JobAsset{ContentType: "video/mp4", Bytes: []byte("mp4")}, result.Asset)
	copied := result.Clone()
	*copied.OutputSeconds, *copied.EstimatedUSD, copied.Asset.Bytes[0] = 99, 99, 'x'
	require.Equal(t, int64(5), *result.OutputSeconds)
	require.Equal(t, .375, *result.EstimatedUSD)
	require.Equal(t, "mp4", string(result.Asset.Bytes))
	_, isJobRunner := connector.(JobRunner)
	require.False(t, isJobRunner, "a native request ID must not pretend to support polling")
}

func TestNativeVideoKeepsUsageWhenAssetOrStateFails(t *testing.T) {
	t.Parallel()
	credentialReference := (&url.URL{Scheme: "https", Host: "assets.example", Path: "/video.mp4", User: url.UserPassword("user", "password")}).String()
	for _, tc := range []struct {
		name, extra, video string
		invalid            bool
	}{
		{"inline", `"output_length":5`, "data:video/mp4;base64,bXA0", false},
		{"explicit success", `"status":"succeeded","output_length":5`, "data:video/mp4;base64,bXA0", false},
		{"failed", `"status":"failed","output_length":5`, "", true},
		{"queued", `"status":"queued","output_length":5`, "", true},
		{"running", `"status":"running","output_length":5`, "", true},
		{"unknown", `"status":"new-state","output_length":5`, "", true},
		{"invalid asset", `"output_length":5`, "data:video/mp4;base64,!", true},
		{"oversize asset", `"output_length":5`, "data:video/mp4;base64,bXA0eA==", true},
		{"wrong media", `"output_length":5`, "data:text/html;base64,bXA0", true},
		{"external reference", `"output_length":5`, "https://assets.example/video.mp4", false},
		{"loopback reference", `"output_length":5`, "http://127.0.0.1:7827/video.mp4", false},
		{"relative reference", `"output_length":5`, "/video.mp4", false},
		{"network relative", `"output_length":5`, "//assets.example/video.mp4", true},
		{"credential reference", `"output_length":5`, credentialReference, true},
		{"insecure reference", `"output_length":5`, "http://assets.example/video.mp4", true},
		{"empty reference", `"output_length":5`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			video, err := json.Marshal(tc.video)
			require.NoError(t, err)
			body := `{"request_id":"r","video_url":` + string(video) + `,"inference_status":{"cost":0.375,` + tc.extra + `}}`
			result, err := decodeDeepInfraVideo(strings.NewReader(body), 3)
			if tc.invalid {
				require.ErrorIs(t, err, ErrInvalidMediaRequest)
			} else {
				require.NoError(t, err)
			}
			require.NotNil(t, result)
			require.Equal(t, int64(5), *result.OutputSeconds)
			require.Equal(t, .375, *result.EstimatedUSD)
		})
	}
}

func TestNativeVideoMissingUsageNeverBecomesZero(t *testing.T) {
	t.Parallel()
	for _, usage := range []string{`{}`, `{"cost":0.375}`, `{"output_length":null}`, `{"output_length":-1}`} {
		body := `{"request_id":"r","video_url":"data:video/mp4;base64,bXA0","inference_status":` + usage + `}`
		result, err := decodeDeepInfraVideo(strings.NewReader(body), 3)
		require.NoError(t, err)
		require.Nil(t, result.OutputSeconds)
	}
	result, err := decodeDeepInfraVideo(strings.NewReader(`{"request_id":"r","video_url":"data:video/mp4;base64,bXA0","inference_status":{"output_length":0,"cost":0}}`), 3)
	require.NoError(t, err)
	require.NotNil(t, result.OutputSeconds)
	require.Zero(t, *result.OutputSeconds)
	for _, body := range []string{
		`{"request_id":"r","inference_status":{"output_length":1.5}}`,
		`{"request_id":"r","inference_status":{"output_length":99999999999999999999}}`,
		`{"request_id":"r","request_id":"other"}`, `{} {}`, strings.Repeat("x", 65543),
	} {
		result, err := decodeDeepInfraVideo(strings.NewReader(body), 3)
		require.ErrorIs(t, err, ErrInvalidMediaRequest)
		require.Nil(t, result)
	}
}

func TestNativeVideoRefusesInvalidInputsBeforeDispatch(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("invalid request reached provider") }))
	defer server.Close()
	connector, err := newDeepInfraVideoConnector("fixture", mediaTestConfig(server.URL))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connector.Close()) })
	generator := connector.(NativeVideoGenerator)
	valid := NativeVideoRequest{MediaTarget: MediaTarget{Endpoint: InferenceEndpoint{Type: catalogs.EndpointTypeDeepInfraVideo, URL: server.URL}}, Prompt: "Cloud", Seconds: 5, Size: "1280x720", MaxBytes: 3}
	for _, change := range []func(*NativeVideoRequest){
		func(r *NativeVideoRequest) { r.Prompt = " " }, func(r *NativeVideoRequest) { r.Seconds = 0 },
		func(r *NativeVideoRequest) { r.MaxBytes = 0 }, func(r *NativeVideoRequest) { r.MaxBytes = 1<<63 - 1 },
		func(r *NativeVideoRequest) { r.Size = "1x1" }, func(r *NativeVideoRequest) { r.Size = "01280x720" },
		func(r *NativeVideoRequest) { r.Endpoint.Type = catalogs.EndpointTypeOpenAI },
	} {
		request := valid
		change(&request)
		_, err := generator.GenerateVideo(nativeTestContext(t), &request)
		require.Error(t, err)
	}
	_, err = generator.GenerateVideo(nativeTestContext(t), nil)
	require.ErrorIs(t, err, ErrInvalidMediaRequest)
}

func TestNativeVideoDoesNotRetryProviderFailure(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("private prompt and credential"))
	}))
	defer server.Close()
	connector, err := newDeepInfraVideoConnector("fixture", mediaTestConfig(server.URL))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connector.Close()) })
	request := approveConnectorFixture(t, &NativeVideoRequest{MediaTarget: MediaTarget{Endpoint: InferenceEndpoint{Type: catalogs.EndpointTypeDeepInfraVideo, URL: server.URL}, Credential: testAPIMaterial("video-key")}, Prompt: "Cloud", Size: "1280x720", Seconds: 5, MaxBytes: 3})
	_, err = connector.(NativeVideoGenerator).GenerateVideo(nativeTestContext(t), request)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "private")
	require.Equal(t, int64(1), calls.Load())
}

func nativeTestContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestNativeVideoRequiresExecutionDeadline(t *testing.T) {
	connector, err := newDeepInfraVideoConnector("fixture", mediaTestConfig("https://provider.example"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connector.Close()) })
	_, err = connector.(NativeVideoGenerator).GenerateVideo(context.Background(), nil)
	require.ErrorContains(t, err, "execution deadline")
}

func TestNativeVideoResolvesRelativeAssetWithoutFetching(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		require.Equal(t, http.MethodPost, r.Method)
		_, _ = w.Write([]byte(`{"request_id":"request","video_url":"/video.mp4?signature=private","inference_status":{"output_length":5}}`))
	}))
	defer server.Close()
	connector, err := newDeepInfraVideoConnector("fixture", mediaTestConfig(server.URL))
	require.NoError(t, err)
	defer connector.Close()
	request := approveConnectorFixture(t, &NativeVideoRequest{MediaTarget: MediaTarget{Model: "exact/model", Endpoint: InferenceEndpoint{Type: catalogs.EndpointTypeDeepInfraVideo, URL: server.URL + "/inference/exact/model"}, Credential: testAPIMaterial("video-key")}, Prompt: "Cloud", Seconds: 5, Size: "1280x720", MaxBytes: 16})
	result, err := connector.(NativeVideoGenerator).GenerateVideo(nativeTestContext(t), request)
	require.NoError(t, err)
	require.Equal(t, server.URL+"/video.mp4?signature=private", result.AssetURL)
	require.EqualValues(t, 1, calls.Load())
}
