package proxy

import (
	"context"
	"fmt"

	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/limits"
)

// ProcessRerank scores one document list against a query. It is not cached and
// not streamed, so it is the shortest of the proxy's operations: validate the
// request the codec decoded, refuse what the account cannot pay for, then route
// it.
func (p *proxy) ProcessRerank(ctx context.Context, req *RerankRequest) (*RerankResponse, error) {
	if err := ValidateRerankRequest(req); err != nil {
		return nil, err
	}
	if err := p.affordableRerank(ctx, req.Request.Model); err != nil {
		return nil, err
	}
	return processOperation(ctx, req, req.Request.Model, p.router.RouteRerank)
}

// affordableRerank refuses a rerank call the holder cannot pay for, before the
// call happens.
//
// One search unit is the floor: every rerank call on a search-unit offering
// bills at least one, and a call over the provider's per-unit document count
// bills several. The exact number arrives with the answer, so the estimate
// takes the floor and the cheapest offering of this model, which is the same
// direction the document reader errs in. A bound that refused work the account
// could have paid for would cost a caller a request over a price that was never
// charged; erring low overshoots the bound by one call, and the account's own
// cap refuses the next one.
//
// An offering that bills the tokens it reads states no floor at all before the
// provider has read the documents, so it estimates nothing and refuses nothing.
func (p *proxy) affordableRerank(ctx context.Context, model string) error {
	allowance := limits.AllowanceFromContext(ctx)
	if !allowance.Bounded {
		return nil
	}
	prices := p.catalogPrices(ctx)
	if prices == nil {
		return nil
	}
	price, priced := prices.LowestSearchUnitPrice(model)
	if !priced {
		return nil
	}
	if err := allowance.Covers(nanoUSD(price)); err != nil {
		return failure.New(
			failure.Billing,
			fmt.Sprintf("Reranking with %s would pass this account's spend budget.", model),
			false,
			failure.ProviderDetails{},
			err,
		)
	}
	return nil
}
