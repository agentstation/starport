package disclosure

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"slices"
)

type contextKey struct{}

// WithPolicy binds immutable disclosure membership to a request.
func WithPolicy(ctx context.Context, policy Policy) context.Context {
	return context.WithValue(ctx, contextKey{}, policy)
}

// FromContext returns the request's disclosure membership, when present.
func FromContext(ctx context.Context) (Policy, bool) {
	policy, ok := ctx.Value(contextKey{}).(Policy)
	return policy, ok
}

// CacheScope separates caller-filtered responses from trusted internal responses.
func CacheScope(ctx context.Context) string {
	policy, ok := FromContext(ctx)
	if !ok {
		return "internal"
	}
	return policy.cacheScope()
}

func (p Policy) cacheScope() string {
	definitions := make([]string, 0, len(p.definitions))
	offerings := make([][2]string, 0, len(p.offerings))
	for id := range p.definitions {
		definitions = append(definitions, string(id))
	}
	for key := range p.offerings {
		offerings = append(offerings, [2]string{string(key.ProviderID), string(key.ProviderModelID)})
	}
	slices.Sort(definitions)
	slices.SortFunc(offerings, func(a, b [2]string) int { return slices.Compare(a[:], b[:]) })
	// These slices contain only strings, which JSON can always encode.
	encoded, _ := json.Marshal(struct {
		Definitions []string
		Offerings   [][2]string
	}{definitions, offerings})
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}
