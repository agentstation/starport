package config

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	catalogconfig "github.com/agentstation/starmap/pkg/catalogs/config"
	starmaperrors "github.com/agentstation/starmap/pkg/errors"
	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/joho/godotenv"
	"github.com/sethvargo/go-envconfig"
)

const (
	// localJournalName is the operation journal next to the configuration
	// file. It shares the writer lock of the configuration directory.
	localJournalName = ".starport-config-operations.json"
	// localJournalLimit bounds the retained operations. The oldest complete
	// operations leave the journal first.
	localJournalLimit = 1000
	// maxConfigurationBytes matches the loader limit.
	maxConfigurationBytes = 1 << 20
	// maxJournalBytes bounds the journal read.
	maxJournalBytes = 4 << 20

	localPending  = "pending"
	localComplete = "complete"
)

// LocalAudit records one local configuration save. Subject names the saved
// revision and the setting keys, never values.
type LocalAudit func(ctx context.Context, actor, action, subject string) error

// LocalWriter saves field edits to the configuration file of a local
// deployment. The file bytes are the revision: the SHA-256 checksum of the
// file is the revision token. An operation journal next to the file makes a
// retry complete from the published checksum. A save applies at the next
// process start.
type LocalWriter struct {
	cfg   *Config
	audit LocalAudit
	now   func() time.Time
	// afterPublish runs after the configuration file publish and before the
	// journal completes. Tests use it to stop a save at that point.
	afterPublish func() error
	mu           sync.Mutex
}

// NewLocalWriter returns the writer for the configuration file that cfg loaded.
// A nil audit function records nothing.
func NewLocalWriter(cfg *Config, audit LocalAudit) *LocalWriter {
	return &LocalWriter{cfg: cfg, audit: audit, now: time.Now}
}

type localJournal struct {
	Operations []localOperation `json:"operations"`
}

// localOperation is one journal entry. Request is the SHA-256 checksum of the
// canonical request, so an exact retry matches without stored values.
type localOperation struct {
	OperationID  string   `json:"operation_id"`
	DeploymentID string   `json:"deployment_id"`
	Request      string   `json:"request"`
	Expected     string   `json:"expected"`
	Target       string   `json:"target"`
	Settings     []string `json:"settings"`
	Status       string   `json:"status"`
	Actor        string   `json:"actor"`
	CreatedAt    string   `json:"created_at"`
	AuditError   string   `json:"audit_error,omitempty"`
}

// localTarget is the configuration file and its directory.
type localTarget struct {
	directory *productfiles.Directory
	name      string
}

// Save publishes the field edits when the file checksum equals the expected
// revision. An exact retry returns the original receipt. A refusal is a
// *Refusal error.
func (w *LocalWriter) Save(ctx context.Context, request FieldSave, actor string) (Receipt, error) {
	if err := w.cfg.CheckOperationRequest(request); err != nil {
		return Receipt{}, err
	}
	edits, err := w.prepare()
	if err != nil {
		return Receipt{}, err
	}
	resolved, err := w.resolve(request.Edits)
	if err != nil {
		return Receipt{}, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	target, err := edits.open()
	if err != nil {
		return Receipt{}, err
	}
	journal, journalBytes, err := target.readJournal()
	if err != nil {
		return Receipt{}, err
	}
	current, err := target.read()
	if err != nil {
		return Receipt{}, err
	}
	currentRevision := fileRevision(current)
	digest := localRequestDigest(request, resolved)
	index := slices.IndexFunc(journal.Operations, func(operation localOperation) bool { return operation.OperationID == request.OperationID })
	if index >= 0 {
		operation := journal.Operations[index]
		if operation.Request != digest || operation.DeploymentID != request.DeploymentID {
			return Receipt{}, OperationConflict()
		}
		switch {
		case operation.Status == localComplete:
			return w.receipt(operation), nil
		case currentRevision == operation.Target:
			return w.complete(ctx, target, journal, journalBytes, index)
		case currentRevision != operation.Expected:
			return Receipt{}, StaleRevision(operation.Expected, currentRevision)
		}
	} else if currentRevision != request.ExpectedRevision {
		return Receipt{}, StaleRevision(request.ExpectedRevision, currentRevision)
	}
	candidate, err := w.candidate(current, resolved)
	if err != nil {
		return Receipt{}, err
	}
	targetRevision := fileRevision(candidate)
	if index >= 0 {
		if journal.Operations[index].Target != targetRevision {
			return Receipt{}, &Refusal{Reason: RefusalIncomplete, Message: "the pending operation does not produce its recorded revision. Use a new operation ID"}
		}
	} else {
		journal.Operations = append(journal.Operations, localOperation{
			OperationID: request.OperationID, DeploymentID: request.DeploymentID, Request: digest,
			Expected: currentRevision, Target: targetRevision, Settings: FieldEditKeys(resolved),
			Status: localPending, Actor: actor, CreatedAt: w.now().UTC().Format(time.RFC3339Nano),
		})
		index = len(journal.Operations) - 1
		if journalBytes, err = target.writeJournal(ctx, journalBytes, journal); err != nil {
			return Receipt{}, err
		}
	}
	if targetRevision != currentRevision {
		if err := target.directory.CompareAndPublish(ctx, target.name, current, candidate); err != nil {
			return Receipt{}, target.publishError(err, currentRevision)
		}
	}
	if w.afterPublish != nil {
		if err := w.afterPublish(); err != nil {
			return Receipt{}, err
		}
	}
	return w.complete(ctx, target, journal, journalBytes, index)
}

// Receipt returns the receipt of a journal operation. A pending operation
// whose revision was not published reports an incomplete refusal.
func (w *LocalWriter) Receipt(_ context.Context, operationID string) (Receipt, bool, error) {
	edits, err := w.prepare()
	if err != nil {
		return Receipt{}, false, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	target, err := edits.open()
	if err != nil {
		return Receipt{}, false, err
	}
	journal, _, err := target.readJournal()
	if err != nil {
		return Receipt{}, false, err
	}
	index := slices.IndexFunc(journal.Operations, func(operation localOperation) bool { return operation.OperationID == operationID })
	if index < 0 {
		return Receipt{}, false, nil
	}
	operation := journal.Operations[index]
	if operation.Status == localComplete {
		return w.receipt(operation), true, nil
	}
	current, err := target.read()
	if err != nil {
		return Receipt{}, false, err
	}
	receipt := w.receipt(operation)
	if fileRevision(current) != operation.Target {
		receipt.Status = OperationRefused
		receipt.Refusal = &Refusal{Reason: RefusalIncomplete, Message: "the operation did not publish its revision. Retry it with the same operation ID and edits"}
	}
	return receipt, true, nil
}

// Validate checks field edits against the current file and returns the
// revision that a save would publish. It writes nothing.
func (w *LocalWriter) Validate(_ context.Context, request FieldSave) (Validation, error) {
	if err := w.cfg.CheckWriteRequest(request.DeploymentID); err != nil {
		return Validation{}, err
	}
	edits, err := w.prepare()
	if err != nil {
		return Validation{}, err
	}
	target, err := edits.open()
	if err != nil {
		return Validation{}, err
	}
	current, err := target.read()
	if err != nil {
		return Validation{}, err
	}
	revision := fileRevision(current)
	validation := Validation{Current: ReceiptRevision{Revision: revision, Checksum: revision}}
	resolved, err := w.resolve(request.Edits)
	if err == nil && request.ExpectedRevision != revision {
		err = StaleRevision(request.ExpectedRevision, revision)
	}
	var candidate []byte
	if err == nil {
		candidate, err = w.candidate(current, resolved)
	}
	var refusal *Refusal
	if errors.As(err, &refusal) {
		validation.Refusal = refusal
		return validation, nil
	}
	if err != nil {
		return Validation{}, err
	}
	preview := fileRevision(candidate)
	validation.Valid, validation.Settings = true, FieldEditKeys(resolved)
	validation.Preview = ReceiptRevision{Revision: preview, Checksum: preview}
	return validation, nil
}

// CandidateCatalog returns the catalog settings that this process would
// select with the field edits applied to its effective values. It reads no
// file or store and writes nothing.
func (c *Config) CandidateCatalog(edits map[string]*string) (CatalogConfig, error) {
	resolved, err := ResolveFieldEdits(edits)
	if err != nil {
		return CatalogConfig{}, err
	}
	values := ApplyFieldEdits(c.Catalog.canonicalValues, resolved)
	layers := []catalogconfig.Layer{{Name: catalogLayerName(0, false), Values: values}}
	return c.checkCandidate(layers)
}

// checkCandidate resolves and decodes candidate catalog layers. A refusal
// message never holds a sensitive value.
func (c *Config) checkCandidate(layers []catalogconfig.Layer) (CatalogConfig, error) {
	resolution, err := catalogconfig.Resolve(layers...)
	if err == nil {
		var next CatalogConfig
		if next, err = c.decodeCatalog(resolution, layers); err == nil {
			return next, nil
		}
	}
	message := "the edited catalog settings are not valid: " + err.Error()
	for _, layer := range layers {
		for name, value := range layer.Values {
			if value != "" && strings.Contains(message, value) && sensitiveSetting(name) {
				message = "the edited catalog settings are not valid"
			}
		}
	}
	return CatalogConfig{}, &Refusal{Reason: RefusalInvalidEdit, Message: message}
}

// sensitiveSetting reports whether a canonical setting holds a credential.
func sensitiveSetting(name string) bool {
	for _, descriptor := range catalogconfig.Descriptors() {
		if descriptor.Name == name {
			return descriptor.Sensitive
		}
	}
	return true
}

// localFile is the selected configuration file before its directory opens.
type localFile struct {
	path string
}

// prepare selects the single configuration file of a local deployment.
func (w *LocalWriter) prepare() (localFile, error) {
	if w.cfg.SharedManagement() {
		return localFile{}, &Refusal{Reason: RefusalUnavailable, Message: "a shared deployment saves configuration in the shared store"}
	}
	path, refusal := w.cfg.localSaveFile()
	if refusal != nil {
		return localFile{}, refusal
	}
	return localFile{path: path}, nil
}

func (f localFile) open() (localTarget, error) {
	directory, err := productfiles.NewDirectory(filepath.Dir(f.path))
	if err != nil {
		return localTarget{}, &Refusal{Reason: RefusalUnavailable, Message: "the configuration directory does not pass the private access check"}
	}
	return localTarget{directory: directory, name: filepath.Base(f.path)}, nil
}

// resolve resolves the edits and refuses a setting that the process
// environment overrides. A saved value under that override would never apply.
func (w *LocalWriter) resolve(edits map[string]*string) ([]FieldEdit, error) {
	resolved, err := ResolveFieldEdits(edits)
	if err != nil {
		return nil, err
	}
	environment := w.cfg.localLayer(catalogLayerName(0, false))
	for _, edit := range resolved {
		if _, present := environment[edit.Name]; present {
			return nil, &Refusal{
				Reason: RefusalInvalidEdit, Setting: edit.Key,
				Message: "the process environment sets " + catalogEnvironmentName(edit.Name) + ". The environment value overrides the configuration file",
			}
		}
	}
	return resolved, nil
}

// candidate returns the edited file bytes after it checks that the catalog
// settings resolve and decode with the process environment.
func (w *LocalWriter) candidate(current []byte, edits []FieldEdit) ([]byte, error) {
	candidate, err := editDotenv(current, edits)
	if err != nil {
		return nil, err
	}
	values, err := godotenv.Unmarshal(string(candidate))
	if err != nil {
		return nil, &Refusal{Reason: RefusalInvalidEdit, Message: "the edited configuration file does not parse"}
	}
	source := catalogClockLookuper{envconfig.MapLookuper(values)}
	product, err := catalogLayer(source, 1, false)
	if err != nil {
		return nil, &Refusal{Reason: RefusalInvalidEdit, Message: err.Error()}
	}
	inherited, err := catalogLayer(source, 1, true)
	if err != nil {
		return nil, &Refusal{Reason: RefusalInvalidEdit, Message: err.Error()}
	}
	layers := []catalogconfig.Layer{
		{Name: catalogLayerName(0, false), Values: w.cfg.localLayer(catalogLayerName(0, false))}, product,
		{Name: catalogLayerName(0, true), Values: w.cfg.localLayer(catalogLayerName(0, true))}, inherited,
	}
	if _, err := w.cfg.checkCandidate(layers); err != nil {
		return nil, err
	}
	return candidate, nil
}

// complete records the audit entry and marks the operation complete. A failed
// audit entry stays in the receipt. The file is never reverted.
func (w *LocalWriter) complete(ctx context.Context, target localTarget, journal localJournal, journalBytes []byte, index int) (Receipt, error) {
	operation := &journal.Operations[index]
	if w.audit != nil {
		subject := AuditSubject("local:"+operation.Target, operation.Settings)
		if err := w.audit(ctx, operation.Actor, ActionSave, subject); err != nil {
			operation.AuditError = "the audit record was not written"
		}
	}
	operation.Status = localComplete
	completed := *operation
	journal.Operations = pruneJournal(journal.Operations)
	if _, err := target.writeJournal(ctx, journalBytes, journal); err != nil {
		return Receipt{}, err
	}
	return w.receipt(completed), nil
}

func (w *LocalWriter) receipt(operation localOperation) Receipt {
	loaded := w.cfg.loadedFileChecksum()
	receipt := Receipt{
		OperationID: operation.OperationID, DeploymentID: operation.DeploymentID,
		Management: ManagementLocal, Status: OperationSaved,
		Saved:      ReceiptRevision{Revision: operation.Target, Checksum: operation.Target},
		Applied:    ReceiptRevision{Revision: loaded, Checksum: loaded},
		AuditError: operation.AuditError, Settings: slices.Clone(operation.Settings),
		Actor: operation.Actor, CreatedAt: operation.CreatedAt,
	}
	if loaded == operation.Target {
		receipt.Status = OperationApplied
	}
	return receipt
}

// pruneJournal drops the oldest complete operations above the limit.
func pruneJournal(operations []localOperation) []localOperation {
	excess := len(operations) - localJournalLimit
	if excess <= 0 {
		return operations
	}
	return slices.DeleteFunc(operations, func(operation localOperation) bool {
		if excess > 0 && operation.Status == localComplete {
			excess--
			return true
		}
		return false
	})
}

// read returns the file bytes. A nil result reports an absent file.
func (t localTarget) read() ([]byte, error) {
	data, err := t.directory.ReadFile(t.name, maxConfigurationBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, &Refusal{Reason: RefusalUnavailable, Message: "the configuration file does not pass the private file read check"}
	}
	return data, nil
}

func (t localTarget) readJournal() (localJournal, []byte, error) {
	var journal localJournal
	data, err := t.directory.ReadFile(localJournalName, maxJournalBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return journal, nil, nil
	}
	if err != nil {
		return journal, nil, &Refusal{Reason: RefusalUnavailable, Message: "the configuration operation journal does not pass the private file read check"}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&journal); err != nil {
		return journal, nil, fmt.Errorf("decode configuration operation journal: %w", err)
	}
	return journal, data, nil
}

func (t localTarget) writeJournal(ctx context.Context, previous []byte, journal localJournal) ([]byte, error) {
	data, err := json.Marshal(journal)
	if err != nil {
		return nil, err
	}
	if err := t.directory.CompareAndPublish(ctx, localJournalName, previous, data); err != nil {
		if starmaperrors.IsConflict(err) {
			return nil, &Refusal{Reason: RefusalBusy, Message: "another writer changed the configuration operation journal. Retry the operation"}
		}
		return nil, err
	}
	return data, nil
}

// publishError maps a refused publish to a stale revision when the file
// changed, and to a busy writer otherwise.
func (t localTarget) publishError(err error, expected string) error {
	if !starmaperrors.IsConflict(err) {
		return err
	}
	if current, readErr := t.read(); readErr == nil && fileRevision(current) != expected {
		return StaleRevision(expected, fileRevision(current))
	}
	return &Refusal{Reason: RefusalBusy, Message: "another writer holds the configuration directory. Retry the operation"}
}

// fileRevision is the local revision token. An absent file and an empty file
// share the checksum of no bytes.
func fileRevision(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// loadedFileChecksum is the revision of the single configuration file that
// this process loaded. It is empty when several files were selected.
func (c *Config) loadedFileChecksum() string {
	if c == nil || len(c.fileInputs) != 1 {
		return ""
	}
	if !c.fileInputs[0].loaded {
		return fileRevision(nil)
	}
	return hex.EncodeToString(c.fileInputs[0].digest[:])
}

// localLayer returns a copy of one loaded catalog layer.
func (c *Config) localLayer(name string) map[string]string {
	if c.localCatalog != nil {
		for _, layer := range c.localCatalog.layers {
			if layer.Name == name {
				return maps.Clone(layer.Values)
			}
		}
	}
	return map[string]string{}
}

// localRequestDigest identifies the deployment, the expected revision, and
// the canonical edits of one save.
func localRequestDigest(request FieldSave, edits []FieldEdit) string {
	type edit struct {
		Name  string  `json:"name"`
		Value *string `json:"value"`
	}
	canonical := struct {
		DeploymentID string `json:"deployment_id"`
		Expected     string `json:"expected_revision"`
		Edits        []edit `json:"edits"`
	}{DeploymentID: request.DeploymentID, Expected: request.ExpectedRevision}
	for _, resolved := range edits {
		canonical.Edits = append(canonical.Edits, edit{Name: resolved.Name, Value: resolved.Value})
	}
	data, _ := json.Marshal(canonical)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
