package identity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/agentstation/uuidkey"

	"github.com/agentstation/starport/internal/storage"
	"github.com/agentstation/starport/internal/tenant"
)

func TestGeneratedCredentialUsesParseableUUIDKeyFormat(t *testing.T) {
	credential, err := generateCredential()
	if err != nil {
		t.Fatalf("generate credential: %v", err)
	}
	parsed, err := uuidkey.ParseAPIKey(credential.secret)
	if err != nil {
		t.Fatalf("parse generated credential: %v", err)
	}
	if parsed.Prefix != gatewayKeyPrefix {
		t.Errorf("credential prefix = %q, want %q", parsed.Prefix, gatewayKeyPrefix)
	}
	if credential.id != parsed.Prefix+"_"+parsed.Key.String() {
		t.Errorf("credential ID = %q, want prefix and encoded key", credential.id)
	}
}

func TestIssuerStoresOnlyCredentialHash(t *testing.T) {
	repository, err := Open(storage.NewMockStore())
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.FixedZone("test", -5*60*60))
	issuer := &Issuer{
		repository: repository,
		generate: func() (generatedCredential, error) {
			return generatedCredential{id: "STARPORT_TEST", secret: "gateway-secret"}, nil
		},
		now: func() time.Time { return createdAt },
	}

	result, err := issuer.Issue(context.Background(), IssueRequest{
		Name: "local-admin", Scopes: []string{"*"}, Metadata: map[string]any{"source": "setup"},
	})
	if err != nil {
		t.Fatalf("issue identity: %v", err)
	}
	if result.Secret != "gateway-secret" {
		t.Fatalf("secret = %q", result.Secret)
	}
	if result.APIKey.CreatedAt.Location() != time.UTC {
		t.Errorf("created-at location = %v, want UTC", result.APIKey.CreatedAt.Location())
	}

	digest := sha256.Sum256([]byte(result.Secret))
	record, err := repository.GetByHash(context.Background(), hex.EncodeToString(digest[:]))
	if err != nil {
		t.Fatalf("get issued identity: %v", err)
	}
	if record.APIKey.Hash == result.Secret {
		t.Fatal("repository stored the plaintext credential")
	}
	if record.APIKey.Name != "local-admin" || !record.APIKey.HasScope("admin") {
		t.Errorf("stored identity = %#v", record.APIKey)
	}
}

func TestIssuerRejectsInvalidIdentityBeforeStorage(t *testing.T) {
	repository, err := Open(storage.NewMockStore())
	if err != nil {
		t.Fatal(err)
	}
	issuer := &Issuer{
		repository: repository,
		generate: func() (generatedCredential, error) {
			return generatedCredential{id: "STARPORT_TEST", secret: "gateway-secret"}, nil
		},
		now: time.Now,
	}

	_, err = issuer.Issue(context.Background(), IssueRequest{Name: "invalid name", Scopes: []string{"*"}})
	if !errors.Is(err, ErrInvalidName) {
		t.Fatalf("issue error = %v, want %v", err, ErrInvalidName)
	}
	records, listErr := repository.List(context.Background(), 10, 0)
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(records) != 0 {
		t.Fatalf("stored identities = %d, want 0", len(records))
	}
}

func TestIssuerReturnsCredentialOnAmbiguousStorageFailure(t *testing.T) {
	store := storage.NewMockStore()
	repository, err := Open(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	issuer, err := NewIssuer(repository)
	if err != nil {
		t.Fatal(err)
	}
	result, err := issuer.IssueInitial(context.Background(), IssueRequest{
		Name: "local-admin", Scopes: []string{"*"},
	})
	if err == nil {
		t.Fatal("IssueInitial() returned no storage error")
	}
	if result.Secret == "" || result.APIKey.ID == "" {
		t.Fatalf("ambiguous failure result = %#v", result)
	}
	if _, parseErr := uuidkey.ParseAPIKey(result.Secret); parseErr != nil {
		t.Fatalf("parse returned credential: %v", parseErr)
	}
}

func TestNewIssuerRequiresRepository(t *testing.T) {
	_, err := NewIssuer(nil)
	if !errors.Is(err, ErrIssuerRequired) {
		t.Fatalf("NewIssuer() error = %v, want %v", err, ErrIssuerRequired)
	}
	var issuer *Issuer
	_, err = issuer.Issue(context.Background(), IssueRequest{})
	if !errors.Is(err, ErrIssuerRequired) {
		t.Fatalf("nil issuer error = %v, want %v", err, ErrIssuerRequired)
	}
}

func TestIssueInitialHasOneConcurrentWinner(t *testing.T) {
	repository, err := Open(storage.NewMockStore())
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for index := range 2 {
		index := index
		go func() {
			issuer := &Issuer{
				repository: repository,
				generate: func() (generatedCredential, error) {
					return generatedCredential{
						id: "STARPORT_" + string(rune('A'+index)), secret: "secret-" + string(rune('A'+index)),
					}, nil
				},
				now: time.Now,
			}
			_, issueErr := issuer.IssueInitial(context.Background(), IssueRequest{
				Name: "admin-" + string(rune('A'+index)), Scopes: []string{"*"},
			})
			results <- issueErr
		}()
	}
	successes := 0
	conflicts := 0
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrConflict):
			conflicts++
		default:
			t.Fatalf("IssueInitial() error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes = %d, conflicts = %d", successes, conflicts)
	}
}

// TestIssuerRefusesAnUnknownTenant is the AON2 acceptance case. A key issued
// against a tenant that does not exist would authenticate and then resolve to
// no limits and no credential policy, which is a worse failure than refusing
// the key at issue time.
func TestIssuerRefusesAnUnknownTenant(t *testing.T) {
	store := storage.NewMockStore()
	repository, err := Open(store)
	if err != nil {
		t.Fatal(err)
	}
	tenants, err := tenant.Open(store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := tenants.EnsureDefault(ctx); err != nil {
		t.Fatal(err)
	}

	issuer, err := NewIssuer(repository, WithTenantChecker(tenants))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := issuer.Issue(ctx, IssueRequest{
		Name: "Ghost", TenantID: "does-not-exist", Scopes: []string{"chat:write"},
	}); !errors.Is(err, ErrUnknownTenant) {
		t.Fatalf("issue against a missing tenant = %v, want ErrUnknownTenant", err)
	}

	records, err := repository.List(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("refused issue stored %d keys, want 0", len(records))
	}

	// An unset tenant is the canonical tenant, not a missing one, so the same
	// issuer accepts a request that names no account at all.
	issued, err := issuer.Issue(ctx, IssueRequest{Name: "Default-Key", Scopes: []string{"chat:write"}})
	if err != nil {
		t.Fatalf("issue with no tenant: %v", err)
	}
	if issued.APIKey.TenantID != tenant.DefaultID {
		t.Fatalf("issued tenant = %q, want %q", issued.APIKey.TenantID, tenant.DefaultID)
	}
}

// TestIssuerRefusesAMalformedTenantID guards the credential storage key. A
// tenant ID reaches a credential scope, so a separator or a wildcard inside one
// must never survive to storage.
func TestIssuerRefusesAMalformedTenantID(t *testing.T) {
	store := storage.NewMockStore()
	repository, err := Open(store)
	if err != nil {
		t.Fatal(err)
	}
	tenants, err := tenant.Open(store)
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := NewIssuer(repository, WithTenantChecker(tenants))
	if err != nil {
		t.Fatal(err)
	}

	for _, tenantID := range []string{"acme corp", "acme/eu", "*", "tenant:acme"} {
		if _, err := issuer.Issue(context.Background(), IssueRequest{
			Name: "Bad", TenantID: tenantID, Scopes: []string{"chat:write"},
		}); err == nil {
			t.Errorf("issue with tenant %q succeeded, want a refusal", tenantID)
		}
	}
}
