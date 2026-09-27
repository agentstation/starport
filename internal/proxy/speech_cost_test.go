package proxy

import (
	"context"
	"encoding/json/v2"
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/usage"
	"github.com/stretchr/testify/require"
)

func TestSpeechUsageCapture(t *testing.T) {
	builder, err := starmap.EmbeddedBuilder()
	require.NoError(t, err)
	catalog, err := builder.Build()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(syntheticPricingSource{state: starmap.CatalogState{Catalog: catalog, GenerationID: "speech-usage", Sequence: 1}})
	require.NoError(t, err)
	require.NoError(t, plane.SetAdapter(runtimecatalog.AdapterAvailability{ProviderID: catalogs.ProviderIDOpenAI, Registered: true, Operations: []catalogs.ProviderOperation{catalogs.ProviderOperationAudioSpeech}, EndpointTypes: []catalogs.EndpointType{catalogs.EndpointTypeOpenAI}}))
	for _, tc := range []struct {
		name, reason string
		characters   int64
		known        bool
		cost         int64
	}{
		{name: "measured input", characters: 5, known: true, cost: 75000},
		{name: "measured zero", known: true},
		{name: "unknown input", reason: usage.CostReasonNoUsage},
		{name: "invalid input", characters: -1, known: true, reason: usage.CostReasonInvalidUsage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repository := &recordingUsageRepository{}
			capture := NewUsageCapture(repository)
			service := &usageCaptureService{capture: capture}
			request := &SpeechRequest{RequestID: "speech-report", AccountID: "account", Request: inference.SpeechRequest{Model: "openai/tts-1", Input: "hello", Voice: "alloy"}}
			response, err := captureOperation(t.Context(), service, usage.OperationSpeech, request, request.Request.Model,
				func(context.Context, *SpeechRequest) (*SpeechResponse, error) {
					return &SpeechResponse{ModelUsed: "openai/tts-1", ProviderUsed: "openai", CatalogSnapshot: plane.Current(), Response: inference.SpeechResponse{Audio: []byte("audio"), Usage: inference.Usage{TokensUnknown: true, InputCharacters: tc.characters, InputCharactersKnown: tc.known}}}, nil
				})
			require.NoError(t, err)
			capture.Flush()
			records := repository.all()
			require.Len(t, records, 1)
			record := records[0]
			require.Equal(t, tc.characters, record.InputCharacters)
			require.Equal(t, tc.known, record.InputCharactersKnown)
			require.True(t, record.TokensUnknown)
			require.Equal(t, tc.reason, record.CostUnavailableReason)
			if tc.reason == "" {
				require.NotNil(t, record.Cost)
				require.Equal(t, tc.cost, record.Cost.NanoUSD)
				require.Equal(t, record.Cost, response.Cost)
			} else {
				require.Nil(t, record.Cost)
			}
			data, err := json.Marshal(record)
			require.NoError(t, err)
			var restored usage.Record
			require.NoError(t, json.Unmarshal(data, &restored))
			require.Equal(t, record.InputCharacters, restored.InputCharacters)
			require.Equal(t, record.InputCharactersKnown, restored.InputCharactersKnown)
		})
	}
}
