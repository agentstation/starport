package providerauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/credentials"
)

type materialSourceFunc func(context.Context) (credentials.Material, error)

func (f materialSourceFunc) ResolveMaterial(ctx context.Context) (credentials.Material, error) {
	return f(ctx)
}

func TestBearerSourceProjectsNamedStaticMaterialWithoutExpiry(t *testing.T) {
	profile := bearerTestProfile()
	source, err := NewBearerSource(materialSourceFunc(
		func(context.Context) (credentials.Material, error) {
			return credentials.NewMaterial(
				profile,
				map[catalogs.ProviderCredentialFieldID]string{"api-key": "static-secret"},
				credentials.MaterialMetadata{Version: "opaque"},
			), nil
		},
	), "api-key")
	if err != nil {
		t.Fatalf("new bearer source: %v", err)
	}

	token, err := source.Token(context.Background())
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if token.Value != "static-secret" || !token.ExpiresAt.IsZero() {
		t.Fatalf("token = %#v", token)
	}
}

func TestBearerSourcePreservesOptionalExpiry(t *testing.T) {
	expiresAt := time.Now().Add(time.Hour).Round(0)
	profile := bearerTestProfile()
	source, err := NewBearerSource(materialSourceFunc(
		func(context.Context) (credentials.Material, error) {
			return credentials.NewMaterial(
				profile,
				map[catalogs.ProviderCredentialFieldID]string{"api-key": "renewable"},
				credentials.MaterialMetadata{Version: "opaque", ExpiresAt: expiresAt},
			), nil
		},
	), "api-key")
	if err != nil {
		t.Fatalf("new bearer source: %v", err)
	}

	token, err := source.Token(context.Background())
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if token.Value != "renewable" || !token.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("token = %#v", token)
	}
}

func TestBearerSourceDelegatesLifecycleWithoutASecondCache(t *testing.T) {
	profile := bearerTestProfile()
	calls := 0
	source, err := NewBearerSource(materialSourceFunc(
		func(context.Context) (credentials.Material, error) {
			calls++
			return credentials.NewMaterial(
				profile,
				map[catalogs.ProviderCredentialFieldID]string{"api-key": "secret"},
				credentials.MaterialMetadata{Version: "opaque"},
			), nil
		},
	), "api-key")
	if err != nil {
		t.Fatalf("new bearer source: %v", err)
	}
	for range 2 {
		if _, err := source.Token(context.Background()); err != nil {
			t.Fatalf("token: %v", err)
		}
	}
	if calls != 2 {
		t.Fatalf("material source calls = %d, want 2", calls)
	}
}

func TestBearerSourceRejectsMissingInputsAndValues(t *testing.T) {
	if _, err := NewBearerSource(nil, "api-key"); !errors.Is(err, ErrSourceRequired) {
		t.Fatalf("nil source error = %v", err)
	}
	if _, err := NewBearerSource(materialSourceFunc(
		func(context.Context) (credentials.Material, error) { return credentials.Material{}, nil },
	), ""); err == nil {
		t.Fatal("empty bearer field was accepted")
	}
	source, err := NewBearerSource(materialSourceFunc(
		func(context.Context) (credentials.Material, error) {
			return credentials.NewMaterial(
				bearerTestProfile(),
				map[catalogs.ProviderCredentialFieldID]string{"other": "secret"},
				credentials.MaterialMetadata{},
			), nil
		},
	), "api-key")
	if err != nil {
		t.Fatalf("new bearer source: %v", err)
	}
	if _, err := source.Token(context.Background()); !errors.Is(err, ErrTokenEmpty) {
		t.Fatalf("missing value error = %v", err)
	}
}

func bearerTestProfile() catalogs.ProviderCredentialProfile {
	return catalogs.ProviderCredentialProfile{
		ID:        "api-key",
		Primitive: catalogs.ProviderAuthenticationAPIKey,
		Fields:    []catalogs.ProviderCredentialFieldID{"api-key"},
	}
}
