package connectors

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/failure"
)

func mediaTestConfig(baseURL string) ProviderConfig {
	return ProviderConfig{BaseURL: baseURL, Timeout: 5 * time.Second, MaxConnections: 4}
}

// TestADescriptorCannotDeclareAMediaOperationItCannotPerform holds the guard at
// activation. Without it a descriptor states an operation, the catalog
// publishes it, a route reaches it, and the caller learns at request time that
// no code performs it.
func TestADescriptorCannotDeclareAMediaOperationItCannotPerform(t *testing.T) {
	// MockConnector serves chat and embeddings, and implements no media
	// interface, which is the exact shape of a transport left behind when a
	// descriptor gains an operation.
	registry, err := NewTransportRegistry(TransportDescriptor{
		EndpointType: catalogs.EndpointTypeOpenAI,
		Operations: []catalogs.ProviderOperation{
			catalogs.ProviderOperationChatCompletions,
			catalogs.ProviderOperationAudioSpeech,
		},
		Factory: func(catalogs.ProviderID, ProviderConfig) (Connector, error) {
			return NewMockConnector(ProviderConfig{}), nil
		},
	})
	require.NoError(t, err)

	_, err = registry.NewProviderConnector(
		"acme",
		[]catalogs.EndpointType{catalogs.EndpointTypeOpenAI},
		mediaTestConfig("https://provider.example"),
	)
	require.ErrorIs(t, err, ErrTransportInterfaceMissing)
	require.Contains(t, err.Error(), "SpeechSynthesizer")
	require.Contains(t, err.Error(), string(catalogs.ProviderOperationAudioSpeech))
}

// TestConnectorGainedNoMethodForMedia states the reason the media interfaces are
// separate. Adding a media method to Connector would force every transport to
// answer a call it cannot perform, and a stub returning "unsupported" reads the
// same to the compiler as a real implementation.
func TestConnectorGainedNoMethodForMedia(t *testing.T) {
	connector := reflect.TypeOf((*Connector)(nil)).Elem()
	names := make([]string, 0, connector.NumMethod())
	for index := range connector.NumMethod() {
		names = append(names, connector.Method(index).Name)
	}
	require.Equal(t, []string{"Chat", "ChatStream", "Close", "Embeddings", "Name"}, names)

	// The media interfaces stay one method wide, so a transport declares
	// exactly the capability it implements.
	require.Equal(t, 1, reflect.TypeOf((*ImageGenerator)(nil)).Elem().NumMethod())
	require.Equal(t, 1, reflect.TypeOf((*SpeechSynthesizer)(nil)).Elem().NumMethod())
	require.Equal(t, 1, reflect.TypeOf((*Transcriber)(nil)).Elem().NumMethod())
}

// TestAMediaInterfaceIsProbedOnTheSelectedTransport proves the probe reads the
// transport the route chose. One provider connector spans protocols, and only
// the OpenAI protocol carries a compiled media transport, so probing the
// composed connector would report a capability the selected protocol lacks.
func TestAMediaInterfaceIsProbedOnTheSelectedTransport(t *testing.T) {
	registry, err := ProductionTransportRegistry()
	require.NoError(t, err)
	connector, err := registry.NewProviderConnector(
		"acme",
		[]catalogs.EndpointType{catalogs.EndpointTypeOpenAI, catalogs.EndpointTypeAnthropic},
		mediaTestConfig("https://provider.example"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = connector.Close() })

	_, openAIServes := SpeechSynthesizerFor(connector, catalogs.EndpointTypeOpenAI)
	require.True(t, openAIServes)
	_, anthropicServes := SpeechSynthesizerFor(connector, catalogs.EndpointTypeAnthropic)
	require.False(t, anthropicServes)
	_, absentServes := SpeechSynthesizerFor(connector, catalogs.EndpointTypeGoogle)
	require.False(t, absentServes)

	_, generates := ImageGeneratorFor(connector, catalogs.EndpointTypeOpenAI)
	require.True(t, generates)
	_, transcribes := TranscriberFor(connector, catalogs.EndpointTypeOpenAI)
	require.True(t, transcribes)
}

// TestAMediaFailureNormalizesLikeAChatFailure is the reason every media method
// sends through the shared error handler. A media path with its own error
// vocabulary would give the same provider rejection two classifications, and
// the retry budget and availability state read that class.
func TestAMediaFailureNormalizesLikeAChatFailure(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		wantKind  failure.Kind
		retryable bool
	}{
		{
			name:      "rate limit",
			status:    http.StatusTooManyRequests,
			body:      `{"error":{"message":"slow down","type":"rate_limit_error"}}`,
			wantKind:  failure.RateLimit,
			retryable: true,
		},
		{
			name:     "authentication",
			status:   http.StatusUnauthorized,
			body:     `{"error":{"message":"bad key","type":"authentication_error"}}`,
			wantKind: failure.Authentication,
		},
		{
			name:      "unavailable",
			status:    http.StatusServiceUnavailable,
			body:      `{"error":{"message":"down","type":"server_error"}}`,
			wantKind:  failure.ProviderUnavailable,
			retryable: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()

			connector, err := NewOpenAIConnector(mediaTestConfig(server.URL + "/v1"))
			require.NoError(t, err)
			endpoint := InferenceEndpoint{Type: catalogs.EndpointTypeOpenAI, URL: server.URL + "/v1"}
			credential := testAPIMaterial("test-key")

			chatFailure := chatCallFailure(t, connector, endpoint, credential)
			require.Equal(t, test.wantKind, chatFailure.Kind())
			require.Equal(t, test.retryable, chatFailure.Retryable())

			mediaCalls := map[string]func() error{
				"images": func() error {
					_, err := connector.GenerateImages(context.Background(), &ImagesRequest{
						MediaTarget: MediaTarget{
							Model: "gpt-image-1", Endpoint: endpoint, Credential: credential,
						},
						Prompt: "a cat",
					})
					return err
				},
				"speech": func() error {
					_, err := connector.SynthesizeSpeech(context.Background(), &SpeechRequest{
						MediaTarget: MediaTarget{
							Model: "tts-1", Endpoint: endpoint, Credential: credential,
						},
						Input: "hello", Voice: "alloy",
					})
					return err
				},
				"transcription": func() error {
					_, err := connector.Transcribe(context.Background(), &TranscriptionRequest{
						// Endpoint and credential match the chat call, so the
						// only difference between the two paths is the method.
						MediaTarget: MediaTarget{
							Model: "whisper-1", Endpoint: endpoint, Credential: credential,
						},
						File: UploadedFile{Filename: "clip.wav", Bytes: []byte("RIFF")},
					})
					return err
				},
			}
			for name, call := range mediaCalls {
				t.Run(name, func(t *testing.T) {
					normalized := NormalizeFailure("openai", call())
					require.Equal(t, chatFailure.Kind(), normalized.Kind())
					require.Equal(t, chatFailure.Retryable(), normalized.Retryable())
					require.Equal(t, chatFailure.StateScope(), normalized.StateScope())
				})
			}
		})
	}
}

func chatCallFailure(
	t *testing.T,
	connector *OpenAIConnector,
	endpoint InferenceEndpoint,
	credential credentials.Material,
) *failure.Failure {
	t.Helper()
	_, err := connector.Chat(context.Background(), &ChatRequest{
		Model:      "gpt-4",
		Messages:   []Message{{Role: RoleUser, Content: "hello"}},
		Endpoint:   endpoint,
		Credential: credential,
	})
	require.Error(t, err)
	return NormalizeFailure("openai", err)
}

// TestATranscriptionWithoutAudioFailsBeforeTheWire keeps a request that cannot
// succeed from spending a credential and a round trip.
func TestATranscriptionWithoutAudioFailsBeforeTheWire(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("an empty transcription reached the provider")
	}))
	defer server.Close()

	connector, err := NewOpenAIConnector(mediaTestConfig(server.URL + "/v1"))
	require.NoError(t, err)
	_, err = connector.Transcribe(context.Background(), &TranscriptionRequest{
		MediaTarget: MediaTarget{
			Model:      "whisper-1",
			Endpoint:   InferenceEndpoint{Type: catalogs.EndpointTypeOpenAI, URL: server.URL + "/v1"},
			Credential: testAPIMaterial("test-key"),
		},
	})
	require.True(t, errors.Is(err, ErrInvalidMediaRequest), "%v", err)
}
