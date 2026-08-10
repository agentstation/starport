package providerauth

import (
	"context"
	"errors"
	"fmt"

	"cloud.google.com/go/auth"
	"cloud.google.com/go/auth/credentials"
)

const googleCloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"

// NewGoogleDefaultSource uses Google Application Default Credentials for
// Vertex AI inference.
func NewGoogleDefaultSource() (Source, error) {
	credential, err := credentials.DetectDefault(&credentials.DetectOptions{
		Scopes:              []string{googleCloudPlatformScope},
		EarlyTokenRefresh:   defaultRefreshBefore,
		DisableAsyncRefresh: true,
	})
	if err != nil {
		return nil, fmt.Errorf("detect Google default credentials: %w", err)
	}
	return newGoogleSource(credential.TokenProvider)
}

func newGoogleSource(provider auth.TokenProvider) (Source, error) {
	if provider == nil {
		return nil, errors.New("google token provider is required")
	}
	return NewRefreshingSource(SourceFunc(func(ctx context.Context) (Token, error) {
		token, err := provider.Token(ctx)
		if err != nil {
			return Token{}, fmt.Errorf("get Google access token: %w", err)
		}
		return Token{Value: token.Value, ExpiresAt: token.Expiry}, nil
	}), RefreshOptions{})
}
