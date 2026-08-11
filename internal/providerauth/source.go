// Package providerauth projects resolved inference credential material into
// protocol-owned authentication values.
package providerauth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/credentials"
)

var (
	// ErrSourceRequired reports a missing credential material source.
	ErrSourceRequired = errors.New("provider credential source is required")
	// ErrTokenEmpty reports material without its selected bearer field.
	ErrTokenEmpty = errors.New("provider credential bearer value is empty")
	// ErrCredentialRedirect reports an attempt to reuse renewable provider
	// credentials on an HTTP redirect.
	ErrCredentialRedirect = errors.New("provider credential redirect is not allowed")
)

// Mode selects the temporary connector authentication path. Catalog profiles
// own the primitive; CDP6 removes this connector projection.
type Mode string

const (
	// ModeStatic supplies material directly to the temporary connector.
	ModeStatic Mode = "static"
	// ModeDefault supplies a catalog-driven renewable material source.
	ModeDefault Mode = "default"
)

// Validate checks a temporary connector mode.
func (m Mode) Validate() error {
	switch m {
	case "", ModeStatic, ModeDefault:
		return nil
	default:
		return fmt.Errorf("provider auth mode %q is invalid", m)
	}
}

// Token is the bearer projection required by the temporary connector
// transport. Expiry is optional because static material has no expiry.
type Token struct {
	Value     string
	ExpiresAt time.Time
}

// Source supplies one bearer projection. Caching and refresh belong to the
// upstream credential material source.
type Source interface {
	Token(context.Context) (Token, error)
}

// SourceFunc adapts a function to a bearer source.
type SourceFunc func(context.Context) (Token, error)

// Token implements Source.
func (f SourceFunc) Token(ctx context.Context) (Token, error) {
	return f(ctx)
}

// NewBearerSource projects one catalog-declared field from named material.
func NewBearerSource(
	source credentials.MaterialSource,
	fieldID catalogs.ProviderCredentialFieldID,
) (Source, error) {
	if source == nil {
		return nil, ErrSourceRequired
	}
	if strings.TrimSpace(string(fieldID)) == "" {
		return nil, errors.New("provider credential bearer field is required")
	}
	return &bearerSource{source: source, fieldID: fieldID}, nil
}

type bearerSource struct {
	source  credentials.MaterialSource
	fieldID catalogs.ProviderCredentialFieldID
}

func (s *bearerSource) Token(ctx context.Context) (Token, error) {
	material, err := s.source.ResolveMaterial(ctx)
	if err != nil {
		return Token{}, err
	}
	value, exists := material.Value(s.fieldID)
	if !exists || strings.TrimSpace(value) == "" {
		return Token{}, ErrTokenEmpty
	}
	expiresAt, _ := material.ExpiresAt()
	return Token{Value: value, ExpiresAt: expiresAt}, nil
}
