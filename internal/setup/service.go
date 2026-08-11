// Package setup owns safe first-run initialization for a local Starport instance.
package setup

import (
	"context"
	"crypto/sha256"
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
	// #nosec G101 -- This constant is an environment name, not credential material.
	OpenAIProviderCredentialEnvironment = "OPENAI_API_KEY"
	ollamaBaseURLEnvironment            = "OLLAMA_BASE_URL"
	ollamaDefaultBaseURL                = "http://localhost:11434"
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
	// ErrRollbackRefused reports state that does not match an initialization result.
	ErrRollbackRefused = errors.New("setup rollback refused")
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
	identityID   string
	configDigest [sha256.Size]byte
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
	for _, directory := range []string{stagedPaths.BadgerDir, stagedPaths.DataDir, stagedPaths.ConfigDir} {
		if err := syncDirectory(directory); err != nil {
			return Result{}, fmt.Errorf("sync staged setup directory %q: %w", directory, err)
		}
	}
	if err := renameNoReplace(stagingDir, s.paths.ConfigDir); err != nil {
		current, inspectErr := Inspect(s.paths)
		if inspectErr == nil && current == StateReady {
			return Result{}, fmt.Errorf("%w: %q", ErrAlreadyInitialized, s.paths.ConfigDir)
		}
		if inspectErr == nil && current == StatePartial {
			return Result{}, fmt.Errorf("%w: inspect %q before retrying", ErrPartialState, s.paths.ConfigDir)
		}
		return Result{}, fmt.Errorf("install initialized state: %w", err)
	}

	result := Result{
		Provider: request.Provider, IdentityName: request.IdentityName,
		ConfigFile: s.paths.ConfigFile, DataDir: s.paths.DataDir, APIKey: issued.Secret,
		identityID: issued.APIKey.ID, configDigest: sha256.Sum256(contents),
	}
	if err := syncDirectory(parentDir); err != nil {
		return result, fmt.Errorf("sync installed setup directory: %w", err)
	}
	return result, nil
}

// Rollback removes local state when the initialization result could not be returned.
func (s *Service) Rollback(ctx context.Context, result Result) error {
	if s == nil || result.ConfigFile != s.paths.ConfigFile || result.DataDir != s.paths.DataDir ||
		result.identityID == "" {
		return ErrRollbackRefused
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	state, err := Inspect(s.paths)
	if err != nil {
		return err
	}
	if state != StateReady {
		return fmt.Errorf("%w: setup state is %s", ErrRollbackRefused, state)
	}
	parentDir := filepath.Dir(s.paths.ConfigDir)
	rollbackDir, err := os.MkdirTemp(parentDir, "."+filepath.Base(s.paths.ConfigDir)+"-rollback-")
	if err != nil {
		return fmt.Errorf("reserve setup rollback path: %w", err)
	}
	if err := os.Remove(rollbackDir); err != nil {
		return fmt.Errorf("prepare setup rollback path: %w", err)
	}
	if err := renameNoReplace(s.paths.ConfigDir, rollbackDir); err != nil {
		return fmt.Errorf("isolate initialized state for rollback: %w", err)
	}
	rollbackPaths := config.PathsForConfigDir(rollbackDir)
	if err := s.validateRollbackState(ctx, rollbackPaths, result); err != nil {
		return s.restoreRollback(rollbackDir, err)
	}
	if err := os.RemoveAll(rollbackDir); err != nil {
		return fmt.Errorf("remove rolled-back setup state: %w", err)
	}
	if err := syncDirectory(parentDir); err != nil {
		return fmt.Errorf("sync rolled-back setup directory: %w", err)
	}
	return nil
}

func (s *Service) validateRollbackState(ctx context.Context, paths config.Paths, result Result) error {
	contents, err := os.ReadFile(paths.ConfigFile)
	if err != nil {
		return fmt.Errorf("read isolated configuration for rollback: %w", err)
	}
	if sha256.Sum256(contents) != result.configDigest {
		return fmt.Errorf("%w: configuration changed after initialization", ErrRollbackRefused)
	}
	state, err := Inspect(paths)
	if err != nil {
		return err
	}
	if state != StateReady {
		return fmt.Errorf("%w: isolated setup state is %s", ErrRollbackRefused, state)
	}
	if err := validateRollbackLayout(paths); err != nil {
		return err
	}
	store, err := s.openStore(paths.BadgerDir)
	if err != nil {
		return fmt.Errorf("open isolated identity store for rollback: %w", err)
	}
	repository, err := identity.Open(store)
	if err != nil {
		_ = store.Close()
		return fmt.Errorf("open initialized identity repository for rollback: %w", err)
	}
	keys, scanErr := store.ScanWithPrefix(ctx, "", 0)
	records, listErr := repository.List(ctx, 2)
	closeErr := store.Close()
	if scanErr != nil {
		return errors.Join(fmt.Errorf("inspect isolated storage keys for rollback: %w", scanErr), closeErr)
	}
	if listErr != nil {
		return errors.Join(fmt.Errorf("inspect initialized identity for rollback: %w", listErr), closeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close initialized identity store for rollback: %w", closeErr)
	}
	if len(records) != 1 || records[0].APIKey.ID != result.identityID {
		return fmt.Errorf("%w: identity storage changed after initialization", ErrRollbackRefused)
	}
	if len(keys) != 4 {
		return fmt.Errorf("%w: storage contains %d records, want 4", ErrRollbackRefused, len(keys))
	}
	for _, key := range keys {
		if !strings.HasPrefix(key, identity.StoragePrefix) {
			return fmt.Errorf("%w: storage contains application state", ErrRollbackRefused)
		}
	}
	return nil
}

func validateRollbackLayout(paths config.Paths) error {
	entries, err := os.ReadDir(paths.ConfigDir)
	if err != nil {
		return fmt.Errorf("inspect isolated configuration directory for rollback: %w", err)
	}
	if len(entries) != 2 || !hasDirectoryEntry(entries, filepath.Base(paths.ConfigFile), false) ||
		!hasDirectoryEntry(entries, filepath.Base(paths.DataDir), true) {
		return fmt.Errorf("%w: configuration directory contains application state", ErrRollbackRefused)
	}
	dataEntries, err := os.ReadDir(paths.DataDir)
	if err != nil {
		return fmt.Errorf("inspect isolated data directory for rollback: %w", err)
	}
	if len(dataEntries) != 1 || !hasDirectoryEntry(dataEntries, filepath.Base(paths.BadgerDir), true) {
		return fmt.Errorf("%w: data directory contains application state", ErrRollbackRefused)
	}
	return nil
}

func hasDirectoryEntry(entries []fs.DirEntry, name string, directory bool) bool {
	for _, entry := range entries {
		if entry.Name() == name && entry.IsDir() == directory {
			return true
		}
	}
	return false
}

func (s *Service) restoreRollback(rollbackDir string, cause error) error {
	if err := renameNoReplace(rollbackDir, s.paths.ConfigDir); err != nil {
		return errors.Join(
			cause,
			fmt.Errorf("restore refused setup state from %q: %w", rollbackDir, err),
		)
	}
	if err := syncDirectory(filepath.Dir(s.paths.ConfigDir)); err != nil {
		return errors.Join(cause, fmt.Errorf("sync restored setup state: %w", err))
	}
	return cause
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
		return issued, fmt.Errorf("create named identity: %w", err)
	}
	return issued, nil
}

// ReleaseIdentity removes an unpublished initial identity and its setup claim.
func ReleaseIdentity(ctx context.Context, store storage.KVStore, id string) error {
	repository, err := identity.Open(store)
	if err != nil {
		return fmt.Errorf("open identity repository: %w", err)
	}
	if err := repository.ReleaseInitial(ctx, id); err != nil {
		return fmt.Errorf("release initial identity: %w", err)
	}
	return nil
}

func localConfig(request Request, masterKey string) ([]byte, error) {
	values := map[string]string{"STARPORT_SECURITY_MASTER_KEY": masterKey}
	switch request.Provider {
	case catalogs.ProviderIDOpenAI:
		values[OpenAIProviderCredentialEnvironment] = request.ProviderCredential
	case catalogs.ProviderIDOllama:
		values[ollamaBaseURLEnvironment] = ollamaDefaultBaseURL
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
