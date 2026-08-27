package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/credentials"
)

// OpenAIConnector implements the Connector interface for OpenAI
type OpenAIConnector struct {
	OpenAICompatibleConnector
	providerID catalogs.ProviderID
}

// NewOpenAIConnector creates a new OpenAI connector
func NewOpenAIConnector(config ProviderConfig) (*OpenAIConnector, error) {
	return newOpenAIConnector(catalogs.ProviderIDOpenAI, "openai", config)
}

func newOpenAIConnector(
	providerID catalogs.ProviderID,
	provider string,
	config ProviderConfig,
) (*OpenAIConnector, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	httpClient := newProviderHTTPClient(config)

	return &OpenAIConnector{
		OpenAICompatibleConnector: OpenAICompatibleConnector{
			config:     config,
			provider:   provider,
			httpClient: httpClient,
		},
		providerID: providerID,
	}, nil
}

// Name returns the provider name
func (c *OpenAIConnector) Name() string {
	return c.provider
}

// Chat performs a chat completion request
func (c *OpenAIConnector) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	return c.OpenAICompatibleConnector.Chat(ctx, req, c.setHeaders, c.handleError)
}

// ChatStream performs a streaming chat completion request
func (c *OpenAIConnector) ChatStream(ctx context.Context, req *ChatRequest) (ChatStream, error) {
	return c.OpenAICompatibleConnector.ChatStream(ctx, req, c.setHeaders, c.handleError, newOpenAICompatibleStream)
}

// Embeddings generates embeddings for the given input
func (c *OpenAIConnector) Embeddings(ctx context.Context, req *EmbeddingsRequest) (*EmbeddingsResponse, error) {
	return c.OpenAICompatibleConnector.Embeddings(ctx, req, c.setHeaders, c.handleError)
}

// GenerateImages serves images-generations and images-edits.
func (c *OpenAIConnector) GenerateImages(ctx context.Context, req *ImagesRequest) (*ImagesResponse, error) {
	return c.OpenAICompatibleConnector.GenerateImages(ctx, req, c.setHeaders, c.handleError)
}

// SynthesizeSpeech serves audio-speech.
func (c *OpenAIConnector) SynthesizeSpeech(ctx context.Context, req *SpeechRequest) (*SpeechResponse, error) {
	return c.OpenAICompatibleConnector.SynthesizeSpeech(ctx, req, c.setHeaders, c.handleError)
}

// Transcribe serves audio-transcriptions and audio-translations.
func (c *OpenAIConnector) Transcribe(ctx context.Context, req *TranscriptionRequest) (*TranscriptionResponse, error) {
	return c.OpenAICompatibleConnector.Transcribe(ctx, req, c.setHeaders, c.handleError)
}

// SubmitJob serves videos-generations.
func (c *OpenAIConnector) SubmitJob(ctx context.Context, req *JobSubmission) (*ProviderJob, error) {
	return c.OpenAICompatibleConnector.SubmitJob(ctx, req, c.setHeaders, c.handleError)
}

// PollJob reads one accepted video job.
func (c *OpenAIConnector) PollJob(ctx context.Context, ref *ProviderJobRef) (*ProviderJob, error) {
	return c.OpenAICompatibleConnector.PollJob(ctx, ref, c.setHeaders, c.handleError)
}

// CancelJob stops one accepted video job.
func (c *OpenAIConnector) CancelJob(ctx context.Context, ref *ProviderJobRef) (*ProviderJob, error) {
	return c.OpenAICompatibleConnector.CancelJob(ctx, ref, c.setHeaders, c.handleError)
}

// Close closes the connector
func (c *OpenAIConnector) Close() error {
	c.httpClient.CloseIdleConnections()
	return nil
}

// setHeaders sets OpenAI-specific headers
func (c *OpenAIConnector) setHeaders(material credentials.Material, req *http.Request) error {
	if err := applyRequestAuthentication(material, req); err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return nil
}

// handleError handles OpenAI-specific error responses
func (c *OpenAIConnector) handleError(resp *http.Response) error {
	var errResp struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		return &APIError{
			StatusCode: resp.StatusCode,
			Message:    "failed to decode error response",
			Provider:   string(c.providerID),
		}
	}

	return &APIError{
		StatusCode: resp.StatusCode,
		Message:    errResp.Error.Message,
		Type:       errResp.Error.Type,
		Code:       errResp.Error.Code,
		Provider:   string(c.providerID),
	}
}
