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

	"github.com/joho/godotenv"

	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/storage"
)

const (
	configFileMode = 0o600
	privateDirMode = 0o700
)

var (
	// ErrPathsRequired reports incomplete managed paths.
	ErrPathsRequired = errors.New("setup paths are required")
	// ErrAlreadyInitialized reports existing configuration or API key storage.
	ErrAlreadyInitialized = errors.New("starport is already initialized")
	// ErrPartialState reports an incomplete managed configuration directory.
	ErrPartialState = errors.New("starport setup state is incomplete")
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
	// StateReady means the configuration file and API key store both exist.
	StateReady State = "ready"
)

// Request contains the explicit choices for local initialization.
type Request struct {
	APIKeyName string
}

// Result contains initialized paths and the one-time gateway credential.
type Result struct {
	APIKeyName   string
	ConfigFile   string
	DataDir      string
	APIKey       string
	apiKeyID     string
	configDigest [sha256.Size]byte
}

// Service initializes one local configuration and API key store.
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

// Initialize creates local configuration and one named gateway apikey.
// It never replaces an existing configuration file or API key store.
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
		return Result{}, fmt.Errorf("generate security master key: %w", err)
	}
	contents, err := localConfig(masterKey)
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
	issued, err := s.createAPIKey(ctx, stagedPaths.BadgerDir, request.APIKeyName)
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
		APIKeyName: request.APIKeyName,
		ConfigFile: s.paths.ConfigFile, DataDir: s.paths.DataDir, APIKey: issued.Secret,
		apiKeyID: issued.APIKey.ID, configDigest: sha256.Sum256(contents),
	}
	if err := syncDirectory(parentDir); err != nil {
		return result, fmt.Errorf("sync installed setup directory: %w", err)
	}
	return result, nil
}

// Rollback removes local state if the command cannot return the initialization result.
func (s *Service) Rollback(ctx context.Context, result Result) error {
	if s == nil || result.ConfigFile != s.paths.ConfigFile || result.DataDir != s.paths.DataDir ||
		result.apiKeyID == "" {
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
		return fmt.Errorf("open isolated API key store for rollback: %w", err)
	}
	repository, err := apikey.Open(store)
	if err != nil {
		_ = store.Close()
		return fmt.Errorf("open initialized API key repository for rollback: %w", err)
	}
	keys, scanErr := store.ScanWithPrefix(ctx, "", 0)
	records, listErr := repository.List(ctx, 2, 0)
	closeErr := store.Close()
	if scanErr != nil {
		return errors.Join(fmt.Errorf("inspect isolated storage keys for rollback: %w", scanErr), closeErr)
	}
	if listErr != nil {
		return errors.Join(fmt.Errorf("inspect the initialized API key for rollback: %w", listErr), closeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close initialized API key store for rollback: %w", closeErr)
	}
	if len(records) != 1 || records[0].APIKey.ID != result.apiKeyID {
		return fmt.Errorf("%w: API key storage changed after initialization", ErrRollbackRefused)
	}
	if len(keys) != 4 {
		return fmt.Errorf("%w: storage contains %d records, want 4", ErrRollbackRefused, len(keys))
	}
	for _, key := range keys {
		if !strings.HasPrefix(key, apikey.StoragePrefix) {
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
	if err := (apikey.APIKey{ID: "validation", Name: request.APIKeyName, Hash: "validation", Scopes: []string{"*"}}).Validate(); err != nil {
		return fmt.Errorf("API key name: %w", err)
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
		return "", fmt.Errorf("inspect API key store: %w", err)
	}
	if configExists && storeExists {
		return StateReady, nil
	}
	return StatePartial, nil
}

func (s *Service) createAPIKey(
	ctx context.Context,
	path string,
	name string,
) (apikey.IssueResult, error) {
	store, err := s.openStore(path)
	if err != nil {
		return apikey.IssueResult{}, fmt.Errorf("open staged API key store: %w", err)
	}
	issued, issueErr := InitializeAPIKey(ctx, store, name)
	closeErr := store.Close()
	if issueErr != nil {
		return apikey.IssueResult{}, issueErr
	}
	if closeErr != nil {
		return apikey.IssueResult{}, fmt.Errorf("close staged API key store: %w", closeErr)
	}
	return issued, nil
}

// InitializeAPIKey creates the first named API key in configured storage.
// It refuses a repository that already contains an apikey.
func InitializeAPIKey(
	ctx context.Context,
	store storage.KVStore,
	name string,
) (apikey.IssueResult, error) {
	repository, err := apikey.Open(store)
	if err != nil {
		return apikey.IssueResult{}, fmt.Errorf("open API key repository: %w", err)
	}
	records, err := repository.List(ctx, 1, 0)
	if err != nil {
		return apikey.IssueResult{}, fmt.Errorf("inspect API key repository: %w", err)
	}
	if len(records) != 0 {
		return apikey.IssueResult{}, ErrAlreadyInitialized
	}
	// The initial key names no account, so it resolves to the canonical account at
	// read time. Setup deliberately writes nothing but API key records, and the
	// gateway ensures the canonical account at boot, so this path needs no account
	// checker: it never accepts a caller-supplied account to check.
	issuer, err := apikey.NewIssuer(repository)
	if err != nil {
		return apikey.IssueResult{}, fmt.Errorf("open API key issuer: %w", err)
	}
	issued, err := issuer.IssueInitial(ctx, apikey.IssueRequest{
		Name: name, Scopes: []string{"*"}, Metadata: map[string]any{"source": "setup"},
	})
	if err != nil {
		if errors.Is(err, apikey.ErrConflict) {
			return apikey.IssueResult{}, fmt.Errorf("%w: initial API key was already claimed", ErrAlreadyInitialized)
		}
		return issued, fmt.Errorf("create named API key: %w", err)
	}
	return issued, nil
}

// ReleaseAPIKey removes an unpublished initial API key and its setup claim.
func ReleaseAPIKey(ctx context.Context, store storage.KVStore, id string) error {
	repository, err := apikey.Open(store)
	if err != nil {
		return fmt.Errorf("open API key repository: %w", err)
	}
	if err := repository.ReleaseInitial(ctx, id); err != nil {
		return fmt.Errorf("release initial API key: %w", err)
	}
	return nil
}

func localConfig(masterKey string) ([]byte, error) {
	values := map[string]string{"STARPORT_SECURITY_MASTER_KEY": masterKey}
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
