package credentials

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
)

func TestInferencePolicyUpgradeComparisonAndRestart(t *testing.T) {
	for _, test := range []struct {
		name                           string
		legacy, equal, multi, conflict bool
	}{
		{name: "fresh"}, {name: "different", legacy: true, conflict: true}, {name: "identical", legacy: true, equal: true}, {name: "different project", legacy: true, equal: true, multi: true, conflict: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "policy")
			owner := SelectionPolicyOwner{"starport", "team", "one"}
			store, err := OpenSelectionPolicyStore(t.Context(), path, owner, test.legacy)
			if err != nil {
				t.Fatal(err)
			}
			values := map[string]string{"OPENAI_API_KEY": "valid-old", "STARPORT_OPENAI_API_KEY": "valid-new", "CHOSEN_KEY": "valid-explicit"}
			if test.equal {
				values["OPENAI_API_KEY"] = "valid-new"
			}
			provider := staticCredentialProvider()
			if test.multi {
				provider.Credentials.Fields = append(provider.Credentials.Fields, catalogs.ProviderCredentialField{ID: "project", Kind: catalogs.ProviderCredentialFieldParameter, Required: true, Environment: []string{"OPENAI_PROJECT"}})
				provider.Credentials.Profiles[0].Fields = append(provider.Credentials.Profiles[0].Fields, "project")
				values["OPENAI_PROJECT"] = "old-project"
				values["STARPORT_OPENAI_PROJECT"] = "new-project"
			}
			r := NewResolver(WithEnvironmentLookup(mapLookup(values)), WithSelectionPolicyStore(store))
			h, err := r.Provider(provider, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = h.Resolve(t.Context())
			if test.conflict {
				var conflict *PolicyConflictError
				if !errors.As(err, &conflict) {
					t.Fatalf("upgrade did not refuse changed material: %v", err)
				}
				if strings.Contains(err.Error(), "valid-") {
					t.Fatal("conflict exposed credential material")
				}
				if _, err = h.CachedMaterial(t.Context()); err == nil {
					t.Fatal("conflicting material entered request cache")
				}
				reference, err := ParseReference("env:CHOSEN_KEY")
				if err != nil {
					t.Fatal(err)
				}
				policies := map[catalogs.ProviderCredentialFieldID]ReferencePolicy{"api-key": {Reference: reference}}
				if test.multi {
					ref, err := ParseReference("env:STARPORT_OPENAI_PROJECT")
					if err != nil {
						t.Fatal(err)
					}
					policies["project"] = ReferencePolicy{Reference: ref}
				}
				h, err = r.Provider(provider, policies, false)
				if err != nil {
					t.Fatal(err)
				}
				if _, _, err = h.Resolve(t.Context()); err != nil {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			reopened, err := OpenSelectionPolicyStore(t.Context(), path, owner, true)
			if err != nil {
				t.Fatal(err)
			}
			values["OPENAI_API_KEY"] = "valid-changed-after-acceptance"
			fresh := NewResolver(WithEnvironmentLookup(mapLookup(values)), WithSelectionPolicyStore(reopened))
			h, err = fresh.Provider(provider, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			material, configured, err := h.Resolve(t.Context())
			if err != nil || !configured {
				t.Fatalf("retained policy failed: %v", err)
			}
			if v, _ := material.Value("api-key"); v != "valid-new" {
				t.Fatal("accepted policy reverted")
			}
		})
	}
}

func TestInferencePolicyStoreRefusesCorruptionAndOwnerMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy")
	owner := SelectionPolicyOwner{"starport", "team", "one"}
	if _, err := OpenSelectionPolicyStore(t.Context(), path, owner, true); err != nil {
		t.Fatal(err)
	}
	other := owner
	other.Instance = "two"
	if _, err := OpenSelectionPolicyStore(t.Context(), path, other, false); err == nil {
		t.Fatal("another owner reused policy state")
	}
	record := filepath.Join(path, "policy.json")
	if err := os.WriteFile(record, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSelectionPolicyStore(t.Context(), path, owner, false); err == nil {
		t.Fatal("corrupt state reset to current policy")
	}
	data, err := os.ReadFile(record)
	if err != nil || string(data) != "invalid" {
		t.Fatal("corrupt state was changed")
	}
}

func TestInferencePolicyConcurrentAcceptance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy")
	owner := SelectionPolicyOwner{"starport", "team", "one"}
	left, err := OpenSelectionPolicyStore(t.Context(), path, owner, true)
	if err != nil {
		t.Fatal(err)
	}
	right, err := OpenSelectionPolicyStore(t.Context(), path, owner, true)
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for i := range 8 {
		group.Go(func() {
			store := left
			if i%2 == 1 {
				store = right
			}
			if err := store.Accept(t.Context(), "openai"); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	policy, err := left.Policy(t.Context(), "openai")
	if err != nil || policy != InferencePolicyCurrent {
		t.Fatal("concurrent acceptance did not retain current policy")
	}
}

func TestInferenceCachedMaterialDoesNotReadPolicyOrEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy")
	store, err := OpenSelectionPolicyStore(t.Context(), path, SelectionPolicyOwner{"starport", "team", "one"}, true)
	if err != nil {
		t.Fatal(err)
	}
	reads := 0
	resolver := NewResolver(WithSelectionPolicyStore(store), WithEnvironmentLookup(func(name string) (string, bool) {
		reads++
		return "valid-same", name == "OPENAI_API_KEY" || name == "STARPORT_OPENAI_API_KEY"
	}))
	handle, err := resolver.Provider(staticCredentialProvider(), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := handle.Resolve(t.Context()); err != nil {
		t.Fatal(err)
	}
	before := reads
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	for range 100 {
		material, err := handle.CachedSource().ResolveMaterial(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if value, _ := material.Value("api-key"); value != "valid-same" {
			t.Fatal("cache changed material")
		}
	}
	if reads != before {
		t.Fatal("request cache read the environment")
	}
}

func TestInferenceSourceSnapshotRejectsMixedVersions(t *testing.T) {
	calls := 0
	source := &testReferenceSource{backend: ReferenceBackendFile, resolve: func(context.Context, Reference) (SourceMaterial, error) {
		calls++
		version := "first"
		if calls > 1 {
			version = "second"
		}
		return NewSourceMaterial(map[string]string{"value": "valid-secret"}, version, time.Time{}, nil), nil
	}}
	snapshot := &policySourceSnapshot{source: source, results: make(map[Reference]capturedSource)}
	first := Reference{backend: ReferenceBackendFile, resource: "/credential", field: "key"}
	if _, err := snapshot.Resolve(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshot.Resolve(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("comparison reread the same source")
	}
	second := first
	second.field = "project"
	if _, err := snapshot.Resolve(t.Context(), second); !IsSourceError(err, SourceErrorUnavailable) {
		t.Fatalf("mixed source versions accepted: %v", err)
	}
}
