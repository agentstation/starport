package providerauth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"cloud.google.com/go/auth"
	googlecredentials "cloud.google.com/go/auth/credentials"
	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/credentials"
)

const defaultRefreshBefore = 2 * time.Minute

type googleDefaultChain struct {
	provider auth.TokenProvider
}

func (chain googleDefaultChain) Resolve(
	ctx context.Context,
	profile catalogs.ProviderCredentialProfile,
	fields map[catalogs.ProviderCredentialFieldID]catalogs.ProviderCredentialField,
) (credentials.SourceMaterial, error) {
	provider := chain.provider
	if provider == nil {
		credential, err := googlecredentials.DetectDefault(&googlecredentials.DetectOptions{
			Scopes:              append([]string(nil), profile.Scopes...),
			EarlyTokenRefresh:   0,
			DisableAsyncRefresh: true,
		})
		if err != nil {
			return credentials.SourceMaterial{}, credentials.NewSourceError(
				credentials.SourceErrorNotConfigured,
				"google-default",
			)
		}
		provider = credential.TokenProvider
	}
	if provider == nil {
		return credentials.SourceMaterial{}, errors.New("google token provider is required")
	}
	token, err := provider.Token(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return credentials.SourceMaterial{}, ctx.Err()
		}
		return credentials.SourceMaterial{}, fmt.Errorf("get Google access token: %w", err)
	}
	fieldID, err := bearerField(profile, fields)
	if err != nil {
		return credentials.SourceMaterial{}, err
	}
	if token == nil || token.Value == "" {
		return credentials.SourceMaterial{}, credentials.NewSourceError(
			credentials.SourceErrorInvalid,
			"google-default",
		)
	}
	return renewableBearerMaterial(fieldID, token.Value, token.Expiry), nil
}
