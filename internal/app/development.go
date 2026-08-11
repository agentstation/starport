package app

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strconv"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/setup"
	"github.com/agentstation/starport/internal/storage"
)

const developmentIdentityName = "local-development"

// Development owns one isolated local application and its one-time key.
type Development struct {
	application *App
	url         string
	apiKey      string
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
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate development config: %w", err)
	}

	store, err := storage.Open(cfg.Storage.RuntimeStorage())
	if err != nil {
		return nil, fmt.Errorf("open development storage: %w", err)
	}
	issued, err := setup.InitializeIdentity(ctx, store, developmentIdentityName)
	if err != nil {
		return nil, errors.Join(err, store.Close())
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
		return nil, fmt.Errorf("create development application: %w", err)
	}

	return &Development{
		application: application,
		url:         "http://" + net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port)),
		apiKey:      issued.Secret,
	}, nil
}

// URL returns the loopback gateway URL.
func (runtime *Development) URL() string {
	if runtime == nil {
		return ""
	}
	return runtime.url
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

// Close releases a development gateway that did not start.
func (runtime *Development) Close(ctx context.Context) error {
	if runtime == nil || runtime.application == nil {
		return nil
	}
	return runtime.application.Close(ctx)
}
