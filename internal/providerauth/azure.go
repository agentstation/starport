// Package providerauth applies catalog-declared authentication primitives to
// provider inference requests.
package providerauth

import (
	"context"
	"errors"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/credentials"
)

type azureDefaultChain struct {
	credential azcore.TokenCredential
}

func (chain azureDefaultChain) Resolve(
	ctx context.Context,
	profile catalogs.ProviderCredentialProfile,
	fields map[catalogs.ProviderCredentialFieldID]catalogs.ProviderCredentialField,
) (credentials.SourceMaterial, error) {
	credential := chain.credential
	if credential == nil {
		var err error
		credential, err = azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			return credentials.SourceMaterial{}, credentials.NewSourceError(
				credentials.SourceErrorNotConfigured,
				"azure-default",
			)
		}
	}
	if credential == nil {
		return credentials.SourceMaterial{}, errors.New("azure token credential is required")
	}
	token, err := credential.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: append([]string(nil), profile.Scopes...),
	})
	if err != nil {
		if ctx.Err() != nil {
			return credentials.SourceMaterial{}, ctx.Err()
		}
		return credentials.SourceMaterial{}, fmt.Errorf("get Azure access token: %w", err)
	}
	fieldID, err := bearerField(profile, fields)
	if err != nil {
		return credentials.SourceMaterial{}, err
	}
	if token.Token == "" {
		return credentials.SourceMaterial{}, credentials.NewSourceError(
			credentials.SourceErrorInvalid,
			"azure-default",
		)
	}
	return renewableBearerMaterial(fieldID, token.Token, token.ExpiresOn), nil
}
