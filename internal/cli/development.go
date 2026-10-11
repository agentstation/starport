package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"time"
)

const (
	developmentCloseTimeout = 5 * time.Second
	flagEphemeral           = "ephemeral"
)

var (
	// ErrDevelopmentStarterRequired reports a missing development boundary.
	ErrDevelopmentStarterRequired = errors.New("development starter is required")
	// ErrDevelopmentSessionInvalid reports an incomplete development session.
	ErrDevelopmentSessionInvalid = errors.New("development session is invalid")
)

// DevelopmentStarter creates one local gateway session. A persistent start
// that initializes a clean root prints the new key through the delivery, the
// way serve does, so a failed write rolls the initialization back.
type DevelopmentStarter func(context.Context, DevelopmentOptions, CredentialDelivery) (DevelopmentSession, error)

// DevelopmentOptions carries the dev choices for one command run.
type DevelopmentOptions struct {
	GatewayOptions
	// Ephemeral keeps every store in memory or in a scratch directory that the
	// session removes on exit. Otherwise the session uses the platform roots.
	Ephemeral bool
}

// DevelopmentSession contains one local gateway and the key it minted, if any.
type DevelopmentSession struct {
	URL string
	// APIKey is a gateway key that this session minted and has not printed.
	// An ephemeral session mints one on every run. A persistent session
	// delivers its first-run key before it returns and sets none.
	APIKey string
	// ConsoleURL signs one browser in to this session. It is a separate value
	// from URL and from APIKey because it is a separate credential: it opens a
	// console session and grants no gateway API key, and it is spent the first
	// time it is followed.
	ConsoleURL string
	// AuthDisabled reports that the session serves requests without a gateway
	// API key.
	AuthDisabled bool
	Run          func(context.Context) error
	Close        func(context.Context) error
}

// validate rejects a session no one could use.
//
// A session that requires a key can carry none: a persistent second run
// accepts the key that the first run printed. A session that requires no key
// must not have minted one, because a printed key that the gateway ignores
// tells the operator that the gateway checks keys.
func (session DevelopmentSession) validate() error {
	if session.URL == "" || session.Run == nil || session.Close == nil {
		return ErrDevelopmentSessionInvalid
	}
	if session.AuthDisabled && session.APIKey != "" {
		return ErrDevelopmentSessionInvalid
	}
	return nil
}

// writeDevelopmentResult prints the URL, the authentication state, and the
// console link on every run, and the key line only for a key that the session
// minted.
func writeDevelopmentResult(writer io.Writer, session DevelopmentSession) error {
	if session.AuthDisabled {
		_, err := fmt.Fprintf(
			writer,
			"Starport development gateway\nURL: %s\nAuthentication: disabled (no gateway API key required)\n",
			session.URL,
		)
		return err
	}
	if _, err := fmt.Fprintf(
		writer,
		"Starport development gateway\nURL: %s\nAuthentication: required\n",
		session.URL,
	); err != nil {
		return err
	}
	if session.APIKey != "" {
		if _, err := fmt.Fprintf(writer, "Gateway API key (shown once): %s\n", session.APIKey); err != nil {
			return err
		}
	}
	return writeDevelopmentConsole(writer, session)
}

// writeDevelopmentConsole names the console link when there is one.
//
// It is printed whether or not a browser is going to be opened. An operator on
// a machine reached over SSH, or one whose browser did not come up, still needs
// the link, and a session that opened something and said nothing leaves them
// with no way to retry.
func writeDevelopmentConsole(writer io.Writer, session DevelopmentSession) error {
	if session.ConsoleURL == "" {
		return nil
	}
	_, err := fmt.Fprintf(writer, "Console (one-time launch link): %s\n", session.ConsoleURL)
	return err
}

const (
	// gatewayWaitTimeout bounds the wait for a development gateway to listen.
	// It is generous because the cost of waiting too long is a browser that
	// opens late, and the cost of giving up too early is one that opens on a
	// connection error the operator then has to reason about.
	gatewayWaitTimeout = 15 * time.Second
	gatewayWaitPoll    = 25 * time.Millisecond
	gatewayDialTimeout = time.Second
)

// openConsoleWhenReady follows the console link once the gateway answers.
//
// The wait is a TCP dial rather than a fixed delay because the race is real and
// short: the banner is printed before the listener is up, and a browser that
// arrives first shows a connection error. A dial asks the smallest question
// that settles it — no route, no protocol, and no credential.
//
// Nothing is printed here. This runs beside a gateway that owns the terminal,
// and the link is already in the banner, so a browser that will not start
// leaves the operator exactly where a printed complaint would have.
func openConsoleWhenReady(ctx context.Context, deps Dependencies, session DevelopmentSession) {
	if !waitForGateway(ctx, session.URL) {
		return
	}
	_ = browserOpener(deps)(session.ConsoleURL)
}

func waitForGateway(ctx context.Context, rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return false
	}
	deadline := time.Now().Add(gatewayWaitTimeout)
	// The dialer carries the context as well as the per-attempt timeout, so an
	// operator who interrupts a starting gateway does not wait out a dial that
	// has already stopped mattering.
	dialer := net.Dialer{Timeout: gatewayDialTimeout}
	for {
		conn, err := dialer.DialContext(ctx, "tcp", parsed.Host)
		if err == nil {
			_ = conn.Close()
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(gatewayWaitPoll):
		}
	}
}

func closeDevelopmentSession(ctx context.Context, session DevelopmentSession, cause error) error {
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), developmentCloseTimeout)
	defer cancel()
	if err := session.Close(closeCtx); err != nil {
		return errors.Join(cause, fmt.Errorf("close development session: %w", err))
	}
	return cause
}
