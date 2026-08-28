package openai

import (
	"io"

	"github.com/agentstation/starport/internal/inference"
)

// OpenAI publishes no rerank route. Cohere published one, and Jina, Voyage AI,
// and the open-source inference servers converged on its request shape, so a
// caller reaches this gateway by changing a base URL. Decision RNK-D1 records
// the choice.
//
// The wire carries one field the canonical request does not: return_documents
// asks for the ranked text echoed beside each score. It changes how the answer
// is written rather than what it holds, so it travels beside the canonical
// request rather than inside it, and the encoder reads the text back out of
// the request that produced the answer.

// RerankRequest is the rerank wire request served at POST /v1/rerank.
type RerankRequest struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	// TopN asks for fewer results. It is not a page: a provider scores every
	// document either way, and it bills for every document either way.
	TopN *int `json:"top_n,omitempty"`
	// ReturnDocuments asks for the ranked text echoed on every result.
	ReturnDocuments bool `json:"return_documents,omitempty"`
	// MaxTokensPerDoc caps how much of each document the provider reads. Not
	// every provider has a wire name for it, and the transport refuses the
	// field rather than dropping it when it cannot say it.
	MaxTokensPerDoc *int `json:"max_tokens_per_doc,omitempty"`
}

// RerankDecoding is one decoded rerank request with the wire-only options that
// change how its answer is written. Holding them apart is what keeps the
// canonical request free of a presentation flag that no router, cache, or
// usage record has any use for.
type RerankDecoding struct {
	Request         inference.RerankRequest
	ReturnDocuments bool
}

// RerankResult is one scored document on the wire. The document field is
// present only when the caller asked for it.
type RerankResult struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
	Document       *string `json:"document,omitempty"`
}

// RerankResponse is the rerank wire response.
type RerankResponse struct {
	Object  string         `json:"object"`
	Model   string         `json:"model"`
	Results []RerankResult `json:"results"`
	Usage   RerankUsage    `json:"usage"`
}

// RerankUsage reports what a rerank turn consumed. Providers split on the
// unit: one bills a search unit and another bills tokens, so the answer states
// whichever one the provider reported and omits the other.
type RerankUsage struct {
	TotalTokens int `json:"total_tokens,omitempty"`
	SearchUnits int `json:"search_units,omitempty"`
}

// DecodeRerank decodes one strict rerank request. An unknown field fails the
// same way it fails on the chat route, because a caller that misspells a cost
// control has to hear about it rather than pay the default.
func DecodeRerank(reader io.Reader) (RerankDecoding, error) {
	var wire RerankRequest
	if err := decodeStrict(reader, &wire); err != nil {
		return RerankDecoding{}, err
	}
	request, err := inference.NewRerankRequest(wire.Model, wire.Query, wire.Documents)
	if err != nil {
		return RerankDecoding{}, err
	}
	request.TopN = wire.TopN
	request.MaxTokensPerDocument = wire.MaxTokensPerDoc
	return RerankDecoding{Request: request, ReturnDocuments: wire.ReturnDocuments}, nil
}

// EncodeRerank writes one canonical rerank answer. It takes the decoding
// rather than the response alone: an echoed document comes from the request,
// which is the only copy of the text the gateway holds.
func EncodeRerank(
	response inference.RerankResponse,
	decoding RerankDecoding,
) (RerankResponse, error) {
	if err := response.Validate(decoding.Request); err != nil {
		return RerankResponse{}, err
	}
	var documents []string
	if decoding.ReturnDocuments {
		resolved, err := response.Documents(decoding.Request)
		if err != nil {
			return RerankResponse{}, err
		}
		documents = resolved
	}

	results := make([]RerankResult, len(response.Results))
	for index, result := range response.Results {
		results[index] = RerankResult{
			Index:          result.Index,
			RelevanceScore: result.RelevanceScore,
		}
		if documents != nil {
			results[index].Document = &documents[index]
		}
	}
	return RerankResponse{
		Object:  ListObject,
		Model:   responseModel(response.Model, decoding.Request.Model),
		Results: results,
		Usage: RerankUsage{
			TotalTokens: response.Usage.TotalTokens,
			SearchUnits: response.Usage.SearchUnits,
		},
	}, nil
}
