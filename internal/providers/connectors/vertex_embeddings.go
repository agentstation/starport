package connectors

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"net/http"

	"github.com/agentstation/starmap/pkg/catalogs"
)

// Embeddings preserves text items and provider-reported token measurements.
// Token-ID input is unsupported by the Vertex text prediction operation.
func (c *VertexAIConnector) Embeddings(ctx context.Context, req *EmbeddingsRequest) (*EmbeddingsResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("embedding request is required")
	}
	inputs, err := embeddingInputs(req.Input)
	if err != nil {
		return nil, err
	}
	if len(inputs) == 0 {
		return nil, fmt.Errorf("embedding input is required")
	}
	instances := make([]map[string]string, len(inputs))
	for i, input := range inputs {
		if input == "" {
			return nil, fmt.Errorf("embedding input %d is empty", i)
		}
		instances[i] = map[string]string{"content": input}
	}
	parameters := map[string]any{"autoTruncate": false}
	if req.Dimensions != nil {
		if *req.Dimensions <= 0 {
			return nil, fmt.Errorf("embedding dimensions must be positive")
		}
		parameters["outputDimensionality"] = *req.Dimensions
	}
	body, err := json.Marshal(map[string]any{"instances": instances, "parameters": parameters})
	if err != nil {
		return nil, fmt.Errorf("encode Vertex embedding request: %w", err)
	}
	endpoint, err := selectedEndpoint(req.Endpoint, catalogs.EndpointTypeGoogleCloud)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create Vertex embedding request: %w", err)
	}
	if err := c.setHeaders(req.Credential, request); err != nil {
		return nil, fmt.Errorf("apply provider request authentication: %w", err)
	}
	response, err := doRequest(c.httpClient, request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, c.handleError(response)
	}
	var wire vertexEmbeddingResponse
	if err := json.UnmarshalRead(response.Body, &wire); err != nil {
		return nil, fmt.Errorf("decode Vertex embedding response: %w", err)
	}
	return wire.normalized(req, len(inputs))
}

type vertexEmbeddingResponse struct {
	Predictions []struct {
		Embeddings struct {
			Values     []float32 `json:"values"`
			Statistics *struct {
				TokenCount *int `json:"token_count"`
				Truncated  bool `json:"truncated"`
			} `json:"statistics"`
		} `json:"embeddings"`
	} `json:"predictions"`
}

func (wire vertexEmbeddingResponse) normalized(req *EmbeddingsRequest, count int) (*EmbeddingsResponse, error) {
	result := &EmbeddingsResponse{Object: objectList, Model: req.Model, Usage: Usage{decoded: true}}
	result.Data = make([]Embedding, len(wire.Predictions))
	complete := len(wire.Predictions) == count
	var resultErr error
	if !complete {
		resultErr = fmt.Errorf("vertex returned %d embeddings for %d inputs", len(wire.Predictions), count)
	}
	total := 0
	for i, prediction := range wire.Predictions {
		result.Data[i] = Embedding{Object: objectEmbedding, Index: i, Embedding: prediction.Embeddings.Values}
		if len(prediction.Embeddings.Values) == 0 || req.Dimensions != nil && len(prediction.Embeddings.Values) != *req.Dimensions {
			resultErr = fmt.Errorf("vertex returned invalid embedding dimensions")
		}
		statistics := prediction.Embeddings.Statistics
		if statistics == nil || statistics.TokenCount == nil {
			complete = false
			continue
		}
		if statistics.Truncated {
			complete = false
			resultErr = fmt.Errorf("vertex truncated embedding input despite disabled truncation")
			continue
		}
		sum, valid := sumUsageTokens(total, *statistics.TokenCount)
		if !valid {
			complete = false
			continue
		}
		total = sum
	}
	if complete {
		result.Usage.PromptTokens = total
		result.Usage.TotalTokens = total
		result.Usage.setReportedTotals(true, false, true)
	}
	return result, resultErr
}
