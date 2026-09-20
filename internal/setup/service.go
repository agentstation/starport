// Package setup owns safe first-run initialization for a local Starport instance.
package setup

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/joho/godotenv"

	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/authorization/revision"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/storage"
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
	APIKeyName string
	ConfigFile string
	DataDir    string
	APIKey     string
	apiKeyID   string
	recovery   *setupJournal
}

// Service initializes one local configuration and API key store.
type Service struct {
	paths             config.Paths
	openStore         func(string) (storage.KVStore, error)
	generateMasterKey func() (string, error)
	checkpoint        func(string) error
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
func (s *Service) Initialize(ctx context.Context, request Request) (_ Result, resultErr error) {
	if err := s.validate(request); err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := s.paths.CheckLegacyPaths(ctx); err != nil {
		return Result{}, err
	}
	guard, err := lockDatabase(ctx, s.paths)
	if err != nil {
		return Result{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, guard.close()) }()
	if err := guard.bindSetup(ctx, s.paths); err != nil {
		return Result{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, guard.finishSetup(s.paths)) }()
	if err := s.recoverAcrossRoots(ctx); err != nil {
		return Result{}, err
	}
	state, err := Inspect(s.paths)
	if err != nil {
		return Result{}, err
	}
	switch state {
	case StateReady:
		return Result{}, fmt.Errorf("%w: %q", ErrAlreadyInitialized, s.paths.ConfigFile)
	case StatePartial:
		return Result{}, fmt.Errorf("%w: inspect %q before retrying", ErrPartialState, s.paths.ConfigFile)
	case StateAbsent:
	}
	prepared, err := s.prepareAcrossRoots(ctx, request)
	if err != nil {
		return Result{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, prepared.writer.close()) }()
	return prepared.publish(ctx)
}

// Rollback removes unchanged setup state if the command cannot return its result.
func (s *Service) Rollback(ctx context.Context, result Result) (resultErr error) {
	if s == nil || validateSetupPaths(s.paths) != nil {
		return ErrRollbackRefused
	}
	guard, err := lockDatabase(ctx, s.paths)
	if err != nil {
		return errors.Join(ErrRollbackRefused, err)
	}
	defer func() { resultErr = errors.Join(resultErr, guard.close()) }()
	if err := guard.bindSetup(ctx, s.paths); err != nil {
		return errors.Join(ErrRollbackRefused, err)
	}
	defer func() { resultErr = errors.Join(resultErr, guard.finishSetup(s.paths)) }()
	return s.rollbackAcrossRoots(ctx, result)
}

func (s *Service) validateRollbackRecords(ctx context.Context, directory, apiKeyID string) error {
	store, err := s.openStore(directory)
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
	stamp, revisionErr := revision.NewKV(store, nil).Read(ctx)
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
	if len(records) != 1 || records[0].APIKey.ID != apiKeyID {
		return fmt.Errorf("%w: API key storage changed after initialization", ErrRollbackRefused)
	}
	if revisionErr != nil || stamp.Sequence != 1 {
		return errors.Join(ErrRollbackRefused, fmt.Errorf("authorization state changed after initialization"), revisionErr)
	}
	if len(keys) != 5 {
		return fmt.Errorf("%w: storage contains %d records, want 5", ErrRollbackRefused, len(keys))
	}
	for _, key := range keys {
		if key != revision.StorageKey && !strings.HasPrefix(key, apikey.StoragePrefix) {
			return fmt.Errorf("%w: storage contains application state", ErrRollbackRefused)
		}
	}
	return nil
}

func (s *Service) validate(request Request) error {
	if s == nil || s.openStore == nil || s.generateMasterKey == nil ||
		s.paths.ConfigDir == "" || s.paths.ConfigFile == "" ||
		s.paths.DataDir == "" || s.paths.BadgerDir == "" {
		return ErrPathsRequired
	}
	if err := validateSetupPaths(s.paths); err != nil {
		return err
	}
	if err := (apikey.APIKey{ID: "validation", Name: request.APIKeyName, Hash: "validation", Scopes: []string{"*"}}).Validate(); err != nil {
		return fmt.Errorf("API key name: %w", err)
	}
	return nil
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
	// The initial key names no account. Reads resolve it to the canonical account.
	// Setup writes only API key records. The gateway creates the canonical account
	// at boot. This path accepts no caller-supplied account and needs no account checker.
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
