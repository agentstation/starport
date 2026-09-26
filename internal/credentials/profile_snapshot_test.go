package credentials

import (
	"context"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
)

func profileSnapshotProvider() catalogs.Provider {
	provider := staticCredentialProvider()
	provider.Credentials.Fields = append(provider.Credentials.Fields, catalogs.ProviderCredentialField{ID: "project", Kind: catalogs.ProviderCredentialFieldParameter, Required: true})
	provider.Credentials.Profiles[0].Fields = append(provider.Credentials.Profiles[0].Fields, "project")
	return provider
}

func TestInferenceRejectsConflictingSecretVersionsBeforeReads(t *testing.T) {
	calls := 0
	resolver := NewResolver(WithReferenceSource(&testReferenceSource{backend: ReferenceBackendVault, resolve: func(context.Context, Reference) (SourceMaterial, error) { calls++; return SourceMaterial{}, nil }}))
	_, err := resolver.Provider(profileSnapshotProvider(), map[catalogs.ProviderCredentialFieldID]ReferencePolicy{
		"api-key": {Reference: Reference{backend: ReferenceBackendVault, resource: "team/secret", field: "key", version: "1"}},
		"project": {Reference: Reference{backend: ReferenceBackendVault, resource: "team/secret", field: "project", version: "2"}},
	}, false)
	if err == nil {
		t.Fatal("conflicting secret versions were accepted")
	}
	if calls != 0 {
		t.Fatal("invalid version scope read secret material")
	}
}

func TestInferenceSecretRotationPublishesCompleteProfiles(t *testing.T) {
	for _, backend := range []ReferenceBackend{ReferenceBackendGCPStore, ReferenceBackendAzureVault, ReferenceBackendAWSStore, ReferenceBackendVault, ReferenceBackendOpenBao} {
		t.Run(string(backend), func(t *testing.T) {
			mode := "first"
			source := &testReferenceSource{backend: backend, resolve: func(_ context.Context, ref Reference) (SourceMaterial, error) {
				version := mode
				if mode == "mixed" && ref.field == "project" {
					version = "other"
				}
				return NewSourceMaterial(map[string]string{ref.field: "valid-" + version}, version, time.Time{}, nil), nil
			}}
			resolver := NewResolver(WithReferenceSource(source))
			handle, err := resolver.Provider(profileSnapshotProvider(), map[catalogs.ProviderCredentialFieldID]ReferencePolicy{
				"api-key": {Reference: Reference{backend: backend, resource: "team/secret", field: "key"}},
				"project": {Reference: Reference{backend: backend, resource: "team/secret", field: "project"}},
			}, false)
			if err != nil {
				t.Fatal(err)
			}
			check := func(want string) {
				t.Helper()
				material, err := handle.CachedMaterial(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				for _, field := range []catalogs.ProviderCredentialFieldID{"api-key", "project"} {
					if value, _ := material.Value(field); value != "valid-"+want {
						t.Fatalf("field %s did not retain complete %s profile", field, want)
					}
				}
			}
			if _, _, err := handle.Resolve(t.Context()); err != nil {
				t.Fatal(err)
			}
			check("first")
			mode = "mixed"
			if _, _, err := handle.Refresh(t.Context()); !IsSourceError(err, SourceErrorUnavailable) {
				t.Fatalf("mixed rotation accepted: %v", err)
			}
			check("first")
			mode = "second"
			if _, _, err := handle.Refresh(t.Context()); err != nil {
				t.Fatal(err)
			}
			check("second")
		})
	}
}
