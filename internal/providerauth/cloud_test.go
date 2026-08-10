package providerauth

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"cloud.google.com/go/auth"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

type googleTokenProviderFunc func(context.Context) (*auth.Token, error)

func (f googleTokenProviderFunc) Token(ctx context.Context) (*auth.Token, error) {
	return f(ctx)
}

type azureTokenCredentialFunc func(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error)

func (f azureTokenCredentialFunc) GetToken(
	ctx context.Context,
	options policy.TokenRequestOptions,
) (azcore.AccessToken, error) {
	return f(ctx, options)
}

func TestGoogleSourceAdaptsTokenAndCancellation(t *testing.T) {
	expiresAt := time.Now().Add(time.Hour)
	provider := googleTokenProviderFunc(func(ctx context.Context) (*auth.Token, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return &auth.Token{Value: "google-token", Expiry: expiresAt}, nil
	})
	source, err := newGoogleSource(provider, "quota-project")
	if err != nil {
		t.Fatalf("new Google source: %v", err)
	}
	token, err := source.Token(context.Background())
	if err != nil {
		t.Fatalf("Google token: %v", err)
	}
	if token.Value != "google-token" || !token.ExpiresAt.Equal(expiresAt) ||
		token.QuotaProjectID != "quota-project" {
		t.Fatalf("Google token = %#v", token)
	}

	canceledSource, err := newGoogleSource(googleTokenProviderFunc(
		func(ctx context.Context) (*auth.Token, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	), "")
	if err != nil {
		t.Fatalf("new canceled Google source: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = canceledSource.Token(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Google token error = %v, want context cancellation", err)
	}
}

func TestGoogleCredentialSourcePreservesQuotaProject(t *testing.T) {
	expiresAt := time.Now().Add(time.Hour)
	credential := auth.NewCredentials(&auth.CredentialsOptions{
		TokenProvider: googleTokenProviderFunc(func(context.Context) (*auth.Token, error) {
			return &auth.Token{Value: "google-token", Expiry: expiresAt}, nil
		}),
		QuotaProjectIDProvider: auth.CredentialsPropertyFunc(
			func(context.Context) (string, error) { return "quota-project", nil },
		),
	})
	source, err := newGoogleCredentialSource(credential)
	if err != nil {
		t.Fatalf("new Google credential source: %v", err)
	}
	token, err := source.Token(context.Background())
	if err != nil {
		t.Fatalf("Google token: %v", err)
	}
	if token.QuotaProjectID != "quota-project" {
		t.Fatalf("Google quota project = %q, want quota-project", token.QuotaProjectID)
	}
}

func TestGoogleCredentialSourceRejectsQuotaProjectFailure(t *testing.T) {
	injected := errors.New("quota project unavailable")
	credential := auth.NewCredentials(&auth.CredentialsOptions{
		TokenProvider: googleTokenProviderFunc(func(context.Context) (*auth.Token, error) {
			return nil, errors.New("unexpected token request")
		}),
		QuotaProjectIDProvider: auth.CredentialsPropertyFunc(
			func(context.Context) (string, error) { return "", injected },
		),
	})
	if _, err := newGoogleCredentialSource(credential); !errors.Is(err, injected) {
		t.Fatalf("new Google credential source error = %v, want quota failure", err)
	}
}

func TestAzureSourceUsesCognitiveServicesScopeAndCancellation(t *testing.T) {
	expiresAt := time.Now().Add(time.Hour)
	var scopes []string
	credential := azureTokenCredentialFunc(func(
		ctx context.Context,
		options policy.TokenRequestOptions,
	) (azcore.AccessToken, error) {
		if err := ctx.Err(); err != nil {
			return azcore.AccessToken{}, err
		}
		scopes = append([]string(nil), options.Scopes...)
		return azcore.AccessToken{Token: "azure-token", ExpiresOn: expiresAt}, nil
	})
	source, err := newAzureSource(credential)
	if err != nil {
		t.Fatalf("new Azure source: %v", err)
	}
	token, err := source.Token(context.Background())
	if err != nil {
		t.Fatalf("Azure token: %v", err)
	}
	if token.Value != "azure-token" || !token.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("Azure token = %#v", token)
	}
	if want := []string{azureCognitiveServicesScope}; !slices.Equal(scopes, want) {
		t.Errorf("Azure scopes = %v, want %v", scopes, want)
	}

	canceledSource, err := newAzureSource(azureTokenCredentialFunc(
		func(ctx context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
			<-ctx.Done()
			return azcore.AccessToken{}, ctx.Err()
		},
	))
	if err != nil {
		t.Fatalf("new canceled Azure source: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = canceledSource.Token(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Azure token error = %v, want context cancellation", err)
	}
}

func TestCloudSourcesRequireSDKCredentials(t *testing.T) {
	if _, err := newGoogleCredentialSource(nil); err == nil {
		t.Error("Google source accepted nil credentials")
	}
	if _, err := newGoogleSource(nil, ""); err == nil {
		t.Error("Google source accepted a nil provider")
	}
	if _, err := newAzureSource(nil); err == nil {
		t.Error("Azure source accepted a nil credential")
	}
}
