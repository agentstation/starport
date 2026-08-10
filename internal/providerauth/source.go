// Package providerauth owns renewable credentials for provider inference.
// It does not read or reuse Starmap catalog-acquisition credentials.
package providerauth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const defaultRefreshBefore = 2 * time.Minute

var (
	// ErrSourceRequired reports a missing credential source.
	ErrSourceRequired = errors.New("provider credential source is required")
	// ErrTokenEmpty reports a credential source that returned no token value.
	ErrTokenEmpty = errors.New("provider credential token is empty")
	// ErrTokenExpiryRequired reports a renewable token without an expiry.
	ErrTokenExpiryRequired = errors.New("provider credential token expiry is required")
	// ErrTokenStale reports a credential source that returned a token too close
	// to expiry for a new inference request.
	ErrTokenStale = errors.New("provider credential token is stale")
)

// Mode selects the operator-owned inference credential source.
type Mode string

const (
	// ModeStatic uses the configured provider secret.
	ModeStatic Mode = "static"
	// ModeDefault uses the cloud platform's default credential chain.
	ModeDefault Mode = "default"
)

// Validate checks a configured mode. An empty mode is valid for providers that
// use a directly configured secret.
func (m Mode) Validate() error {
	switch m {
	case "", ModeStatic, ModeDefault:
		return nil
	default:
		return fmt.Errorf("provider auth mode %q is invalid", m)
	}
}

// Token is one provider inference bearer token.
type Token struct {
	Value     string
	ExpiresAt time.Time
}

// Source supplies provider inference bearer tokens.
type Source interface {
	Token(context.Context) (Token, error)
}

// SourceFunc adapts a function to Source.
type SourceFunc func(context.Context) (Token, error)

// Token supplies one provider inference bearer token.
func (f SourceFunc) Token(ctx context.Context) (Token, error) {
	return f(ctx)
}

// RefreshOptions configures a refreshable credential source.
type RefreshOptions struct {
	RefreshBefore time.Duration
	Now           func() time.Time
}

// NewRefreshingSource caches a token and replaces it before expiry. One caller
// performs each refresh. Other callers can stop waiting through their own
// contexts.
func NewRefreshingSource(upstream Source, options RefreshOptions) (Source, error) {
	if upstream == nil {
		return nil, ErrSourceRequired
	}
	if options.RefreshBefore < 0 {
		return nil, errors.New("provider credential refresh window cannot be negative")
	}
	if options.RefreshBefore == 0 {
		options.RefreshBefore = defaultRefreshBefore
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &refreshingSource{
		upstream:      upstream,
		refreshBefore: options.RefreshBefore,
		now:           options.Now,
	}, nil
}

type refreshingSource struct {
	upstream      Source
	refreshBefore time.Duration
	now           func() time.Time

	mu      sync.Mutex
	token   Token
	refresh *refreshAttempt
}

type refreshAttempt struct {
	done            chan struct{}
	err             error
	retryForWaiters bool
}

func (s *refreshingSource) Token(ctx context.Context) (Token, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		s.mu.Lock()
		if tokenFresh(s.token, s.now(), s.refreshBefore) {
			token := s.token
			s.mu.Unlock()
			return token, nil
		}
		if s.refresh != nil {
			attempt := s.refresh
			s.mu.Unlock()
			select {
			case <-ctx.Done():
				return Token{}, ctx.Err()
			case <-attempt.done:
				if err := ctx.Err(); err != nil {
					return Token{}, err
				}
				if attempt.err == nil {
					continue
				}
				if attempt.retryForWaiters {
					continue
				}
				return Token{}, attempt.err
			}
		}

		attempt := &refreshAttempt{done: make(chan struct{})}
		s.refresh = attempt
		s.mu.Unlock()

		token, err := s.upstream.Token(ctx)
		retryForWaiters := err != nil && ctx.Err() != nil && errors.Is(err, ctx.Err())
		if err == nil {
			err = validateToken(token, s.now(), s.refreshBefore)
		}

		if err != nil {
			err = fmt.Errorf("refresh provider credential: %w", err)
		}

		s.mu.Lock()
		if err == nil {
			s.token = token
		}
		attempt.err = err
		attempt.retryForWaiters = retryForWaiters
		s.refresh = nil
		close(attempt.done)
		s.mu.Unlock()

		if err != nil {
			return Token{}, err
		}
		return token, nil
	}
}

func validateToken(token Token, now time.Time, refreshBefore time.Duration) error {
	if strings.TrimSpace(token.Value) == "" {
		return ErrTokenEmpty
	}
	if token.ExpiresAt.IsZero() {
		return ErrTokenExpiryRequired
	}
	if !now.Add(refreshBefore).Before(token.ExpiresAt) {
		return ErrTokenStale
	}
	return nil
}

func tokenFresh(token Token, now time.Time, refreshBefore time.Duration) bool {
	return validateToken(token, now, refreshBefore) == nil
}
