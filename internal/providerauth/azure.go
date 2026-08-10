package providerauth

import (
	"context"
	"errors"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

const azureCognitiveServicesScope = "https://cognitiveservices.azure.com/.default"

// NewAzureDefaultSource uses Azure DefaultAzureCredential for Azure OpenAI
// inference.
func NewAzureDefaultSource() (Source, error) {
	credential, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("create Azure default credential: %w", err)
	}
	return newAzureSource(credential)
}

func newAzureSource(credential azcore.TokenCredential) (Source, error) {
	if credential == nil {
		return nil, errors.New("azure token credential is required")
	}
	return NewRefreshingSource(SourceFunc(func(ctx context.Context) (Token, error) {
		token, err := credential.GetToken(ctx, policy.TokenRequestOptions{
			Scopes: []string{azureCognitiveServicesScope},
		})
		if err != nil {
			return Token{}, fmt.Errorf("get Azure access token: %w", err)
		}
		return Token{Value: token.Token, ExpiresAt: token.ExpiresOn}, nil
	}), RefreshOptions{})
}
