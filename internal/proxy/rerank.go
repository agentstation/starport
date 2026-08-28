package proxy

import (
	"context"
)

// ProcessRerank scores one document list against a query. It is not cached and
// not streamed, so it is the shortest of the proxy's operations: validate the
// request the codec decoded, then route it.
func (p *proxy) ProcessRerank(ctx context.Context, req *RerankRequest) (*RerankResponse, error) {
	if err := ValidateRerankRequest(req); err != nil {
		return nil, err
	}
	return processOperation(ctx, req, req.Request.Model, p.router.RouteRerank)
}
