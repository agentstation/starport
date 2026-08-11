package byok

import "context"

// ValidateKey validates one Starport inference credential against the active
// catalog contract. It never reads Starmap catalog-acquisition credentials.
func (m *keyManager) ValidateKey(
	ctx context.Context,
	provider string,
	key map[string]string,
	config map[string]any,
) error {
	if m.validator == nil {
		return ErrCredentialValidatorRequired
	}
	return m.validator.ValidateCredential(ctx, provider, key, config)
}
