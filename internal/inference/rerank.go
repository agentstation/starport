package inference

import "errors"

// Reranking scores a list of documents against one query and returns them in
// relevance order. Four providers serve it and no two agree on the wire: one
// names the result count top_n and another names it top_k, one returns the
// ranked list under results and another under data, and one requires the
// document text echoed on every result while another has no field for it.
//
// The canonical shape below carries none of those names. A result points at a
// document by its position in the request, which is the one fact every provider
// agrees on, and the codecs at the edge translate in both directions.

// ErrRerankQueryEmpty reports a rerank request with nothing to rank against.
var ErrRerankQueryEmpty = errors.New("a rerank request needs a query")

// ErrRerankDocumentsEmpty reports a rerank request with nothing to rank.
var ErrRerankDocumentsEmpty = errors.New("a rerank request needs at least one document")

// RerankRequest is the canonical rerank request.
type RerankRequest struct {
	Model string
	// Query is the text every document is scored against.
	Query string
	// Documents is the list to rank, in the order the caller supplied. A
	// result names a position in this slice, so its order is part of the
	// request's meaning rather than a presentation detail.
	Documents []string
	// TopN is how many results the caller wants back. Nil asks for all of
	// them. It is a request for fewer results rather than a page: a provider
	// scores every document either way and bills for every document either
	// way.
	TopN *int
	// MaxTokensPerDocument caps how much of each document the provider reads.
	// Nil leaves the provider's own default in place. A provider that exceeds
	// its cap either truncates the document or splits it into chunks it bills
	// separately, so the cap is a cost control as well as a size control.
	MaxTokensPerDocument *int
}

// NewRerankRequest builds a canonical rerank request and refuses the two
// requests that cannot be answered. An empty query scores every document
// against nothing, and an empty document list ranks nothing. Both reach a
// provider as a paid error, so they stop here.
func NewRerankRequest(model, query string, documents []string) (RerankRequest, error) {
	if query == "" {
		return RerankRequest{}, ErrRerankQueryEmpty
	}
	if len(documents) == 0 {
		return RerankRequest{}, ErrRerankDocumentsEmpty
	}
	return RerankRequest{
		Model:     model,
		Query:     query,
		Documents: append([]string(nil), documents...),
	}, nil
}

// Clone returns an independent rerank request copy.
func (r RerankRequest) Clone() RerankRequest {
	clone := r
	clone.Documents = append([]string(nil), r.Documents...)
	clone.TopN = clonePointer(r.TopN)
	clone.MaxTokensPerDocument = clonePointer(r.MaxTokensPerDocument)
	return clone
}

// RerankResult is one scored document.
//
// It holds an index and no text. A copy of the document would double the
// memory a large batch needs, and it would let a response disagree with the
// request that produced it. A codec that has to echo the text reads it back
// out of the request it still holds.
type RerankResult struct {
	// Index is the document's position in the request that produced it.
	Index int
	// RelevanceScore is how well that document answers the query. Providers
	// normalize it to the unit interval, and the gateway does not rescale it.
	RelevanceScore float64
}

// RerankResponse is the canonical rerank response.
type RerankResponse struct {
	Model string
	// Results holds the scored documents in relevance order, highest first.
	// It is shorter than the request when the caller asked for fewer.
	Results []RerankResult
	Usage   Usage
}

// Clone returns an independent rerank response copy.
func (r RerankResponse) Clone() RerankResponse {
	clone := r
	clone.Results = append([]RerankResult(nil), r.Results...)
	return clone
}

// Documents resolves each result back to the text the request carried. A codec
// that has to echo the document calls this rather than storing a second copy,
// and a result that names a position the request does not hold is an error the
// caller sees rather than an empty string it cannot explain.
func (r RerankResponse) Documents(request RerankRequest) ([]string, error) {
	resolved := make([]string, len(r.Results))
	for i, result := range r.Results {
		if result.Index < 0 || result.Index >= len(request.Documents) {
			return nil, ErrRerankResultOutOfRange
		}
		resolved[i] = request.Documents[result.Index]
	}
	return resolved, nil
}

// ErrRerankResultOutOfRange reports a result that names a document position the
// request never held.
var ErrRerankResultOutOfRange = errors.New("a rerank result names a document the request does not hold")
