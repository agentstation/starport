package app

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"slices"
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
	// scratchRoot is the session-owned scratch directory. It holds the stored
	// file bytes and the catalog state the connected runtime retains. The
	// session removes verified state on close. Changed state remains for recovery.
	scratchRoot string
	scratch     *developmentScratch
}

// NewDevelopment creates an in-memory gateway bound to loopback.
func NewDevelopment(ctx context.Context, cfg *config.Config, options ...Option) (*Development, error) {
	if ctx == nil {
		return nil, errors.New("development context is required")
	}
	if cfg == nil {
		return nil, ErrConfigRequired
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if err := cfg.ConfigureDevelopmentRuntime(); err != nil {
		return nil, err
	}
	masterKey, err := credentials.GenerateMasterKey()
	if err != nil {
		return nil, fmt.Errorf("generate development master key: %w", err)
	}
	cfg.Security.MasterKey = base64.RawURLEncoding.EncodeToString(masterKey)
	// The file-byte and retained catalog backends use a temporary directory.
	// The session removes it on close and leaves the shared data and user state roots untouched.
	// Later runs inherit no configuration from this temporary state.
	scratch, err := prepareDevelopmentScratch(ctx)
	if err != nil {
		return nil, fmt.Errorf("create development scratch directory: %w", err)
	}
	if err := cfg.BindDevelopmentScratch(scratch.path); err != nil {
		return nil, errors.Join(err, scratch.close())
	}
	if err := cfg.Validate(); err != nil {
		return nil, errors.Join(fmt.Errorf("validate development config: %w", err), scratch.close())
	}

	store, err := storage.Open(cfg.Storage.RuntimeStorage())
	if err != nil {
		return nil, errors.Join(fmt.Errorf("open development storage: %w", err), scratch.close())
	}
	// The session issues a gateway key only when authentication requires one.
	// With authentication disabled, it prints no key and requires no pasted key.
	var apiKey string
	if cfg.Security.AuthMode.Effective() != config.AuthModeDisabled {
		issued, err := setup.InitializeAPIKey(ctx, store, developmentAPIKeyName)
		if err != nil {
			return nil, errors.Join(err, store.Close(), scratch.close())
		}
		apiKey = issued.Secret
	}

	claimed := false
	application, err := New(cfg, append(slices.Clone(options), func(options *buildOptions) {
		options.factories.openStorage = func(config.StorageConfig) (storage.KVStore, error) {
			claimed = true
			return store, nil
		}
	})...)
	if err != nil {
		if !claimed {
			err = errors.Join(err, store.Close())
		}
		// Failed composition can include a resource shutdown failure.
		// Without a published record, later runs preserve this directory.
		return nil, errors.Join(fmt.Errorf("create development application; preserve scratch %s: %w", scratch.path, err), scratch.lock.Close())
	}
	if err := scratch.publish(ctx); err != nil {
		closeErr := application.Close(ctx)
		if closeErr != nil {
			return nil, errors.Join(fmt.Errorf("publish development ownership; preserve scratch %s: %w", scratch.path, err), closeErr)
		}
		return nil, errors.Join(err, scratch.close())
	}

	return &Development{
		application: application,
		url:         "http://" + net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port)),
		apiKey:      apiKey,
		scratchRoot: scratch.path,
		scratch:     scratch,
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
// The session creates this link from its current address and port.
// It reads no link from a file. A saved link can identify another gateway.
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

// Close releases the development gateway and removes its scratch directory.
// The CLI calls it on every exit path, started or not.
func (runtime *Development) Close(ctx context.Context) error {
	if runtime == nil || runtime.application == nil {
		return nil
	}
	if err := runtime.application.Close(ctx); err != nil {
		return err
	}
	return runtime.scratch.close()
}
