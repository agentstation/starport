package app

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/localauth"
	"github.com/agentstation/starport/internal/setup"
	"github.com/agentstation/starport/internal/storage"
)

const developmentAPIKeyName = "local-development"

// Development owns one isolated local application and its one-time key.
type Development struct {
	application *App
	url         string
	apiKey      string
	// filesRoot is the session-owned scratch directory holding stored file
	// bytes. The session removes it on close, so a development gateway leaves
	// nothing behind.
	filesRoot string
}

// NewDevelopment creates an in-memory gateway bound to loopback.
func NewDevelopment(ctx context.Context, cfg *config.Config) (*Development, error) {
	if ctx == nil {
		return nil, errors.New("development context is required")
	}
	if cfg == nil {
		return nil, ErrConfigRequired
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	cfg.ConfigureDevelopmentRuntime()
	masterKey, err := credentials.GenerateMasterKey()
	if err != nil {
		return nil, fmt.Errorf("generate development master key: %w", err)
	}
	cfg.Security.MasterKey = base64.RawURLEncoding.EncodeToString(masterKey)
	// Stored file bytes have no in-memory backend, so the session owns a
	// temporary directory instead and removes it on close. What matters for
	// the development promise is that the shared data directory stays
	// untouched: a scratch directory the session deletes is working memory,
	// not configuration another run would inherit.
	filesRoot, err := os.MkdirTemp("", "starport-dev-files-")
	if err != nil {
		return nil, fmt.Errorf("create development file storage: %w", err)
	}
	cfg.Files.Path = filesRoot
	if err := cfg.Validate(); err != nil {
		return nil, errors.Join(fmt.Errorf("validate development config: %w", err), os.RemoveAll(filesRoot))
	}

	store, err := storage.Open(cfg.Storage.RuntimeStorage())
	if err != nil {
		return nil, errors.Join(fmt.Errorf("open development storage: %w", err), os.RemoveAll(filesRoot))
	}
	// A key is issued only when one is needed. With authentication disabled
	// the session has nothing to print and nothing to paste, and minting a key
	// anyway would teach the wrong thing about the mode it is running in.
	var apiKey string
	if cfg.Security.AuthMode.Effective() != config.AuthModeDisabled {
		issued, err := setup.InitializeAPIKey(ctx, store, developmentAPIKeyName)
		if err != nil {
			return nil, errors.Join(err, store.Close())
		}
		apiKey = issued.Secret
	}

	claimed := false
	application, err := New(cfg, func(options *buildOptions) {
		options.factories.openStorage = func(config.StorageConfig) (storage.KVStore, error) {
			claimed = true
			return store, nil
		}
	})
	if err != nil {
		if !claimed {
			err = errors.Join(err, store.Close())
		}
		return nil, errors.Join(fmt.Errorf("create development application: %w", err), os.RemoveAll(filesRoot))
	}

	return &Development{
		application: application,
		url:         "http://" + net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port)),
		apiKey:      apiKey,
		filesRoot:   filesRoot,
	}, nil
}

// URL returns the loopback gateway URL.
func (runtime *Development) URL() string {
	if runtime == nil {
		return ""
	}
	return runtime.url
}

// ConsoleURL returns a one-time link that signs one browser in to this
// development gateway.
//
// The link is minted here rather than read from a file so it names this
// session's address and this session's port. A development gateway picks both
// at start, and a link that pointed at the configured gateway would sign the
// operator in to a different process than the one they just started.
func (runtime *Development) ConsoleURL() (string, error) {
	if runtime == nil || runtime.application == nil {
		return "", errors.New("development application is required")
	}
	ticket, err := runtime.application.localGate.MintTicket(time.Now())
	if err != nil {
		return "", fmt.Errorf("mint a console launch ticket: %w", err)
	}
	return localauth.LaunchURL(runtime.url, ticket)
}

// APIKey returns the one-time ephemeral gateway credential.
func (runtime *Development) APIKey() string {
	if runtime == nil {
		return ""
	}
	return runtime.apiKey
}

// Run starts the development gateway and closes it on exit.
func (runtime *Development) Run(ctx context.Context) error {
	if runtime == nil || runtime.application == nil {
		return errors.New("development application is required")
	}
	return runtime.application.Run(ctx)
}

// Close releases the development gateway and removes its scratch file
// storage. The CLI calls it on every exit path, started or not.
func (runtime *Development) Close(ctx context.Context) error {
	if runtime == nil || runtime.application == nil {
		return nil
	}
	err := runtime.application.Close(ctx)
	if runtime.filesRoot != "" {
		err = errors.Join(err, os.RemoveAll(runtime.filesRoot))
	}
	return err
}
