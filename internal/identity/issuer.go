package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/agentstation/uuidkey"
	"github.com/google/uuid"

	"github.com/agentstation/starport/internal/limits"
)

const gatewayKeyPrefix = "STARPORT"

const gatewayEntropyLength = 42

var gatewayEntropyEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// ErrIssuerRequired reports an absent identity repository.
var ErrIssuerRequired = errors.New("identity issuer repository is required")

// TenantChecker reports whether a tenant exists. The issuer holds this
// rather than a tenant repository so the key concept never learns how an
// account is stored.
type TenantChecker interface {
	Exists(ctx context.Context, tenantID string) (bool, error)
}

// IssuerOption configures an issuer.
type IssuerOption func(*Issuer)

// WithTenantChecker makes the issuer refuse a key that names an account that
// does not exist. An issuer built without one validates the tenant ID format
// but cannot confirm the account, so every production call site supplies it.
func WithTenantChecker(checker TenantChecker) IssuerOption {
	return func(i *Issuer) { i.tenants = checker }
}

// IssueRequest contains the durable attributes for a new gateway identity.
type IssueRequest struct {
	Name string
	// TenantID names the owning account. An empty value issues the key to the
	// canonical tenant.
	TenantID      string
	Scopes        []string
	AllowedModels []string
	Limits        *limits.Limits
	Metadata      map[string]any
	ExpiresAt     *time.Time
}

// IssueResult contains a new identity and its one-time plaintext credential.
// The repository stores only the credential hash.
type IssueResult struct {
	APIKey APIKey
	Secret string
}

// Issuer creates gateway credentials and their durable identity records.
type Issuer struct {
	repository Repository
	tenants    TenantChecker
	generate   func() (generatedCredential, error)
	now        func() time.Time
}

type generatedCredential struct {
	id     string
	secret string
}

// NewIssuer returns an identity issuer backed by repository.
func NewIssuer(repository Repository, options ...IssuerOption) (*Issuer, error) {
	if repository == nil {
		return nil, ErrIssuerRequired
	}
	issuer := &Issuer{
		repository: repository,
		generate:   generateCredential,
		now:        time.Now,
	}
	for _, option := range options {
		option(issuer)
	}
	return issuer, nil
}

// Issue creates one identity and returns its plaintext credential once.
func (i *Issuer) Issue(ctx context.Context, request IssueRequest) (IssueResult, error) {
	if i == nil || i.repository == nil {
		return IssueResult{}, ErrIssuerRequired
	}
	return i.issue(ctx, request, i.repository.Create)
}

// IssueInitial atomically creates the first identity in a repository.
func (i *Issuer) IssueInitial(ctx context.Context, request IssueRequest) (IssueResult, error) {
	if i == nil || i.repository == nil {
		return IssueResult{}, ErrIssuerRequired
	}
	return i.issue(ctx, request, i.repository.CreateInitial)
}

func (i *Issuer) issue(
	ctx context.Context,
	request IssueRequest,
	create func(context.Context, APIKey) (Record, error),
) (IssueResult, error) {
	if i == nil || i.repository == nil || i.generate == nil || i.now == nil || create == nil {
		return IssueResult{}, ErrIssuerRequired
	}
	if err := ctx.Err(); err != nil {
		return IssueResult{}, err
	}

	tenantID := ResolveTenantID(request.TenantID)
	if err := i.requireTenant(ctx, tenantID); err != nil {
		return IssueResult{}, err
	}

	credential, err := i.generate()
	if err != nil {
		return IssueResult{}, fmt.Errorf("generate gateway credential: %w", err)
	}
	digest := sha256.Sum256([]byte(credential.secret))
	apiKey := APIKey{
		ID:            credential.id,
		Name:          request.Name,
		Hash:          hex.EncodeToString(digest[:]),
		TenantID:      tenantID,
		Scopes:        append([]string(nil), request.Scopes...),
		AllowedModels: append([]string(nil), request.AllowedModels...),
		Limits:        request.Limits.Clone(),
		Metadata:      cloneMap(request.Metadata),
		Active:        true,
		CreatedAt:     i.now().UTC(),
		ExpiresAt:     cloneTime(request.ExpiresAt),
	}
	if err := apiKey.Validate(); err != nil {
		return IssueResult{}, err
	}
	record, err := create(ctx, apiKey)
	if err != nil {
		return IssueResult{APIKey: apiKey, Secret: credential.secret}, fmt.Errorf("create identity: %w", err)
	}
	return IssueResult{APIKey: record.APIKey, Secret: credential.secret}, nil
}

// requireTenant refuses a key that names an account that does not exist. A key
// issued against a missing tenant would authenticate and then resolve to no
// limits and no credential policy, which is worse than refusing it here.
func (i *Issuer) requireTenant(ctx context.Context, tenantID string) error {
	if i.tenants == nil {
		return nil
	}
	exists, err := i.tenants.Exists(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("check tenant %q: %w", tenantID, err)
	}
	if !exists {
		return fmt.Errorf("%w: %s", ErrUnknownTenant, tenantID)
	}
	return nil
}

func generateCredential() (generatedCredential, error) {
	keyID, err := uuid.NewRandom()
	if err != nil {
		return generatedCredential{}, err
	}
	key, err := uuidkey.Encode(keyID.String(), uuidkey.WithoutHyphens)
	if err != nil {
		return generatedCredential{}, err
	}
	entropyBytes := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, entropyBytes); err != nil {
		return generatedCredential{}, err
	}
	entropy := gatewayEntropyEncoding.EncodeToString(entropyBytes)[:gatewayEntropyLength]
	value := uuidkey.APIKey{Prefix: gatewayKeyPrefix, Key: key, Entropy: entropy}
	return generatedCredential{
		id:     fmt.Sprintf("%s_%s", value.Prefix, value.Key),
		secret: value.String(),
	}, nil
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}
