package connectors

import (
	"context"

	"github.com/agentstation/starmap/pkg/catalogs"
)

// ModerationRequest is one moderation call at the connector seam. The wire
// shape belongs to each transport, so the request carries the inputs and no
// wire words.
type ModerationRequest struct {
	MediaTarget
	// Inputs is the list of texts to classify, in the order the caller
	// supplied. A result answers the input at the same position.
	Inputs []string
}

// ModerationCategory is one harm category's verdict on one input, named
// exactly as the provider states it.
type ModerationCategory struct {
	Name    string  `json:"name"`
	Flagged bool    `json:"flagged"`
	Score   float64 `json:"score"`
}

// ModerationResult is every category's verdict on one input.
type ModerationResult struct {
	Flagged    bool                 `json:"flagged"`
	Categories []ModerationCategory `json:"categories"`
}

// ModerationResponse is the provider answer to a moderation call.
type ModerationResponse struct {
	// ID is the provider's identifier for this classification.
	ID string `json:"id"`
	// Model is the exact model the provider answered with, which can name a
	// dated snapshot of the alias the caller asked for.
	Model string `json:"model"`
	// Results holds one result per request input, at the same position.
	Results []ModerationResult `json:"results"`
	Usage   *MediaUsage        `json:"usage,omitempty"`
}

// Moderator is the narrow optional interface a transport implements to serve
// the moderations operation. Connector does not carry it, for the same
// reason Reranker stands apart: one compiled transport serves moderation and
// the rest serve none of it, so a method on Connector would make every other
// transport answer a call it cannot perform and would stop the compiler
// reporting the difference.
type Moderator interface {
	Moderate(ctx context.Context, request *ModerationRequest) (*ModerationResponse, error)
}

// ModeratorFor returns the moderation transport a route selected. It reports
// false for a connector whose compiled transport does not serve the
// operation, so the caller refuses before it spends a credential.
func ModeratorFor(
	connector Connector,
	endpointType catalogs.EndpointType,
) (Moderator, bool) {
	transport, found := selectTransport(connector, endpointType)
	if !found {
		return nil, false
	}
	moderator, implemented := transport.(Moderator)
	return moderator, implemented
}
