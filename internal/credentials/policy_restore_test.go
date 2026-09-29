package credentials

import (
	"context"
	"encoding/json/v2"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starmap/pkg/productfiles"
)

func selectionRestoreFixture(t *testing.T) (string, SelectionPolicyOwner) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "policy")
	owner := SelectionPolicyOwner{"starport", "team", "one"}
	store, err := OpenSelectionPolicyStore(t.Context(), path, owner, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Accept(t.Context(), "openai"); err != nil {
		t.Fatal(err)
	}
	return path, owner
}

func selectionRestoreBytes(t *testing.T, path string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(path, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		body, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(path, name)
		if err != nil {
			return err
		}
		result[relative] = string(body)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestSelectionPolicyRestoreInspectionPreservesHistory(t *testing.T) {
	path, owner := selectionRestoreFixture(t)
	before := selectionRestoreBytes(t, path)
	if err := InspectSelectionPolicyDirectory(t.Context(), path, owner); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, selectionRestoreBytes(t, path)) {
		t.Fatal("inspection changed retained history")
	}
	store, err := OpenSelectionPolicyStore(t.Context(), path, owner, false)
	if err != nil {
		t.Fatal(err)
	}
	for provider, want := range map[string]EnvironmentPolicy{"openai": InferencePolicyCurrent, "anthropic": InferencePolicyLegacy} {
		got, err := store.Policy(t.Context(), catalogs.ProviderID(provider))
		if err != nil || got != want {
			t.Fatalf("policy %s: %s, %v", provider, got, err)
		}
	}
}

func TestSelectionPolicyRestoreInspectionRefusesInvalidHistory(t *testing.T) {
	for _, scenario := range []string{"product", "deployment", "instance", "missing-default", "invalid-default", "noncanonical", "unknown-member", "oversized", "wrong-provider-name", "legacy-provider", "unknown-file", "pending-publication", "directory"} {
		t.Run(scenario, func(t *testing.T) {
			path, owner := selectionRestoreFixture(t)
			file := filepath.Join(path, "policy.json")
			write := func(name string, body []byte) {
				t.Helper()
				if err := os.WriteFile(name, body, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "product":
				owner.Product = "starmap"
			case "deployment":
				owner.Deployment = "other"
			case "instance":
				owner.Instance = "other"
			case "missing-default":
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
			case "invalid-default":
				write(file, []byte("invalid"))
			case "noncanonical":
				body, err := os.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				write(file, append(body, '\n'))
			case "unknown-member":
				body, err := os.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				write(file, append([]byte(`{"unknown":true,`), body[1:]...))
			case "oversized":
				write(file, []byte(strings.Repeat(" ", selectionPolicyRecordLimit+1)))
			case "wrong-provider-name":
				if err := os.Rename(filepath.Join(path, selectionProviderFile("openai")), filepath.Join(path, selectionProviderFile("anthropic"))); err != nil {
					t.Fatal(err)
				}
			case "legacy-provider":
				body, err := json.Marshal(selectionPolicyRecord{Schema: 1, Owner: owner, Provider: "openai", Policy: InferencePolicyLegacy})
				if err != nil {
					t.Fatal(err)
				}
				write(filepath.Join(path, selectionProviderFile("openai")), body)
			case "unknown-file":
				write(filepath.Join(path, "unowned"), []byte("preserve"))
			case "pending-publication":
				write(filepath.Join(path, productfiles.PublicationDirectoryName, "pending.jsonl"), []byte("preserve incomplete receipt"))
			case "directory":
				if err := os.Mkdir(filepath.Join(path, "unowned-directory"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			before := selectionRestoreBytes(t, path)
			if err := InspectSelectionPolicyDirectory(t.Context(), path, owner); err == nil {
				t.Fatal("accepted invalid history")
			}
			if !reflect.DeepEqual(before, selectionRestoreBytes(t, path)) {
				t.Fatal("inspection changed invalid history")
			}
		})
	}
}

func TestSelectionPolicyRestoreInspectionDoesNotCreateMissingState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent")
	owner := SelectionPolicyOwner{"starport", "team", "one"}
	if err := InspectSelectionPolicyDirectory(t.Context(), path, owner); err == nil {
		t.Fatal("accepted missing history")
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("inspection created state")
	}
	if err := InspectSelectionPolicyDirectory(nil, path, owner); err == nil {
		t.Fatal("accepted nil context")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := InspectSelectionPolicyDirectory(ctx, path, owner); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
