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

func TestImageUsageCapture(t *testing.T) {
	builder, err := starmap.EmbeddedBuilder()
	require.NoError(t, err)
	catalog, err := builder.Build()
	require.NoError(t, err)
	plane, err := runtimecatalog.Open(syntheticPricingSource{state: starmap.CatalogState{Catalog: catalog, GenerationID: "image-usage", Sequence: 1}})
	require.NoError(t, err)
	require.NoError(t, plane.SetAdapter(runtimecatalog.AdapterAvailability{ProviderID: "deepinfra", Registered: true, Operations: []catalogs.ProviderOperation{catalogs.ProviderOperationImagesGenerations}, EndpointTypes: []catalogs.EndpointType{catalogs.EndpointTypeOpenAI}}))
	for _, tc := range []struct {
		name, size, reason string
		count              int
		cost               int64
	}{
		{name: "scaled output", size: "1024x1536", count: 2, cost: 1500000},
		{name: "default dimensions", count: 1, cost: 500000},
		{name: "missing output", reason: usage.CostReasonNoUsage},
		{name: "invalid output", count: -1, reason: usage.CostReasonInvalidUsage},
		{name: "unknown dimensions", size: "auto", count: 1, reason: usage.CostReasonInvalidUsage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repository := &recordingUsageRepository{}
			capture := NewUsageCapture(repository)
			service := &usageCaptureService{capture: capture}
			model := "deepinfra/black-forest-labs/FLUX-1-schnell"
			request := &ImagesRequest{RequestID: "image-report", AccountID: "account", Request: inference.ImagesRequest{Model: model, Prompt: "landscape", N: 2, Size: tc.size}}
			response, err := captureOperation(t.Context(), service, usage.OperationImages, request, model,
				func(context.Context, *ImagesRequest) (*ImagesResponse, error) {
					return &ImagesResponse{ModelUsed: model, ProviderUsed: "deepinfra", CatalogSnapshot: plane.Current(), Response: inference.ImagesResponse{Usage: inference.Usage{TokensUnknown: true, GeneratedImages: tc.count}}}, nil
				})
			require.NoError(t, err)
			capture.Flush()
			records := repository.all()
			require.Len(t, records, 1)
			record := records[0]
			require.NotNil(t, record.Media)
			require.Equal(t, tc.size, record.Media.ImageSize)
			require.EqualValues(t, tc.count, record.Media.GeneratedImages)
			require.True(t, record.TokensUnknown)
			require.Equal(t, tc.reason, record.CostUnavailableReason)
			if tc.reason == "" {
				require.NotNil(t, record.Cost)
				require.Equal(t, tc.cost, record.Cost.NanoUSD)
				require.Equal(t, record.Cost, response.Cost)
			} else {
				require.Nil(t, record.Cost)
			}
			raw, err := json.Marshal(record)
			require.NoError(t, err)
			var restored usage.Record
			require.NoError(t, json.Unmarshal(raw, &restored))
			require.Equal(t, record.Media, restored.Media)
		})
	}
}
