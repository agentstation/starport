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
	return newGoogleCredentialSource(credential)
}

func newGoogleCredentialSource(credential *auth.Credentials) (Source, error) {
	if credential == nil {
		return nil, errors.New("google credentials are required")
	}
	quotaProjectID, err := credential.QuotaProjectID(context.Background())
	if err != nil {
		return nil, fmt.Errorf("resolve Google quota project: %w", err)
	}
	return newGoogleSource(credential.TokenProvider, quotaProjectID)
}

func newGoogleSource(provider auth.TokenProvider, quotaProjectID string) (Source, error) {
	if provider == nil {
		return nil, errors.New("google token provider is required")
	}
	return NewRefreshingSource(SourceFunc(func(ctx context.Context) (Token, error) {
		token, err := provider.Token(ctx)
		if err != nil {
			return Token{}, fmt.Errorf("get Google access token: %w", err)
		}
		return Token{
			Value:          token.Value,
			ExpiresAt:      token.Expiry,
			QuotaProjectID: quotaProjectID,
		}, nil
	}), RefreshOptions{})
}
