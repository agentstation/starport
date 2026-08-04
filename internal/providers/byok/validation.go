package byok

import (
	"context"

	"github.com/agentstation/starmap/pkg/catalogs"
)

// ValidateKey validates one Starport inference credential through the compiled
// adapter descriptor. It never reads Starmap catalog-acquisition credentials.
func (m *keyManager) ValidateKey(
	ctx context.Context,
	provider string,
	key map[string]string,
	config map[string]any,
) error {
	if m.adapters == nil {
		return ErrAdapterRegistryRequired
	}
	return m.adapters.ValidateCredential(ctx, catalogs.ProviderID(provider), key, config)
}
