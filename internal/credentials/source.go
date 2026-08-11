package credentials

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	maxCredentialFileBytes = 1 << 20
	sourceScalarField      = "value"
)

// SourceMaterial is one source result before profile assembly. Values and
// source versions stay private outside the credential package.
type SourceMaterial struct {
	values    map[string]string
	version   string
	expiresAt time.Time
	lease     *Lease
}

// NewSourceMaterial creates a caller-owned source result.
func NewSourceMaterial(
	values map[string]string,
	version string,
	expiresAt time.Time,
	lease *Lease,
) SourceMaterial {
	material := SourceMaterial{
		values: make(map[string]string, len(values)), version: version, expiresAt: expiresAt,
	}
	for key, value := range values {
		material.values[key] = value
	}
	if lease != nil {
		copied := *lease
		material.lease = &copied
	}
	return material
}

// Value returns one exact source field.
func (m SourceMaterial) Value(field string) (string, bool) {
	value, exists := m.values[field]
	return value, exists
}

// String returns a secret-free source-material summary.
func (m SourceMaterial) String() string {
	return fmt.Sprintf("credential source material (version=%t, expiry=%t, lease=%t)", m.version != "", !m.expiresAt.IsZero(), m.lease != nil)
}

// GoString returns a secret-free Go-syntax source-material summary.
func (m SourceMaterial) GoString() string { return m.String() }

// ReferenceSource resolves one explicit credential reference.
type ReferenceSource interface {
	Backend() ReferenceBackend
	Resolve(context.Context, Reference) (SourceMaterial, error)
}

type environmentSource struct {
	lookup EnvironmentLookup
}

func (environmentSource) Backend() ReferenceBackend { return ReferenceBackendEnvironment }

func (s environmentSource) Resolve(ctx context.Context, reference Reference) (SourceMaterial, error) {
	if err := ctx.Err(); err != nil {
		return SourceMaterial{}, err
	}
	if reference.field != "" || reference.version != "" ||
		!credentialEnvironmentPattern.MatchString(reference.resource) {
		return SourceMaterial{}, NewSourceError(SourceErrorInvalid, s.Backend())
	}
	value, found := s.lookup(reference.resource)
	if !found || value == "" {
		return SourceMaterial{}, NewSourceError(SourceErrorNotConfigured, s.Backend())
	}
	return NewSourceMaterial(
		map[string]string{sourceScalarField: value},
		reference.resource+"\x00"+value,
		time.Time{},
		nil,
	), nil
}

type fileSource struct{}

func (fileSource) Backend() ReferenceBackend { return ReferenceBackendFile }

func (s fileSource) Resolve(ctx context.Context, reference Reference) (SourceMaterial, error) {
	if err := ctx.Err(); err != nil {
		return SourceMaterial{}, err
	}
	if reference.field != "" || reference.version != "" || !filepath.IsAbs(reference.resource) {
		return SourceMaterial{}, NewSourceError(SourceErrorInvalid, s.Backend())
	}
	file, err := os.Open(reference.resource) // #nosec G304 -- The operator selects the exact credential file.
	if err != nil {
		return SourceMaterial{}, classifySourceIOError(s.Backend(), err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return SourceMaterial{}, classifySourceIOError(s.Backend(), err)
	}
	if !info.Mode().IsRegular() {
		return SourceMaterial{}, NewSourceError(SourceErrorInvalid, s.Backend())
	}
	data, err := io.ReadAll(io.LimitReader(file, maxCredentialFileBytes+1))
	if err != nil {
		return SourceMaterial{}, classifySourceIOError(s.Backend(), err)
	}
	if err := ctx.Err(); err != nil {
		return SourceMaterial{}, err
	}
	if len(data) > maxCredentialFileBytes {
		return SourceMaterial{}, NewSourceError(SourceErrorInvalid, s.Backend())
	}
	if len(data) == 0 {
		return SourceMaterial{}, NewSourceError(SourceErrorNotConfigured, s.Backend())
	}
	return NewSourceMaterial(
		map[string]string{sourceScalarField: string(data)},
		string(data),
		time.Time{},
		nil,
	), nil
}

// SourceErrorKind classifies a secret-free credential source failure.
type SourceErrorKind string

const (
	// SourceErrorNotConfigured means that the selected source has no material.
	SourceErrorNotConfigured SourceErrorKind = "not_configured"
	// SourceErrorDenied means that source access or authentication was denied.
	SourceErrorDenied SourceErrorKind = "denied"
	// SourceErrorInvalid means that the source reference or material is invalid.
	SourceErrorInvalid SourceErrorKind = "invalid"
	// SourceErrorUnavailable means that a configured source could not complete.
	SourceErrorUnavailable SourceErrorKind = "unavailable"
)

// SourceError reports a typed source failure without resource or material.
type SourceError struct {
	Kind    SourceErrorKind
	Backend ReferenceBackend
}

// NewSourceError creates a secret-free typed source error.
func NewSourceError(kind SourceErrorKind, backend ReferenceBackend) error {
	return &SourceError{Kind: kind, Backend: backend}
}

func (e *SourceError) Error() string {
	return fmt.Sprintf("credential source %s is %s", e.Backend, e.Kind)
}

// IsSourceError reports whether an error has the requested source class.
func IsSourceError(err error, kind SourceErrorKind) bool {
	var sourceErr *SourceError
	return errors.As(err, &sourceErr) && sourceErr.Kind == kind
}

func classifySourceIOError(backend ReferenceBackend, err error) error {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return NewSourceError(SourceErrorNotConfigured, backend)
	case errors.Is(err, os.ErrPermission):
		return NewSourceError(SourceErrorDenied, backend)
	default:
		return NewSourceError(SourceErrorUnavailable, backend)
	}
}
