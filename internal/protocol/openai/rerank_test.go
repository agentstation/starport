package openai

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/inference"
)

// rerankRequestBody is the request Cohere published and Jina, Voyage AI, and
// the open-source inference servers copied. A caller reaches this gateway by
// changing a base URL, so the body it already sends has to decode here.
const rerankRequestBody = `{
  "model": "rerank-v3.5",
  "query": "which provider serves reranking",
  "documents": ["a poem about the sea", "Cohere serves reranking", "Voyage AI serves reranking"],
  "top_n": 2,
  "max_tokens_per_doc": 512
}`

func rerankAnswer() inference.RerankResponse {
	return inference.RerankResponse{
		Model: "rerank-v3.5",
		Results: []inference.RerankResult{
			{Index: 1, RelevanceScore: 0.91},
			{Index: 2, RelevanceScore: 0.42},
		},
		Usage: inference.Usage{TotalTokens: 38, SearchUnits: 1},
	}
}

// TestTheRerankCodecRoundTripsThePublishedShape holds condition RNK-V08. Every
// field the published request carries has to survive the decode, and the
// answer has to carry the two facts a caller ranks on plus the unit it was
// billed in.
func TestTheRerankCodecRoundTripsThePublishedShape(t *testing.T) {
	decoding, err := DecodeRerank(strings.NewReader(rerankRequestBody))
	require.NoError(t, err)

	require.Equal(t, "rerank-v3.5", decoding.Request.Model)
	require.Equal(t, "which provider serves reranking", decoding.Request.Query)
	require.Len(t, decoding.Request.Documents, 3)
	require.Equal(t, "Cohere serves reranking", decoding.Request.Documents[1])
	require.NotNil(t, decoding.Request.TopN)
	require.Equal(t, 2, *decoding.Request.TopN)
	require.NotNil(t, decoding.Request.MaxTokensPerDocument)
	require.Equal(t, 512, *decoding.Request.MaxTokensPerDocument)
	require.False(t, decoding.ReturnDocuments)

	encoded, err := EncodeRerank(rerankAnswer(), decoding)
	require.NoError(t, err)
	require.Equal(t, "list", encoded.Object)
	require.Equal(t, "rerank-v3.5", encoded.Model)
	require.Equal(t, 38, encoded.Usage.TotalTokens)
	require.Equal(t, 1, encoded.Usage.SearchUnits)
	require.Equal(t, []RerankResult{
		{Index: 1, RelevanceScore: 0.91},
		{Index: 2, RelevanceScore: 0.42},
	}, encoded.Results)

	// The wire names are the contract. A client written against Cohere reads
	// relevance_score off each result and nothing else, so a rename that
	// compiled would still break every caller.
	written, err := json.Marshal(encoded)
	require.NoError(t, err)
	require.JSONEq(t, `{
	  "object": "list",
	  "model": "rerank-v3.5",
	  "results": [
	    {"index": 1, "relevance_score": 0.91},
	    {"index": 2, "relevance_score": 0.42}
	  ],
	  "usage": {"total_tokens": 38, "search_units": 1}
	}`, string(written))
}

// TestAnEchoedDocumentComesFromTheRequest holds half of condition RNK-V09. The
// canonical response carries no text, so the only copy the gateway holds is
// the request's. An echo that came from anywhere else would be a second copy
// that could disagree with the first.
func TestAnEchoedDocumentComesFromTheRequest(t *testing.T) {
	body := strings.Replace(
		rerankRequestBody, `"top_n": 2,`, `"top_n": 2, "return_documents": true,`, 1,
	)
	decoding, err := DecodeRerank(strings.NewReader(body))
	require.NoError(t, err)
	require.True(t, decoding.ReturnDocuments)

	encoded, err := EncodeRerank(rerankAnswer(), decoding)
	require.NoError(t, err)
	require.Len(t, encoded.Results, 2)
	require.NotNil(t, encoded.Results[0].Document)
	require.Equal(t, "Cohere serves reranking", *encoded.Results[0].Document)
	require.NotNil(t, encoded.Results[1].Document)
	require.Equal(t, "Voyage AI serves reranking", *encoded.Results[1].Document)

	// The same answer written for a caller that did not ask names no text at
	// all. A gateway that echoed it anyway would tell every caller it stored
	// the batch.
	quiet, err := DecodeRerank(strings.NewReader(rerankRequestBody))
	require.NoError(t, err)
	silent, err := EncodeRerank(rerankAnswer(), quiet)
	require.NoError(t, err)
	for _, result := range silent.Results {
		require.Nil(t, result.Document)
	}
}

// TestTheRerankCodecRefusesAnAnswerItCannotPublish holds the other half of
// condition RNK-V09. Both faults produce an answer that reads as ordinary,
// which is why the codec refuses rather than repairs.
func TestTheRerankCodecRefusesAnAnswerItCannotPublish(t *testing.T) {
	decoding, err := DecodeRerank(strings.NewReader(rerankRequestBody))
	require.NoError(t, err)

	for _, testCase := range []struct {
		name    string
		results []inference.RerankResult
		wantErr error
	}{
		{
			name:    "a score above one",
			results: []inference.RerankResult{{Index: 0, RelevanceScore: 1.4}},
			wantErr: inference.ErrRerankScoreOutOfRange,
		},
		{
			name:    "a negative score",
			results: []inference.RerankResult{{Index: 0, RelevanceScore: -0.1}},
			wantErr: inference.ErrRerankScoreOutOfRange,
		},
		{
			name:    "an index the request never held",
			results: []inference.RerankResult{{Index: 7, RelevanceScore: 0.5}},
			wantErr: inference.ErrRerankResultOutOfRange,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			answer := rerankAnswer()
			answer.Results = testCase.results
			_, err := EncodeRerank(answer, decoding)
			require.ErrorIs(t, err, testCase.wantErr)
		})
	}
}

// TestTheRerankCodecReportsAMisspelledField holds the strict-decode rule the
// chat route already follows. The two optional fields a caller sets are both
// cost controls, so a misspelling that decoded silently would bill the
// provider default and read as the caller's own request.
func TestTheRerankCodecReportsAMisspelledField(t *testing.T) {
	for _, body := range []string{
		`{"model":"m","query":"q","documents":["d"],"topn":2}`,
		`{"model":"m","query":"q","documents":["d"],"return_document":true}`,
	} {
		_, err := DecodeRerank(strings.NewReader(body))
		require.Error(t, err)
		require.Contains(t, err.Error(), "unknown field")
	}
}

// TestTheRerankCodecRefusesARequestNoProviderCanAnswer keeps the two paid
// errors out of the wire. A provider bills for both and answers neither.
func TestTheRerankCodecRefusesARequestNoProviderCanAnswer(t *testing.T) {
	_, err := DecodeRerank(strings.NewReader(`{"model":"m","query":"","documents":["d"]}`))
	require.ErrorIs(t, err, inference.ErrRerankQueryEmpty)

	_, err = DecodeRerank(strings.NewReader(`{"model":"m","query":"q","documents":[]}`))
	require.ErrorIs(t, err, inference.ErrRerankDocumentsEmpty)
}

// TestADocumentListLongerThanTheOfferingIsRefused holds the bound the catalog
// states. A reranker refuses the whole batch rather than ranking a prefix of
// it, so a caller that exceeds the bound pays for a round trip that could not
// have succeeded.
func TestADocumentListLongerThanTheOfferingIsRefused(t *testing.T) {
	decoding, err := DecodeRerank(strings.NewReader(rerankRequestBody))
	require.NoError(t, err)

	require.NoError(t, decoding.Request.CheckDocumentBound(3))
	require.NoError(t, decoding.Request.CheckDocumentBound(1000))

	err = decoding.Request.CheckDocumentBound(2)
	require.ErrorIs(t, err, inference.ErrRerankDocumentsExceedBound)
	require.Contains(t, err.Error(), "3 documents against a bound of 2")

	// A catalog that publishes no document count states no bound, which is
	// not the same as a bound of zero.
	require.NoError(t, decoding.Request.CheckDocumentBound(0))
}
