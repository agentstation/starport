package requestctx

import "context"

// BatchAuthorization captures private evidence for one account-owned batch.
type BatchAuthorization func(account, id string) ([]byte, error)

type batchAuthorizationKey struct{}

// WithBatchAuthorization installs a capture function after caller authentication.
func WithBatchAuthorization(ctx context.Context, capture BatchAuthorization) context.Context {
	return context.WithValue(ctx, batchAuthorizationKey{}, capture)
}

// RetainBatchAuthorization captures restart evidence without extending permission.
// An absent capture function leaves recovery unavailable to library-only callers.
func RetainBatchAuthorization(ctx context.Context, account, id string) ([]byte, error) {
	capture, _ := ctx.Value(batchAuthorizationKey{}).(BatchAuthorization)
	if capture == nil {
		return nil, nil
	}
	return capture(account, id)
}
