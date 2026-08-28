package openrouter

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/agentstation/starport/internal/inference"
)

// OpenRouter publishes POST /api/v1/rerank, which decision RNK-D9 records
// after RNK0 read the live schema. It resells the Cohere request shape and
// changes three things. A document is a string or an object that carries text
// or an image. A provider preference block selects who serves the call. Every
// result carries the echoed document, which the schema marks required rather
// than optional.
//
// The third one is why this codec takes the request when it writes the answer.
// The canonical response holds an index and a score, so the text comes back
// out of the request the gateway already holds rather than from a second copy
// carried through routing, the cache, and the usage record.

// ErrRerankDocumentUnsupported reports a structured document this gateway
// cannot rank. A rerank provider scores text. An image document reaches the
// wire as a picture the transport has no field for, and ranking its caption
// would answer a question the caller did not ask.
var ErrRerankDocumentUnsupported = errors.New("openrouter rerank document is not text")

// RerankRequest is the OpenRouter rerank wire request.
type RerankRequest struct {
	Model     string            `json:"model"`
	Query     string            `json:"query"`
	Documents []json.RawMessage `json:"documents"`
	TopN      *int              `json:"top_n,omitempty"`
	// MaxTokensPerDoc caps how much of each document the provider reads.
	MaxTokensPerDoc *int `json:"max_tokens_per_doc,omitempty"`
	// Provider carries the same preference block every other OpenRouter route
	// accepts, so one validator covers all of them.
	Provider *ProviderPreferences `json:"provider,omitempty"`
}

// RerankResult is one scored document. OpenRouter marks the document
// required, so this codec always echoes it.
type RerankResult struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
	Document       string  `json:"document"`
}

// RerankResponse is the OpenRouter rerank wire response.
type RerankResponse struct {
	ID       string         `json:"id,omitempty"`
	Model    string         `json:"model"`
	Provider string         `json:"provider,omitempty"`
	Results  []RerankResult `json:"results"`
	Usage    RerankUsage    `json:"usage"`
}

// RerankUsage reports what a rerank turn consumed. Providers split on the
// unit: one bills a search unit and another bills tokens, so the answer states
// whichever one the provider reported and omits the other.
type RerankUsage struct {
	SearchUnits int `json:"search_units,omitempty"`
	TotalTokens int `json:"total_tokens,omitempty"`
	// Cost is what the gateway charged, in US dollars. OpenRouter states it on
	// every rerank answer. A turn the catalog could not price omits it rather
	// than reporting zero, because zero is a price a caller would believe.
	Cost *float64 `json:"cost,omitempty"`
}

// RerankDecoding is one decoded OpenRouter rerank request with the unkept
// promises the drop-in contract requires the answer to report.
type RerankDecoding struct {
	Request inference.RerankRequest
	// UnenforcedProviderFields names documented provider fields this request
	// used that Starport accepts without enforcing. Accepting a documented
	// field is the contract. Staying quiet about an unkept one is not.
	UnenforcedProviderFields []string
}

// DecodeRerank decodes one strict OpenRouter rerank request.
func DecodeRerank(reader io.Reader) (RerankDecoding, error) {
	var wire RerankRequest
	if err := decodeStrict(reader, &wire); err != nil {
		return RerankDecoding{}, err
	}
	if err := validateProviderPreferences(wire.Provider); err != nil {
		return RerankDecoding{}, err
	}
	documents := make([]string, len(wire.Documents))
	for index, raw := range wire.Documents {
		text, err := decodeRerankDocument(raw)
		if err != nil {
			return RerankDecoding{}, fmt.Errorf("document %d: %w", index, err)
		}
		documents[index] = text
	}
	request, err := inference.NewRerankRequest(wire.Model, wire.Query, documents)
	if err != nil {
		return RerankDecoding{}, err
	}
	request.TopN = wire.TopN
	request.MaxTokensPerDocument = wire.MaxTokensPerDoc
	return RerankDecoding{
		Request:                  request,
		UnenforcedProviderFields: unenforcedProviderFields(wire.Provider),
	}, nil
}

// decodeRerankDocument reads one document, which the schema states as a plain
// string or as an object that names its own kind.
func decodeRerankDocument(raw json.RawMessage) (string, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	var structured struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &structured); err != nil {
		return "", err
	}
	if structured.Type != "" && structured.Type != contentTypeText {
		return "", fmt.Errorf("%w: %s", ErrRerankDocumentUnsupported, structured.Type)
	}
	if structured.Text == "" {
		return "", ErrRerankDocumentUnsupported
	}
	return structured.Text, nil
}

// EncodeRerank writes one canonical rerank answer in the OpenRouter shape. The
// schema requires the document on every result, so the request is not optional
// here the way it is on the /v1 route.
//
// The cost comes from the gateway rather than from the provider. A rerank
// provider reports the units it billed and no money at all, so the dollar
// figure is the gateway's own accounting and the caller gets it only when the
// catalog priced the turn.
func EncodeRerank(
	response inference.RerankResponse,
	request inference.RerankRequest,
	provider string,
	costUSD *float64,
) (RerankResponse, error) {
	if err := response.Validate(request); err != nil {
		return RerankResponse{}, err
	}
	documents, err := response.Documents(request)
	if err != nil {
		return RerankResponse{}, err
	}
	results := make([]RerankResult, len(response.Results))
	for index, result := range response.Results {
		results[index] = RerankResult{
			Index:          result.Index,
			RelevanceScore: result.RelevanceScore,
			Document:       documents[index],
		}
	}
	return RerankResponse{
		Model:    responseModel(response.Model, request.Model),
		Provider: provider,
		Results:  results,
		Usage: RerankUsage{
			SearchUnits: response.Usage.SearchUnits,
			TotalTokens: response.Usage.TotalTokens,
			Cost:        costUSD,
		},
	}, nil
}
