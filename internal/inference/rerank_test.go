package inference

import (
	"errors"
	"go/build"
	"strings"
	"testing"
)

// TestRerankResultPointsAtTheRequest holds the shape decision this operation
// turns on. Two of the four provider wire shapes echo the document text on
// every result, so the tempting canonical form carries a copy. A copy doubles
// the memory a thousand-document batch needs, and it lets a response disagree
// with the request that produced it. The index is the fact every provider
// agrees on, and the text comes back out of the request.
func TestRerankResultPointsAtTheRequest(t *testing.T) {
	t.Parallel()

	request, err := NewRerankRequest("cohere/rerank-v3.5", "who ships reranking", []string{
		"Cohere serves reranking.",
		"A poem about the sea.",
		"Voyage AI serves reranking.",
	})
	if err != nil {
		t.Fatalf("NewRerankRequest: %v", err)
	}
	response := RerankResponse{
		Model: request.Model,
		Results: []RerankResult{
			{Index: 2, RelevanceScore: 0.91},
			{Index: 0, RelevanceScore: 0.88},
		},
	}

	documents, err := response.Documents(request)
	if err != nil {
		t.Fatalf("Documents: %v", err)
	}
	want := []string{"Voyage AI serves reranking.", "Cohere serves reranking."}
	for i, document := range want {
		if documents[i] != document {
			t.Fatalf("document %d = %q, want %q", i, documents[i], document)
		}
	}

	// A provider that returns a position the request never held is a bug the
	// caller has to see. Resolving it to an empty string would look like a
	// document that happened to be blank.
	response.Results[0].Index = 3
	if _, err := response.Documents(request); !errors.Is(err, ErrRerankResultOutOfRange) {
		t.Fatalf("out of range error = %v, want ErrRerankResultOutOfRange", err)
	}
}

// TestRerankRequestRefusesWhatCannotBeRanked keeps two paid errors out of the
// provider. An empty query scores every document against nothing, and an empty
// document list ranks nothing. A provider bills for the round trip either way.
func TestRerankRequestRefusesWhatCannotBeRanked(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name      string
		query     string
		documents []string
		want      error
	}{
		{name: "no query", documents: []string{"a document"}, want: ErrRerankQueryEmpty},
		{name: "no documents", query: "a query", want: ErrRerankDocumentsEmpty},
		{name: "empty document slice", query: "a query", documents: []string{}, want: ErrRerankDocumentsEmpty},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewRerankRequest("cohere/rerank-v3.5", testCase.query, testCase.documents)
			if !errors.Is(err, testCase.want) {
				t.Fatalf("error = %v, want %v", err, testCase.want)
			}
		})
	}
}

// TestRerankRequestCloneSharesNothing holds the rule every canonical type in
// this package holds. A retried attempt clones the request it is about to send
// again, and a shared slice would let the second attempt see the first one's
// edits.
func TestRerankRequestCloneSharesNothing(t *testing.T) {
	t.Parallel()

	topN := 3
	tokenCap := 4096
	request, err := NewRerankRequest("cohere/rerank-v3.5", "a query", []string{"first", "second"})
	if err != nil {
		t.Fatalf("NewRerankRequest: %v", err)
	}
	request.TopN = &topN
	request.MaxTokensPerDocument = &tokenCap

	clone := request.Clone()
	request.Documents[0] = "changed"
	*request.TopN = 99
	*request.MaxTokensPerDocument = 99

	if clone.Documents[0] != "first" {
		t.Fatalf("cloned document = %q", clone.Documents[0])
	}
	if *clone.TopN != 3 {
		t.Fatalf("cloned top n = %d", *clone.TopN)
	}
	if *clone.MaxTokensPerDocument != 4096 {
		t.Fatalf("cloned token cap = %d", *clone.MaxTokensPerDocument)
	}

	response := RerankResponse{Results: []RerankResult{{Index: 0, RelevanceScore: 0.5}}}
	clonedResponse := response.Clone()
	response.Results[0].Index = 7
	if clonedResponse.Results[0].Index != 0 {
		t.Fatalf("cloned result index = %d", clonedResponse.Results[0].Index)
	}
}

// TestCanonicalPackageNamesNoProtocol reads this package's own import list. The
// canonical shape exists so that no layer below the codec carries a provider
// name, and the fastest way to break that is an import that drags one in. The
// check is on the whole package rather than the rerank file, because the rule
// is the package's rule.
func TestCanonicalPackageNamesNoProtocol(t *testing.T) {
	t.Parallel()

	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatalf("ImportDir: %v", err)
	}
	for _, imported := range append(append([]string(nil), pkg.Imports...), pkg.TestImports...) {
		if strings.Contains(imported, "/internal/protocol/") ||
			strings.Contains(imported, "/internal/providers/") {
			t.Errorf("the canonical package imports %q", imported)
		}
	}
}
