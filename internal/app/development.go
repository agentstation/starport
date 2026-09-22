package app

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
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

// developmentFilesDirectory holds stored file bytes under the scratch root.
const developmentFilesDirectory = "files"

// developmentCatalogStateDirectory holds the retained catalog state under the
// scratch root.
const developmentCatalogStateDirectory = "catalog-state"

// developmentScratchPermissions keeps the scratch directories private to the
// user that runs the session.
const developmentScratchPermissions = 0o700

// Development owns one isolated local application and its one-time key.
type Development struct {
	application *App
	url         string
	apiKey      string
	// scratchRoot is the session-owned scratch directory. It holds the stored
	// file bytes and the catalog state the connected runtime retains. The
	// session removes it on close, so a development gateway leaves nothing
	// behind.
	scratchRoot string
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

	cfg.ConfigureDevelopmentRuntime()
	masterKey, err := credentials.GenerateMasterKey()
	if err != nil {
		return nil, fmt.Errorf("generate development master key: %w", err)
	}
	cfg.Security.MasterKey = base64.RawURLEncoding.EncodeToString(masterKey)
	// The file-byte and retained catalog backends use a temporary directory.
	// The session removes it on close and leaves the shared data and user state roots untouched.
	// Later runs inherit no configuration from this temporary state.
	scratchRoot, err := os.MkdirTemp("", "starport-dev-")
	if err != nil {
		return nil, fmt.Errorf("create development scratch directory: %w", err)
	}
	cfg.Files.Path = filepath.Join(scratchRoot, developmentFilesDirectory)
	// An operator who names a state directory keeps it. The default is
	// scratch, so the session retains no catalog state on the machine.
	// Starmap creates its own state directory with native access rules.
	if cfg.Catalog.StateDirectoryIsScratch() {
		cfg.Catalog.StateDirectory = filepath.Join(scratchRoot, developmentCatalogStateDirectory)
	}
	if err := os.MkdirAll(cfg.Files.Path, developmentScratchPermissions); err != nil {
		return nil, errors.Join(fmt.Errorf("create development scratch directory: %w", err), os.RemoveAll(scratchRoot))
	}
	if err := cfg.Validate(); err != nil {
		return nil, errors.Join(fmt.Errorf("validate development config: %w", err), os.RemoveAll(scratchRoot))
	}

	store, err := storage.Open(cfg.Storage.RuntimeStorage())
	if err != nil {
		return nil, errors.Join(fmt.Errorf("open development storage: %w", err), os.RemoveAll(scratchRoot))
	}
	// The session issues a gateway key only when authentication requires one.
	// With authentication disabled, it prints no key and requires no pasted key.
	var apiKey string
	if cfg.Security.AuthMode.Effective() != config.AuthModeDisabled {
		issued, err := setup.InitializeAPIKey(ctx, store, developmentAPIKeyName)
		if err != nil {
			return nil, errors.Join(err, store.Close())
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
		return nil, errors.Join(fmt.Errorf("create development application: %w", err), os.RemoveAll(scratchRoot))
	}

	return &Development{
		application: application,
		url:         "http://" + net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port)),
		apiKey:      apiKey,
		scratchRoot: scratchRoot,
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
	err := runtime.application.Close(ctx)
	if runtime.scratchRoot != "" {
		err = errors.Join(err, os.RemoveAll(runtime.scratchRoot))
	}
	return err
}
