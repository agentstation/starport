// Package setup owns safe first-run initialization for a local Starport instance.
package setup

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/joho/godotenv"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/storage"
)

const (
	configFileMode = 0o600
	privateDirMode = 0o700

	// OpenAIProviderCredentialEnvironment supplies the local OpenAI inference credential.
	OpenAIProviderCredentialEnvironment = "STARPORT_PROVIDERS_OPENAI_API_KEY"
)

var (
	// ErrPathsRequired reports incomplete managed paths.
	ErrPathsRequired = errors.New("setup paths are required")
	// ErrAlreadyInitialized reports existing configuration or identity storage.
	ErrAlreadyInitialized = errors.New("starport is already initialized")
	// ErrPartialState reports an incomplete managed configuration directory.
	ErrPartialState = errors.New("starport setup state is incomplete")
	// ErrProviderRequired reports an absent provider selection.
	ErrProviderRequired = errors.New("setup provider is required")
	// ErrUnsupportedProvider reports a provider outside the local setup profiles.
	ErrUnsupportedProvider = errors.New("setup supports only OpenAI or Ollama")
	// ErrProviderCredentialRequired reports an absent OpenAI inference credential.
	ErrProviderCredentialRequired = errors.New("OpenAI provider credential is required")
)

// State describes the durable local setup state.
type State string

const (
	// StateAbsent means the managed configuration directory does not exist.
	StateAbsent State = "absent"
	// StatePartial means the directory exists without both required artifacts.
	StatePartial State = "partial"
	// StateReady means the configuration file and identity store both exist.
	StateReady State = "ready"
)

// Request contains the explicit choices for local initialization.
type Request struct {
	Provider           catalogs.ProviderID
	ProviderCredential string
	IdentityName       string
}

// Result contains initialized paths and the one-time gateway credential.
type Result struct {
	Provider     catalogs.ProviderID
	IdentityName string
	ConfigFile   string
	DataDir      string
	APIKey       string
}

// Service initializes one local configuration and identity store.
type Service struct {
	paths             config.Paths
	openStore         func(string) (storage.KVStore, error)
	generateMasterKey func() (string, error)
}

// New returns a local setup service for the supplied managed paths.
func New(paths config.Paths) *Service {
	return &Service{
		paths:             paths,
		openStore:         openLocalStore,
		generateMasterKey: generateMasterKey,
	}
}

// Initialize creates local configuration and one named gateway identity.
// It never replaces an existing configuration file or identity store.
func (s *Service) Initialize(ctx context.Context, request Request) (Result, error) {
	if err := s.validate(request); err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	state, err := Inspect(s.paths)
	if err != nil {
		return Result{}, err
	}
	switch state {
	case StateReady:
		return Result{}, fmt.Errorf("%w: %q", ErrAlreadyInitialized, s.paths.ConfigDir)
	case StatePartial:
		return Result{}, fmt.Errorf("%w: inspect %q before retrying", ErrPartialState, s.paths.ConfigDir)
	case StateAbsent:
	}

	masterKey, err := s.generateMasterKey()
	if err != nil {
		return Result{}, fmt.Errorf("generate provider credential master key: %w", err)
	}
	contents, err := localConfig(request, masterKey)
	if err != nil {
		return Result{}, err
	}

	parentDir := filepath.Dir(s.paths.ConfigDir)
	if err := os.MkdirAll(parentDir, privateDirMode); err != nil {
		return Result{}, fmt.Errorf("create configuration parent directory: %w", err)
	}
	stagingDir, err := os.MkdirTemp(parentDir, "."+filepath.Base(s.paths.ConfigDir)+"-init-")
	if err != nil {
		return Result{}, fmt.Errorf("create setup staging directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(stagingDir) }()

	stagedPaths := config.PathsForConfigDir(stagingDir)
	if err := os.MkdirAll(stagedPaths.DataDir, privateDirMode); err != nil {
		return Result{}, fmt.Errorf("create staged data directory: %w", err)
	}
	issued, err := s.createIdentity(ctx, stagedPaths.BadgerDir, request.IdentityName)
	if err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := writeExclusive(stagedPaths.ConfigFile, contents); err != nil {
		return Result{}, fmt.Errorf("write staged configuration file: %w", err)
	}
	if err := os.Rename(stagingDir, s.paths.ConfigDir); err != nil {
		current, inspectErr := Inspect(s.paths)
		if inspectErr == nil && current == StateReady {
			return Result{}, fmt.Errorf("%w: %q", ErrAlreadyInitialized, s.paths.ConfigDir)
		}
		if inspectErr == nil && current == StatePartial {
			return Result{}, fmt.Errorf("%w: inspect %q before retrying", ErrPartialState, s.paths.ConfigDir)
		}
		return Result{}, fmt.Errorf("install initialized state: %w", err)
	}

	return Result{
		Provider: request.Provider, IdentityName: request.IdentityName,
		ConfigFile: s.paths.ConfigFile, DataDir: s.paths.DataDir, APIKey: issued.Secret,
	}, nil
}

func (s *Service) validate(request Request) error {
	if s == nil || s.openStore == nil || s.generateMasterKey == nil ||
		s.paths.ConfigDir == "" || s.paths.ConfigFile == "" ||
		s.paths.DataDir == "" || s.paths.BadgerDir == "" {
		return ErrPathsRequired
	}
	expected := config.PathsForConfigDir(s.paths.ConfigDir)
	if !filepath.IsAbs(s.paths.ConfigDir) || s.paths != expected {
		return ErrPathsRequired
	}
	if request.Provider == "" {
		return ErrProviderRequired
	}
	switch request.Provider {
	case catalogs.ProviderIDOpenAI:
		if strings.TrimSpace(request.ProviderCredential) == "" {
			return ErrProviderCredentialRequired
		}
	case catalogs.ProviderIDOllama:
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedProvider, request.Provider)
	}
	if err := (identity.APIKey{ID: "validation", Name: request.IdentityName, Hash: "validation", Scopes: []string{"*"}}).Validate(); err != nil {
		return fmt.Errorf("identity name: %w", err)
	}
	return nil
}

// Inspect reads the managed local setup state without changing it.
func Inspect(paths config.Paths) (State, error) {
	_, err := os.Lstat(paths.ConfigDir)
	if errors.Is(err, fs.ErrNotExist) {
		return StateAbsent, nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect configuration directory: %w", err)
	}
	configExists, err := pathExists(paths.ConfigFile)
	if err != nil {
		return "", fmt.Errorf("inspect configuration file: %w", err)
	}
	storeExists, err := pathExists(paths.BadgerDir)
	if err != nil {
		return "", fmt.Errorf("inspect identity store: %w", err)
	}
	if configExists && storeExists {
		return StateReady, nil
	}
	return StatePartial, nil
}

func (s *Service) createIdentity(
	ctx context.Context,
	path string,
	name string,
) (identity.IssueResult, error) {
	store, err := s.openStore(path)
	if err != nil {
		return identity.IssueResult{}, fmt.Errorf("open staged identity store: %w", err)
	}
	issued, issueErr := InitializeIdentity(ctx, store, name)
	closeErr := store.Close()
	if issueErr != nil {
		return identity.IssueResult{}, issueErr
	}
	if closeErr != nil {
		return identity.IssueResult{}, fmt.Errorf("close staged identity store: %w", closeErr)
	}
	return issued, nil
}

// InitializeIdentity creates the first named identity in configured storage.
// It refuses a repository that already contains an identity.
func InitializeIdentity(
	ctx context.Context,
	store storage.KVStore,
	name string,
) (identity.IssueResult, error) {
	repository, err := identity.Open(store)
	if err != nil {
		return identity.IssueResult{}, fmt.Errorf("open identity repository: %w", err)
	}
	records, err := repository.List(ctx, 1)
	if err != nil {
		return identity.IssueResult{}, fmt.Errorf("inspect identity repository: %w", err)
	}
	if len(records) != 0 {
		return identity.IssueResult{}, ErrAlreadyInitialized
	}
	issuer, err := identity.NewIssuer(repository)
	if err != nil {
		return identity.IssueResult{}, fmt.Errorf("open identity issuer: %w", err)
	}
	issued, err := issuer.IssueInitial(ctx, identity.IssueRequest{
		Name: name, Scopes: []string{"*"}, Metadata: map[string]any{"source": "setup"},
	})
	if err != nil {
		if errors.Is(err, identity.ErrConflict) {
			return identity.IssueResult{}, fmt.Errorf("%w: initial identity was already claimed", ErrAlreadyInitialized)
		}
		return identity.IssueResult{}, fmt.Errorf("create named identity: %w", err)
	}
	return issued, nil
}

func localConfig(request Request, masterKey string) ([]byte, error) {
	values := map[string]string{"STARPORT_SECURITY_MASTER_KEY": masterKey}
	switch request.Provider {
	case catalogs.ProviderIDOpenAI:
		values[OpenAIProviderCredentialEnvironment] = request.ProviderCredential
	case catalogs.ProviderIDOllama:
		values["STARPORT_PROVIDERS_OLLAMA_ENABLED"] = "true"
	default:
		return nil, ErrUnsupportedProvider
	}
	encoded, err := godotenv.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("encode local configuration: %w", err)
	}
	return []byte("# Generated by starport init. Keep this file secret.\n" + encoded + "\n"), nil
}

func openLocalStore(path string) (storage.KVStore, error) {
	return storage.OpenBadger(storage.BadgerConfig{
		Path: path, SyncWrites: true, Compression: true,
		NumVersions: 1, NumLevelZero: 5, MemTableSize: 64 << 20,
	})
}

func generateMasterKey() (string, error) {
	key, err := credentials.GenerateMasterKey()
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(key), nil
}

func pathExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func writeExclusive(path string, contents []byte) (err error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := root.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	name := filepath.Base(path)
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, configFileMode)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
		if err != nil {
			_ = root.Remove(name)
		}
	}()
	if err := file.Chmod(configFileMode); err != nil {
		return err
	}
	if _, err := file.Write(contents); err != nil {
		return err
	}
	return file.Sync()
}
