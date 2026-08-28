package proxy

import (
	"context"
	"fmt"
	"testing"

	"github.com/agentstation/starport/internal/execution"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/providers/connectors"
	routepkg "github.com/agentstation/starport/internal/router"
	"github.com/agentstation/starport/internal/routing"
	"github.com/agentstation/starport/internal/tokenize"
	"github.com/stretchr/testify/require"
)

// refusingRouter answers every route with the planner's modality refusal. It
// stands in for a catalog whose models all read text alone.
type refusingRouter struct{ unroutedOperations }

func (refusingRouter) SelectModel(context.Context, *routepkg.Request) (string, connectors.Connector, error) {
	return "", nil, routingModalityRefusal()
}

func (refusingRouter) RouteWithFallback(context.Context, *routepkg.Request) (*routepkg.Response, error) {
	return nil, routingModalityRefusal()
}

func (refusingRouter) RouteStream(context.Context, *routepkg.Request) (execution.ManagedStream, error) {
	return nil, routingModalityRefusal()
}

func (refusingRouter) RouteEmbeddings(context.Context, *routepkg.EmbeddingRequest) (*routepkg.EmbeddingResponse, error) {
	return nil, routingModalityRefusal()
}

// routingModalityRefusal builds the error the planner returns, wrapped the
// same way the router wraps it, so the proxy is tested against the real
// error shape rather than a bare sentinel.
func routingModalityRefusal() error {
	return fmt.Errorf(
		"%w: %w: openai/gpt-4o@openai: model does not read audio input",
		routing.ErrNoCandidate, routing.ErrModalityUnsupported,
	)
}

func audioRequest() *ChatCompletionRequest {
	return &ChatCompletionRequest{
		Request: inference.ChatRequest{
			Model: "openai/gpt-4o",
			Messages: []inference.Message{{
				Role: inference.RoleUser,
				Content: []inference.ContentPart{
					{Kind: inference.ContentText, Text: "what is said here"},
					{Kind: inference.ContentAudio, Audio: &inference.Audio{
						Data:   []byte{0x52, 0x49, 0x46, 0x46},
						Format: "wav",
					}},
				},
			}},
		},
	}
}

// TestModalityRefusalAnswersTheCaller holds the status contract. Every other
// routing failure is a gateway condition and answers 503, which tells the
// caller to retry. Audio sent to a model that reads none never succeeds on
// retry, so it answers 400 and names the modality.
func TestModalityRefusalAnswersTheCaller(t *testing.T) {
	service := &proxy{router: refusingRouter{}}

	_, err := service.ProcessChatCompletion(context.Background(), audioRequest())

	var validation *ValidationError
	require.ErrorAs(t, err, &validation)
	require.Contains(t, validation.Error(), "audio")
	require.Contains(t, validation.Error(), "openai/gpt-4o")

	streaming := audioRequest()
	streaming.Request.Stream = true
	_, err = service.ProcessChatCompletionStream(context.Background(), streaming)
	require.ErrorAs(t, err, &validation)
	require.Contains(t, validation.Error(), "audio")
}

// TestRequestMetadataCarriesTheModalities proves the planner receives what
// the request holds. Without this list the planner had no way to tell an
// audio request from a text one, so it offered every model to both.
func TestRequestMetadataCarriesTheModalities(t *testing.T) {
	router := &capturingRouter{}
	service := &proxy{router: router}

	_, err := service.ProcessChatCompletion(context.Background(), audioRequest())
	require.NoError(t, err)
	require.NotNil(t, router.req)
	require.NotNil(t, router.req.Metadata)
	require.Equal(t, []string{"audio"}, router.req.Metadata.RequiredModalities)
	require.NotContains(t, router.req.Metadata.RequiredFeatures, "vision")
}

// TestImageRequestStillAsksForVision proves the modality list did not lose
// the capability the routing path already used. Vision is now derived from
// the same list, so an image request must still ask for it.
func TestImageRequestStillAsksForVision(t *testing.T) {
	router := &capturingRouter{}
	service := &proxy{router: router}

	_, err := service.ProcessChatCompletion(context.Background(), &ChatCompletionRequest{
		Request: inference.ChatRequest{
			Model: "openai/gpt-4o",
			Messages: []inference.Message{{
				Role: inference.RoleUser,
				Content: []inference.ContentPart{{
					Kind:  inference.ContentImage,
					Image: &inference.Image{URL: "https://example.test/cat.png"},
				}},
			}},
		},
	})
	require.NoError(t, err)
	require.Contains(t, router.req.Metadata.RequiredFeatures, "vision")
	require.Equal(t, []string{"image"}, router.req.Metadata.RequiredModalities)
}

// TestOneEstimatorServesRoutingAndUsage holds the rule that replaced the
// duplicate estimator. The routing path once counted four bytes per token
// while the usage path counted with a real codec, so the same request was
// two different sizes depending on which path asked.
func TestOneEstimatorServesRoutingAndUsage(t *testing.T) {
	estimator := tokenize.NewEstimator()
	router := &capturingRouter{}
	service := &proxy{router: router, estimator: estimator}

	request := &ChatCompletionRequest{
		Request: inference.ChatRequest{
			Model: "openai/gpt-4o",
			Messages: []inference.Message{{
				Role: inference.RoleUser,
				Content: []inference.ContentPart{{
					Kind: inference.ContentText,
					Text: "Summarize the third quarter revenue report for the board.",
				}},
			}},
		},
	}
	_, err := service.ProcessChatCompletion(context.Background(), request)
	require.NoError(t, err)

	usageCount := estimator.CountMessages(
		tokenize.Hint{Model: request.Request.Model},
		request.Request.Messages,
	)
	require.Equal(t, usageCount, router.req.Metadata.EstimatedTokens)

	// The counts are equal because they come from one estimator, not because
	// the two counting rules happen to agree. The retired heuristic proves it
	// by disagreeing.
	retired := len(request.Request.Messages[0].Content[0].Text) / 4
	require.NotEqual(t, retired, usageCount)
}
