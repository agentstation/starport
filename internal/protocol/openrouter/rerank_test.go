package openrouter

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/inference"
)

// openRouterRerankBody is the request the published OpenRouter schema states.
// It differs from the Cohere-shaped one on the /v1 route in two visible ways:
// a document is a string or an object that names its own kind, and a provider
// preference block selects who serves the call.
const openRouterRerankBody = `{
  "model": "cohere/rerank-v3.5",
  "query": "which provider serves reranking",
  "documents": [
    "a poem about the sea",
    {"type": "text", "text": "Cohere serves reranking"},
    {"text": "Voyage AI serves reranking"}
  ],
  "top_n": 2,
  "provider": {"sort": "price", "require_parameters": true}
}`

func openRouterRerankAnswer() inference.RerankResponse {
	return inference.RerankResponse{
		Model: "cohere/rerank-v3.5",
		Results: []inference.RerankResult{
			{Index: 1, RelevanceScore: 0.91},
			{Index: 2, RelevanceScore: 0.42},
		},
		Usage: inference.Usage{SearchUnits: 1, TotalTokens: 38},
	}
}

// TestTheOpenRouterRerankCodecEchoesEveryDocument holds condition RNK-V21. The
// published schema marks the document required on every result, so an answer
// that omitted it would fail a client that reads the field without checking.
// The text comes back out of the request, which is the only copy the gateway
// holds.
func TestTheOpenRouterRerankCodecEchoesEveryDocument(t *testing.T) {
	decoding, err := DecodeRerank(strings.NewReader(openRouterRerankBody))
	require.NoError(t, err)
	require.Equal(t, "cohere/rerank-v3.5", decoding.Request.Model)
	require.Equal(t, []string{
		"a poem about the sea",
		"Cohere serves reranking",
		"Voyage AI serves reranking",
	}, decoding.Request.Documents)
	require.NotNil(t, decoding.Request.TopN)
	require.Equal(t, 2, *decoding.Request.TopN)

	cost := 0.0025
	encoded, err := EncodeRerank(openRouterRerankAnswer(), decoding.Request, "cohere", &cost)
	require.NoError(t, err)
	require.Equal(t, "cohere/rerank-v3.5", encoded.Model)
	require.Equal(t, "cohere", encoded.Provider)
	require.Equal(t, []RerankResult{
		{Index: 1, RelevanceScore: 0.91, Document: "Cohere serves reranking"},
		{Index: 2, RelevanceScore: 0.42, Document: "Voyage AI serves reranking"},
	}, encoded.Results)

	// The unit split is the reason usage carries two counts. A provider that
	// bills a search unit reports no token total, and the answer states
	// whichever one arrived rather than converting between them.
	require.Equal(t, 1, encoded.Usage.SearchUnits)
	require.Equal(t, 38, encoded.Usage.TotalTokens)

	// The cost is the gateway's own, because a rerank provider reports the
	// units it billed and no money at all.
	require.NotNil(t, encoded.Usage.Cost)
	require.InDelta(t, 0.0025, *encoded.Usage.Cost, 1e-12)

	written, err := json.Marshal(encoded)
	require.NoError(t, err)
	require.JSONEq(t, `{
	  "model": "cohere/rerank-v3.5",
	  "provider": "cohere",
	  "results": [
	    {"index": 1, "relevance_score": 0.91, "document": "Cohere serves reranking"},
	    {"index": 2, "relevance_score": 0.42, "document": "Voyage AI serves reranking"}
	  ],
	  "usage": {"search_units": 1, "total_tokens": 38, "cost": 0.0025}
	}`, string(written))

	// A turn the catalog could not price omits the member rather than
	// publishing zero, which a caller would read as a free request.
	unpriced, err := EncodeRerank(openRouterRerankAnswer(), decoding.Request, "cohere", nil)
	require.NoError(t, err)
	written, err = json.Marshal(unpriced)
	require.NoError(t, err)
	require.NotContains(t, string(written), "cost")
}

// TestTheOpenRouterRerankCodecReportsAnUnkeptProviderPromise holds the
// drop-in contract the chat route already holds. Accepting a documented
// provider field is the contract. Staying quiet about one this gateway does
// not act on is not.
func TestTheOpenRouterRerankCodecReportsAnUnkeptProviderPromise(t *testing.T) {
	decoding, err := DecodeRerank(strings.NewReader(openRouterRerankBody))
	require.NoError(t, err)
	require.Equal(t, []string{"require_parameters"}, decoding.UnenforcedProviderFields)

	// A preference block holding only fields the router acts on promises
	// nothing it cannot keep, so the answer reports no unkept promise.
	plain := strings.Replace(
		openRouterRerankBody,
		`{"sort": "price", "require_parameters": true}`,
		`{"sort": "price"}`,
		1,
	)
	quiet, err := DecodeRerank(strings.NewReader(plain))
	require.NoError(t, err)
	require.Empty(t, quiet.UnenforcedProviderFields)
}

// TestTheOpenRouterRerankCodecRefusesAPreferenceItCannotRead keeps an
// unreadable routing preference off the wire. A misspelled sort order would
// otherwise route by whatever the default is and read as the caller's choice.
func TestTheOpenRouterRerankCodecRefusesAPreferenceItCannotRead(t *testing.T) {
	body := strings.Replace(openRouterRerankBody, `"sort": "price"`, `"sort": "cheapest"`, 1)
	_, err := DecodeRerank(strings.NewReader(body))
	require.Error(t, err)
}

// TestTheOpenRouterRerankCodecRefusesADocumentItCannotRank covers the document
// kinds the schema allows and this gateway does not serve. A rerank provider
// scores text, so an image reaches the transport as a picture it has no field
// for, and ranking a caption would answer a question the caller did not ask.
func TestTheOpenRouterRerankCodecRefusesADocumentItCannotRank(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		document string
	}{
		{name: "an image document", document: `{"type": "image_url", "image_url": {"url": "https://example.test/a.png"}}`},
		{name: "an object with no text", document: `{"type": "text"}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			body := strings.Replace(
				openRouterRerankBody, `"a poem about the sea"`, testCase.document, 1,
			)
			_, err := DecodeRerank(strings.NewReader(body))
			require.ErrorIs(t, err, ErrRerankDocumentUnsupported)
			require.Contains(t, err.Error(), "document 0")
		})
	}
}

// TestTheOpenRouterRerankCodecRefusesAnAnswerItCannotPublish holds the same
// two refusals the /v1 codec holds. An index outside the request resolves to
// the wrong document, and a score outside the unit interval sorts against
// every other provider's scale.
func TestTheOpenRouterRerankCodecRefusesAnAnswerItCannotPublish(t *testing.T) {
	decoding, err := DecodeRerank(strings.NewReader(openRouterRerankBody))
	require.NoError(t, err)

	outOfRange := openRouterRerankAnswer()
	outOfRange.Results = []inference.RerankResult{{Index: 9, RelevanceScore: 0.5}}
	_, err = EncodeRerank(outOfRange, decoding.Request, "cohere", nil)
	require.ErrorIs(t, err, inference.ErrRerankResultOutOfRange)

	overScored := openRouterRerankAnswer()
	overScored.Results = []inference.RerankResult{{Index: 0, RelevanceScore: 1.2}}
	_, err = EncodeRerank(overScored, decoding.Request, "cohere", nil)
	require.ErrorIs(t, err, inference.ErrRerankScoreOutOfRange)
}

// TestTheOpenRouterRerankCodecReportsAMisspelledField holds the strict decode.
// A caller that misspells top_n pays for every document rather than the count
// it asked to be charged for.
func TestTheOpenRouterRerankCodecReportsAMisspelledField(t *testing.T) {
	_, err := DecodeRerank(strings.NewReader(
		`{"model":"m","query":"q","documents":["d"],"top_k":2}`,
	))
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown field")
}
