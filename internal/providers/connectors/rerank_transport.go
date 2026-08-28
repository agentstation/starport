package connectors

import (
	"context"
	"errors"

	"github.com/agentstation/starmap/pkg/catalogs"
)

// ErrRerankOptionUnsupported reports a rerank field the selected provider has
// no wire name for. A dropped field is worse than a refusal here, because the
// two fields a caller can set are both cost controls: a silently dropped cap
// reads as an answer the caller paid the full price for.
var ErrRerankOptionUnsupported = errors.New("provider rerank transport does not support this option")

// RerankRequest is one rerank call at the connector seam. Reranking has no
// standard wire shape: one provider names the result count top_n and another
// names it top_k, one caps each document in tokens and another only switches
// truncation on or off. None of those words appear here. Each transport writes
// its own body from these fields, and a field the provider cannot express is
// refused rather than dropped.
type RerankRequest struct {
	MediaTarget
	// Query is the text every document is scored against.
	Query string
	// Documents is the list to rank, in the order the caller supplied. A
	// result names a position in this slice.
	Documents []string
	// TopN is how many results to return. Nil asks for all of them.
	TopN *int
	// MaxTokensPerDocument caps how much of each document the provider reads.
	// Nil leaves the provider default in place.
	MaxTokensPerDocument *int
}

// RerankResult is one scored document. It names a position in the request
// rather than carrying a copy of the text, so a large batch is held once and a
// response cannot disagree with the request that produced it.
type RerankResult struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
}

// RerankResponse is the provider answer to a rerank call.
type RerankResponse struct {
	Results []RerankResult `json:"results"`
	// SearchUnits is the count a provider that bills by search unit reported.
	// A provider that bills by token reports none, and the cost seam reads the
	// offering's own basis rather than guessing from which field arrived.
	SearchUnits int         `json:"search_units,omitempty"`
	Usage       *MediaUsage `json:"usage,omitempty"`
}

// Reranker is the narrow optional interface a transport implements to serve
// the rerank operation. Connector does not carry it. Two of the compiled
// transports serve reranking and the rest serve none of it, so a method on
// Connector would make five transports answer a call they cannot perform and
// would stop the compiler reporting the difference.
type Reranker interface {
	Rerank(ctx context.Context, request *RerankRequest) (*RerankResponse, error)
}

// RerankerFor returns the rerank transport a route selected. It reports false
// for a connector whose compiled transport does not serve the operation, so
// the caller refuses before it spends a credential.
func RerankerFor(
	connector Connector,
	endpointType catalogs.EndpointType,
) (Reranker, bool) {
	transport, found := selectTransport(connector, endpointType)
	if !found {
		return nil, false
	}
	reranker, implemented := transport.(Reranker)
	return reranker, implemented
}
