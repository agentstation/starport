package cloudchain

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"cloud.google.com/go/auth"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/agentstation/starmap/pkg/catalogs"
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

func TestGoogleDefaultChainUsesCatalogScopeAndBearerField(t *testing.T) {
	expiresAt := time.Now().Add(time.Hour)
	var requested bool
	chain := googleDefaultChain{provider: googleTokenProviderFunc(
		func(ctx context.Context) (*auth.Token, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			requested = true
			return &auth.Token{Value: "google-token", Expiry: expiresAt}, nil
		},
	)}
	material, err := chain.Resolve(context.Background(), cloudTestProfile(
		catalogs.ProviderAuthenticationGoogleDefault,
		[]string{"https://example.test/scope"},
	), cloudTestFields())
	if err != nil {
		t.Fatalf("resolve Google default chain: %v", err)
	}
	if value, exists := material.Value("access-token"); !exists || value != "google-token" {
		t.Fatalf("Google bearer value = %q, %t", value, exists)
	}
	if !requested {
		t.Fatal("Google token provider was not called")
	}
}

func TestGoogleDefaultProjectsCatalogProjectField(t *testing.T) {
	profile := cloudTestProfile(
		catalogs.ProviderAuthenticationGoogleDefault,
		nil,
	)
	profile.Fields = append(profile.Fields, "project")
	profile.ProtocolOptions.GoogleDefault = &catalogs.ProviderGoogleDefaultProtocolOptions{
		ProjectField: "project",
	}
	fields := cloudTestFields()
	fields["project"] = catalogs.ProviderCredentialField{
		ID: "project", Kind: catalogs.ProviderCredentialFieldParameter, Required: true,
	}
	chain := googleDefaultChain{
		provider: googleTokenProviderFunc(func(context.Context) (*auth.Token, error) {
			return &auth.Token{Value: "token", Expiry: time.Now().Add(time.Hour)}, nil
		}),
		projectID: func(context.Context) (string, error) {
			return "catalog-project", nil
		},
	}
	material, err := chain.Resolve(t.Context(), profile, fields)
	if err != nil {
		t.Fatal(err)
	}
	if project, exists := material.Value("project"); !exists || project != "catalog-project" {
		t.Fatalf("project = %q, %t", project, exists)
	}
	supplied, err := chain.SuppliedFields(profile, fields)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(supplied, catalogs.ProviderCredentialFieldID("project")) {
		t.Fatalf("supplied fields = %v", supplied)
	}
}

func TestGoogleDefaultChainForwardsCancellation(t *testing.T) {
	chain := googleDefaultChain{provider: googleTokenProviderFunc(
		func(ctx context.Context) (*auth.Token, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := chain.Resolve(ctx, cloudTestProfile(
		catalogs.ProviderAuthenticationGoogleDefault,
		nil,
	), cloudTestFields())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Google chain error = %v", err)
	}
}

func TestAzureDefaultChainUsesCatalogScopesAndBearerField(t *testing.T) {
	expiresAt := time.Now().Add(time.Hour)
	wantScopes := []string{"https://example.test/.default"}
	var gotScopes []string
	chain := azureDefaultChain{credential: azureTokenCredentialFunc(
		func(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
			if err := ctx.Err(); err != nil {
				return azcore.AccessToken{}, err
			}
			gotScopes = append([]string(nil), options.Scopes...)
			return azcore.AccessToken{Token: "azure-token", ExpiresOn: expiresAt}, nil
		},
	)}
	material, err := chain.Resolve(context.Background(), cloudTestProfile(
		catalogs.ProviderAuthenticationAzureDefault,
		wantScopes,
	), cloudTestFields())
	if err != nil {
		t.Fatalf("resolve Azure default chain: %v", err)
	}
	if value, exists := material.Value("access-token"); !exists || value != "azure-token" {
		t.Fatalf("Azure bearer value = %q, %t", value, exists)
	}
	if !slices.Equal(gotScopes, wantScopes) {
		t.Fatalf("Azure scopes = %v, want %v", gotScopes, wantScopes)
	}
}

func TestCloudChainsAreKeyedOnlyByAuthenticationPrimitive(t *testing.T) {
	chains := DefaultCloudChains()
	if len(chains) != 2 ||
		chains[catalogs.ProviderAuthenticationGoogleDefault] == nil ||
		chains[catalogs.ProviderAuthenticationAzureDefault] == nil {
		t.Fatalf("default cloud chains = %#v", chains)
	}
}

func TestCloudChainRejectsAmbiguousBearerFields(t *testing.T) {
	profile := cloudTestProfile(catalogs.ProviderAuthenticationGoogleDefault, nil)
	profile.Fields = append(profile.Fields, "second-token")
	profile.Placements = append(profile.Placements, catalogs.ProviderCredentialPlacement{
		Field: "second-token", Kind: catalogs.ProviderCredentialPlacementHeader,
		Name: "X-Authorization", Scheme: catalogs.ProviderCredentialSchemeBearer,
	})
	fields := cloudTestFields()
	fields["second-token"] = catalogs.ProviderCredentialField{
		ID: "second-token", Kind: catalogs.ProviderCredentialFieldSecret, Required: true,
	}
	chain := googleDefaultChain{provider: googleTokenProviderFunc(
		func(context.Context) (*auth.Token, error) {
			return &auth.Token{Value: "token", Expiry: time.Now().Add(time.Hour)}, nil
		},
	)}
	if _, err := chain.Resolve(context.Background(), profile, fields); err == nil {
		t.Fatal("ambiguous bearer profile was accepted")
	}
}

func cloudTestProfile(
	primitive catalogs.ProviderAuthenticationPrimitive,
	scopes []string,
) catalogs.ProviderCredentialProfile {
	return catalogs.ProviderCredentialProfile{
		ID: "default", Primitive: primitive,
		Fields: []catalogs.ProviderCredentialFieldID{"access-token"},
		Placements: []catalogs.ProviderCredentialPlacement{{
			Field: "access-token", Kind: catalogs.ProviderCredentialPlacementHeader,
			Name: "Authorization", Scheme: catalogs.ProviderCredentialSchemeBearer,
		}},
		Scopes: append([]string(nil), scopes...),
	}
}

func cloudTestFields() map[catalogs.ProviderCredentialFieldID]catalogs.ProviderCredentialField {
	return map[catalogs.ProviderCredentialFieldID]catalogs.ProviderCredentialField{
		"access-token": {
			ID: "access-token", Kind: catalogs.ProviderCredentialFieldSecret, Required: true,
		},
	}
}
