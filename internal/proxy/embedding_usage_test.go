package proxy

import (
	"encoding/json/v2"
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/usage"
	"github.com/stretchr/testify/require"
)

func TestEmbeddingCapturePreservesUnknownUsage(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(client)
	require.NoError(t, err)
	require.NoError(t, plane.SetAdapter(runtimecatalog.AdapterAvailability{ProviderID: "openai", Registered: true, Operations: []catalogs.ProviderOperation{catalogs.ProviderOperationEmbeddings}, EndpointTypes: []catalogs.EndpointType{catalogs.EndpointTypeOpenAI}}))
	snapshot, route := plane.Current(), "openai/text-embedding-3-small"
	for _, test := range []struct {
		wire  string
		known bool
		cost  int64
	}{
		{`{}`, false, 0},
		{`{"usage":{"total_tokens":8}}`, false, 0},
		{`{"usage":{"prompt_tokens":8,"total_tokens":9}}`, false, 0},
		{`{"usage":{"prompt_tokens":0,"total_tokens":0}}`, true, 0},
		{`{"usage":{"prompt_tokens":8,"total_tokens":8}}`, true, 160},
	} {
		t.Run(test.wire, func(t *testing.T) {
			var provider connectors.EmbeddingsResponse
			require.NoError(t, json.Unmarshal([]byte(test.wire), &provider))
			canonical, err := connectors.EmbeddingResponseToInference(&provider)
			require.NoError(t, err)
			// Cache serialization must preserve whether the source measured the counts.
			data, err := json.Marshal(canonical)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(data, &canonical))
			upstream := &embeddingCaptureProxy{response: &EmbeddingsResponse{Response: canonical, ModelUsed: route, CatalogSnapshot: snapshot}}
			records := &recordingUsageRepository{}
			capture := NewUsageCapture(records)
			_, err = capture.Wrap(upstream).ProcessEmbeddings(t.Context(), &EmbeddingsRequest{Request: inference.EmbeddingRequest{Model: route}, AccountID: "account", KeyID: "key", RequestID: "embedding"})
			require.NoError(t, err)
			capture.Flush()
			all := records.all()
			require.Len(t, all, 1)
			record := all[0]
			if test.known {
				require.NotNil(t, record.Cost)
				require.Equal(t, test.cost, record.Cost.NanoUSD)
				require.Empty(t, record.CostUnavailableReason)
			} else {
				require.Nil(t, record.Cost)
				require.Equal(t, usage.CostReasonNoUsage, record.CostUnavailableReason)
				encoded, err := json.Marshal(record)
				require.NoError(t, err)
				require.Contains(t, string(encoded), `"tokens_unknown":true`)
			}
		})
	}
}
