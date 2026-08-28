package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"

	"github.com/agentstation/starmap/pkg/catalogs"
)

// Reranking is served by providers that serve nothing else. Neither Cohere nor
// Voyage AI publishes a chat or an embeddings route this gateway compiles, so
// each one arrives as a transport that implements the rerank operation and
// refuses the rest. The two wire shapes differ enough that a shared body would
// have to branch on the provider, and they agree on enough that two whole
// transports would repeat the same request: the split below is one HTTP path
// and two codecs.

// errRerankRequestMissing reports a rerank call with no request at all.
var errRerankRequestMissing = errors.New("rerank request is required")

// rerankErrorBodyLimit bounds how much of a provider's error body is read. A
// rejection is a short document, and an unbounded read of a failing endpoint
// is a second failure.
const rerankErrorBodyLimit = 64 << 10

// rerankCodec writes one provider's rerank body and reads its answer. It holds
// every wire word for that provider, so RerankRequest carries none.
type rerankCodec interface {
	// endpointType is the catalog protocol this codec speaks.
	endpointType() catalogs.EndpointType
	// encode returns the body to send, or refuses a field the provider has no
	// name for.
	encode(request *RerankRequest) (any, error)
	// decode reads the provider answer into the canonical response.
	decode(body io.Reader) (*RerankResponse, error)
}

// rerankConnector is the one HTTP path both rerank protocols take.
type rerankConnector struct {
	providerID catalogs.ProviderID
	codec      rerankCodec
	httpClient *http.Client
}

func newRerankConnector(
	providerID catalogs.ProviderID,
	codec rerankCodec,
	config ProviderConfig,
) (*rerankConnector, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return &rerankConnector{
		providerID: providerID,
		codec:      codec,
		httpClient: newProviderHTTPClient(config),
	}, nil
}

func newCohereConnector(providerID catalogs.ProviderID, config ProviderConfig) (Connector, error) {
	return newRerankConnector(providerID, cohereRerankCodec{}, config)
}

func newVoyageConnector(providerID catalogs.ProviderID, config ProviderConfig) (Connector, error) {
	return newRerankConnector(providerID, voyageRerankCodec{}, config)
}

// Name returns the provider name.
func (c *rerankConnector) Name() string { return string(c.providerID) }

// Close releases the connector's idle connections.
func (c *rerankConnector) Close() error {
	c.httpClient.CloseIdleConnections()
	return nil
}

// Chat refuses. A rerank provider generates nothing, and its descriptor
// declares no chat operation, so the planner never selects this transport for
// a chat turn. The refusal names the reason rather than reaching the wire.
func (c *rerankConnector) Chat(context.Context, *ChatRequest) (*ChatResponse, error) {
	return nil, c.unsupported(catalogs.ProviderOperationChatCompletions)
}

// ChatStream refuses for the same reason Chat refuses.
func (c *rerankConnector) ChatStream(context.Context, *ChatRequest) (ChatStream, error) {
	return nil, c.unsupported(catalogs.ProviderOperationChatCompletions)
}

// Embeddings refuses. Both rerank providers publish an embeddings route, and
// neither one is compiled here: an offering this gateway cannot price and
// cannot route is not reachable, so the honest answer is the refusal.
func (c *rerankConnector) Embeddings(
	context.Context,
	*EmbeddingsRequest,
) (*EmbeddingsResponse, error) {
	return nil, c.unsupported(catalogs.ProviderOperationEmbeddings)
}

func (c *rerankConnector) unsupported(operation catalogs.ProviderOperation) error {
	return fmt.Errorf(
		"%s %s: %w", c.providerID, operation, ErrTransportOperationUnsupported,
	)
}

// Rerank scores the request's documents against its query at the selected
// endpoint.
func (c *rerankConnector) Rerank(
	ctx context.Context,
	request *RerankRequest,
) (*RerankResponse, error) {
	if request == nil || request.Query == "" || len(request.Documents) == 0 {
		return nil, errRerankRequestMissing
	}
	endpoint, err := selectedEndpoint(request.Endpoint, c.codec.endpointType())
	if err != nil {
		return nil, err
	}
	payload, err := c.codec.encode(request)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	if err := applyRequestAuthentication(request.Credential, httpRequest); err != nil {
		return nil, fmt.Errorf("apply provider request authentication: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")

	response, err := doRequest(c.httpClient, httpRequest)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return nil, c.rerankError(response)
	}
	answer, err := c.codec.decode(response.Body)
	if err != nil {
		return nil, err
	}
	// A result that names a position the request never held would resolve to
	// the wrong document, or to none. It is a provider defect, and the caller
	// has to see it rather than read a confident answer built from it.
	for _, result := range answer.Results {
		if result.Index < 0 || result.Index >= len(request.Documents) {
			return nil, fmt.Errorf(
				"%s returned rerank result %d for %d documents",
				c.providerID, result.Index, len(request.Documents),
			)
		}
	}
	return answer, nil
}

// rerankError reads a rejection from either rerank protocol. Cohere states the
// reason under message and Voyage AI states it under detail, so one reader
// covers both rather than a near-copy per provider.
func (c *rerankConnector) rerankError(response *http.Response) error {
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, rerankErrorBodyLimit))
	apiError := &APIError{
		StatusCode: response.StatusCode,
		Provider:   string(c.providerID),
	}
	if readErr != nil {
		apiError.Message = "failed to read error response"
		return apiError
	}

	var payload struct {
		Message string          `json:"message"`
		Detail  json.RawMessage `json:"detail"`
		Error   struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		apiError.Message = strings.TrimSpace(string(raw))
		return apiError
	}
	apiError.Type = payload.Error.Type
	apiError.Code = payload.Error.Code
	apiError.Message = firstNonEmpty(payload.Error.Message, payload.Message)
	if apiError.Message == "" {
		var detail string
		if err := json.Unmarshal(payload.Detail, &detail); err == nil {
			apiError.Message = detail
		} else {
			apiError.Message = strings.TrimSpace(string(raw))
		}
	}
	return apiError
}

// cohereRerankCodec speaks the Cohere v2 rerank shape. OpenRouter copied it,
// so it is the closest thing reranking has to a standard.
type cohereRerankCodec struct{}

func (cohereRerankCodec) endpointType() catalogs.EndpointType {
	return catalogs.EndpointTypeCohere
}

func (cohereRerankCodec) encode(request *RerankRequest) (any, error) {
	return struct {
		Model           string   `json:"model"`
		Query           string   `json:"query"`
		Documents       []string `json:"documents"`
		TopN            *int     `json:"top_n,omitempty"`
		MaxTokensPerDoc *int     `json:"max_tokens_per_doc,omitempty"`
	}{
		Model:           request.Model,
		Query:           request.Query,
		Documents:       request.Documents,
		TopN:            request.TopN,
		MaxTokensPerDoc: request.MaxTokensPerDocument,
	}, nil
}

func (cohereRerankCodec) decode(body io.Reader) (*RerankResponse, error) {
	var answer struct {
		Results []RerankResult `json:"results"`
		Meta    struct {
			BilledUnits struct {
				SearchUnits float64 `json:"search_units"`
			} `json:"billed_units"`
			Tokens struct {
				InputTokens  float64 `json:"input_tokens"`
				OutputTokens float64 `json:"output_tokens"`
			} `json:"tokens"`
		} `json:"meta"`
	}
	if err := json.NewDecoder(body).Decode(&answer); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	response := &RerankResponse{
		Results:     answer.Results,
		SearchUnits: roundedCount(answer.Meta.BilledUnits.SearchUnits),
	}
	input := roundedCount(answer.Meta.Tokens.InputTokens)
	output := roundedCount(answer.Meta.Tokens.OutputTokens)
	if input > 0 || output > 0 {
		response.Usage = &MediaUsage{
			InputTokens:  input,
			OutputTokens: output,
			TotalTokens:  input + output,
		}
	}
	return response, nil
}

// voyageRerankCodec speaks the Voyage AI rerank shape. It names the result
// count top_k, returns the ranked list under data, and bills tokens rather
// than search units.
type voyageRerankCodec struct{}

func (voyageRerankCodec) endpointType() catalogs.EndpointType {
	return catalogs.EndpointTypeVoyage
}

func (voyageRerankCodec) encode(request *RerankRequest) (any, error) {
	// Voyage AI switches truncation on or off and states no per-document token
	// cap. Sending the request without the cap would bill the caller for the
	// whole document after it asked for less, so the request is refused.
	if request.MaxTokensPerDocument != nil {
		return nil, fmt.Errorf(
			"%w: voyage rerank has no per-document token cap",
			ErrRerankOptionUnsupported,
		)
	}
	return struct {
		Model     string   `json:"model"`
		Query     string   `json:"query"`
		Documents []string `json:"documents"`
		TopK      *int     `json:"top_k,omitempty"`
	}{
		Model:     request.Model,
		Query:     request.Query,
		Documents: request.Documents,
		TopK:      request.TopN,
	}, nil
}

func (voyageRerankCodec) decode(body io.Reader) (*RerankResponse, error) {
	var answer struct {
		Data  []RerankResult `json:"data"`
		Usage struct {
			TotalTokens float64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(body).Decode(&answer); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	response := &RerankResponse{Results: answer.Data}
	if total := roundedCount(answer.Usage.TotalTokens); total > 0 {
		response.Usage = &MediaUsage{TotalTokens: total}
	}
	return response, nil
}

// roundedCount reads a unit count a provider reports as a JSON number. Cohere
// reports whole units as floats, so the count is rounded rather than truncated
// and a negative report is treated as none.
func roundedCount(value float64) int {
	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return int(math.Round(value))
}
