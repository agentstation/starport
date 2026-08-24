package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

const developmentCloseTimeout = 5 * time.Second

var (
	// ErrDevelopmentStarterRequired reports a missing development boundary.
	ErrDevelopmentStarterRequired = errors.New("development starter is required")
	// ErrDevelopmentSessionInvalid reports an incomplete development session.
	ErrDevelopmentSessionInvalid = errors.New("development session is invalid")
)

// DevelopmentStarter creates one isolated local gateway session.
type DevelopmentStarter func(context.Context, GatewayOptions) (DevelopmentSession, error)

// DevelopmentSession contains one ephemeral gateway and its one-time key.
type DevelopmentSession struct {
	URL    string
	APIKey string
	// AuthDisabled reports that the session serves requests without a gateway
	// API key. It is what makes an empty APIKey legitimate rather than a bug.
	AuthDisabled bool
	Run          func(context.Context) error
	Close        func(context.Context) error
}

// validate rejects a session no one could use.
//
// The key rule is an equivalence, not two independent checks: a session that
// requires a key must carry one to print, and a session that requires none
// must not have minted one. Either mismatch means the runtime and the mode
// disagree, and printing a key the gateway ignores — or printing none when one
// is needed — leaves the operator with no way to make the first request.
func (session DevelopmentSession) validate() error {
	if session.URL == "" || session.Run == nil || session.Close == nil {
		return ErrDevelopmentSessionInvalid
	}
	if session.AuthDisabled != (session.APIKey == "") {
		return ErrDevelopmentSessionInvalid
	}
	return nil
}

func writeDevelopmentResult(writer io.Writer, session DevelopmentSession) error {
	if session.AuthDisabled {
		_, err := fmt.Fprintf(
			writer,
			"Starport development gateway\nURL: %s\nAuthentication: disabled (no gateway API key required)\n",
			session.URL,
		)
		return err
	}
	_, err := fmt.Fprintf(
		writer,
		"Starport development gateway\nURL: %s\nAuthentication: required\nGateway API key (shown once): %s\n",
		session.URL,
		session.APIKey,
	)
	return err
}

func closeDevelopmentSession(ctx context.Context, session DevelopmentSession, cause error) error {
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), developmentCloseTimeout)
	defer cancel()
	if err := session.Close(closeCtx); err != nil {
		return errors.Join(cause, fmt.Errorf("close development session: %w", err))
	}
	return cause
}
