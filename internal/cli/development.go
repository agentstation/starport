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
type DevelopmentStarter func(context.Context) (DevelopmentSession, error)

// DevelopmentSession contains one ephemeral gateway and its one-time key.
type DevelopmentSession struct {
	URL    string
	APIKey string
	Run    func(context.Context) error
	Close  func(context.Context) error
}

func (session DevelopmentSession) validate() error {
	if session.URL == "" || session.APIKey == "" || session.Run == nil || session.Close == nil {
		return ErrDevelopmentSessionInvalid
	}
	return nil
}

func writeDevelopmentResult(writer io.Writer, session DevelopmentSession) error {
	_, err := fmt.Fprintf(
		writer,
		"Starport development gateway\nURL: %s\nGateway API key (shown once): %s\n",
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
