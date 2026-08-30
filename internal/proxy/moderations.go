package proxy

import (
	"context"
)

// ProcessModerations classifies one input list. It is not cached and not
// streamed, and it needs no affordability estimate: the one compiled
// moderation provider publishes the operation free, and an offering that
// bills tokens states no floor before the provider has read the inputs, so
// there is nothing to refuse ahead of the account's own cap.
func (p *proxy) ProcessModerations(
	ctx context.Context,
	req *ModerationRequest,
) (*ModerationResponse, error) {
	if err := ValidateModerationRequest(req); err != nil {
		return nil, err
	}
	return processOperation(ctx, req, req.Request.Model, p.router.RouteModerations)
}
