package connectors

import (
	"context"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/jobs"
)

// NativeVideoRequest runs video inference to completion in one provider request.
// The caller resolves Seconds and Size from the selected offering before dispatch.
type NativeVideoRequest struct {
	MediaTarget
	Prompt         string
	NegativePrompt string
	Seconds        int64
	Size           string
	Seed           *int64
	MaxBytes       int64
}

// NativeVideoResponse separates measured usage from the generated asset.
// Asset errors must not discard valid usage from the same provider response.
type NativeVideoResponse struct {
	State     jobs.JobState
	Reason    string
	RequestID string
	// OutputSeconds is nil when provider duration is absent or invalid.
	OutputSeconds *int64
	// EstimatedUSD is provider telemetry and cannot settle a reservation.
	EstimatedUSD *float64
	Asset        JobAsset
	// AssetURL requires a separately authorized download without inference credentials.
	AssetURL string
}

// Clone returns independent asset bytes and usage fields.
func (r NativeVideoResponse) Clone() NativeVideoResponse {
	r.Asset = r.Asset.Clone()
	if r.OutputSeconds != nil {
		seconds := *r.OutputSeconds
		r.OutputSeconds = &seconds
	}
	if r.EstimatedUSD != nil {
		cost := *r.EstimatedUSD
		r.EstimatedUSD = &cost
	}
	return r
}

// NativeVideoGenerator sends one synchronous provider inference request.
// The jobs service owns gateway jobs and runs background work.
type NativeVideoGenerator interface {
	GenerateVideo(context.Context, *NativeVideoRequest) (*NativeVideoResponse, error)
}

// NativeVideoGeneratorFor returns the synchronous video transport a route selected.
func NativeVideoGeneratorFor(connector Connector, endpointType catalogs.EndpointType) (NativeVideoGenerator, bool) {
	transport, found := selectTransport(connector, endpointType)
	if !found {
		return nil, false
	}
	generator, implemented := transport.(NativeVideoGenerator)
	return generator, implemented
}
