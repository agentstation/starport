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
)

const gatewayKeyPrefix = "STARPORT"

const gatewayEntropyLength = 42

var gatewayEntropyEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// ErrIssuerRequired reports an absent identity repository.
var ErrIssuerRequired = errors.New("identity issuer repository is required")

// IssueRequest contains the durable attributes for a new gateway identity.
type IssueRequest struct {
	Name      string
	Scopes    []string
	Metadata  map[string]any
	ExpiresAt *time.Time
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
	generate   func() (generatedCredential, error)
	now        func() time.Time
}

type generatedCredential struct {
	id     string
	secret string
}

// NewIssuer returns an identity issuer backed by repository.
func NewIssuer(repository Repository) (*Issuer, error) {
	if repository == nil {
		return nil, ErrIssuerRequired
	}
	return &Issuer{
		repository: repository,
		generate:   generateCredential,
		now:        time.Now,
	}, nil
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

	credential, err := i.generate()
	if err != nil {
		return IssueResult{}, fmt.Errorf("generate gateway credential: %w", err)
	}
	digest := sha256.Sum256([]byte(credential.secret))
	apiKey := APIKey{
		ID:        credential.id,
		Name:      request.Name,
		Hash:      hex.EncodeToString(digest[:]),
		Scopes:    append([]string(nil), request.Scopes...),
		Metadata:  cloneMap(request.Metadata),
		Active:    true,
		CreatedAt: i.now().UTC(),
		ExpiresAt: cloneTime(request.ExpiresAt),
	}
	if err := apiKey.Validate(); err != nil {
		return IssueResult{}, err
	}
	record, err := create(ctx, apiKey)
	if err != nil {
		return IssueResult{}, fmt.Errorf("create identity: %w", err)
	}
	return IssueResult{APIKey: record.APIKey, Secret: credential.secret}, nil
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
