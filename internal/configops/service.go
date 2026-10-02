// Package configops owns the configuration field operations of one gateway
// process: the schema, the effective report, validation, a source connection
// test, field saves, and operation receipts. A local deployment saves to its
// configuration file. A shared deployment saves to the shared revision store.
package configops

import (
	"context"
	"errors"
	"maps"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/configrevision"
	"github.com/agentstation/starport/internal/sqlstore"
)

// operationTimeout bounds the store phase of one operation.
const operationTimeout = 30 * time.Second

// Probe checks that the catalog source of the settings answers. Its error
// must not hold a credential.
type Probe func(ctx context.Context, catalog config.CatalogConfig) error

// Options supplies the adapters of a Service.
type Options struct {
	// Store is the shared revision store. Shared management requires it.
	Store *configrevision.Store
	// LocalAudit records a local save.
	LocalAudit config.LocalAudit
	// Observe records the newest stored head after a shared save. The
	// receipt then reports the desired revision of this process.
	Observe func(context.Context) error
	// Probe tests the catalog source. A nil probe refuses the test.
	Probe Probe
}

// Service runs configuration operations against the authority that the
// management mode selects. Schema and Effective read process memory only.
type Service struct {
	cfg     *config.Config
	local   *config.LocalWriter
	store   *configrevision.Store
	observe func(context.Context) error
	probe   Probe
	// mu serializes the shared saves of this process. The store head and the
	// unique operation ID serialize saves across processes.
	mu sync.Mutex
}

// New returns the configuration operations of cfg.
func New(cfg *config.Config, options Options) (*Service, error) {
	service := &Service{cfg: cfg, store: options.Store, observe: options.Observe, probe: options.Probe}
	if cfg.SharedManagement() {
		if options.Store == nil {
			return nil, errors.New("shared configuration operations require the shared revision store")
		}
		return service, nil
	}
	service.local = config.NewLocalWriter(cfg, options.LocalAudit)
	return service, nil
}

// Schema returns the configuration setting descriptors.
func (s *Service) Schema() []config.SchemaSetting {
	return config.ConfigurationSchema()
}

// Effective returns the redacted effective configuration from memory.
func (s *Service) Effective() config.EffectiveReport {
	return s.cfg.EffectiveReport()
}

// Validate checks a field save and previews the revision that it would
// write. It writes nothing. An edit refusal is in the result.
func (s *Service) Validate(ctx context.Context, request config.FieldSave) (config.Validation, error) {
	if s.local != nil {
		return s.local.Validate(ctx, request)
	}
	if err := s.cfg.CheckWriteRequest(request.DeploymentID); err != nil {
		return config.Validation{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	edits, expected, err := sharedRequest(request)
	var candidate sharedCandidate
	if err == nil {
		if err = s.checkSchema(ctx); err == nil {
			candidate, err = s.candidate(ctx, edits, expected)
		}
	}
	validation := config.Validation{Current: candidate.currentRevision()}
	var refusal *config.Refusal
	if errors.As(err, &refusal) && editRefusal(refusal.Reason) {
		validation.Refusal = refusal
		return validation, nil
	}
	if err != nil {
		return config.Validation{}, err
	}
	validation.Valid = true
	validation.Settings = config.ChangedSettingKeys(candidate.current.Values, candidate.values)
	validation.Preview = sharedReceiptRevision(candidate.current.Sequence+1, candidate.checksum)
	return validation, nil
}

// TestConnection probes the catalog source of the effective settings, or of
// the settings with the request edits applied. It writes nothing, and the
// result never holds a credential or a source URL.
func (s *Service) TestConnection(ctx context.Context, request config.FieldSave) (config.ConnectionResult, error) {
	if err := s.cfg.CheckWriteRequest(request.DeploymentID); err != nil {
		return config.ConnectionResult{}, err
	}
	catalog := s.cfg.Catalog
	if len(request.Edits) > 0 {
		candidate, err := s.cfg.CandidateCatalog(request.Edits)
		if err != nil {
			return config.ConnectionResult{}, err
		}
		catalog = candidate
	}
	if s.probe == nil {
		return config.ConnectionResult{}, &config.Refusal{Reason: config.RefusalUnavailable, Message: "this gateway has no catalog source probe"}
	}
	result := config.ConnectionResult{Source: catalog.Source, Reachable: true}
	if err := s.probe(ctx, catalog); err != nil {
		result.Reachable = false
		result.Error = scrub(err.Error(), "the catalog source test failed", catalog.SourceAPIKey, catalog.SourceToken, catalog.SourceURL)
	}
	return result, nil
}

// Save writes a field save. A local save publishes the configuration file. A
// shared save commits the next revision when the head is at the expected
// sequence. An exact retry returns the original receipt. A refusal is a
// *config.Refusal error.
func (s *Service) Save(ctx context.Context, request config.FieldSave, actor string) (config.Receipt, error) {
	if err := s.cfg.CheckOperationRequest(request); err != nil {
		return config.Receipt{}, err
	}
	if s.local != nil {
		return s.local.Save(ctx, request, actor)
	}
	edits, expected, err := sharedRequest(request)
	if err != nil {
		return config.Receipt{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkSchema(ctx); err != nil {
		return config.Receipt{}, err
	}
	if receipt, found, err := s.retry(ctx, request.OperationID, edits, expected); err != nil || found {
		return receipt, err
	}
	candidate, err := s.candidate(ctx, edits, expected)
	if err != nil {
		return config.Receipt{}, err
	}
	settings := config.ChangedSettingKeys(candidate.current.Values, candidate.values)
	revision, err := s.store.SaveFields(ctx, expected, candidate.values, settings, actor, request.OperationID)
	if err != nil {
		return config.Receipt{}, storeError(err)
	}
	// The store replays an operation ID that committed the same policy at the
	// same sequence. Only equal values make that replay this save.
	stored, found, err := s.store.OperationRevision(ctx, request.OperationID)
	if err != nil {
		return config.Receipt{}, storeError(err)
	}
	if !found || stored.Sequence != revision.Sequence || !maps.Equal(stored.Values, candidate.values) {
		return config.Receipt{}, config.OperationConflict()
	}
	if s.observe != nil {
		// A failed observation keeps the applied revision as a retained cache.
		_ = s.observe(ctx)
	}
	return s.sharedReceipt(stored, candidate.current.Values), nil
}

// Receipt returns the receipt of an operation ID. It reports false when the
// operation ID saved nothing.
func (s *Service) Receipt(ctx context.Context, operationID string) (config.Receipt, bool, error) {
	if s.local != nil {
		return s.local.Receipt(ctx, operationID)
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	revision, found, err := s.store.OperationRevision(ctx, operationID)
	var unseal *configrevision.UnsealError
	if errors.As(err, &unseal) {
		// The revision holds a credential that this process cannot open, so
		// this process cannot activate it.
		revision, found, err = s.store.Operation(ctx, operationID)
		if err != nil || !found {
			return config.Receipt{}, found, storeError(err)
		}
		receipt := s.sharedReceipt(revision, nil)
		receipt.Settings = nil
		if receipt.Status != config.OperationApplied {
			receipt.ActivationError = "the revision holds a sealed " + safeSetting(unseal.Setting) + " that the master key of this process cannot open"
		}
		return receipt, true, nil
	}
	if err != nil || !found {
		return config.Receipt{}, found, storeError(err)
	}
	var previous map[string]string
	if revision.Sequence > 1 {
		predecessor, found, err := s.store.RevisionAt(ctx, revision.Sequence-1)
		if err == nil && found {
			previous = predecessor.Values
		}
	}
	return s.sharedReceipt(revision, previous), true, nil
}

// sharedCandidate is the next shared revision of a field save.
type sharedCandidate struct {
	current  configrevision.Revision
	values   map[string]string
	checksum string
}

func (c sharedCandidate) currentRevision() config.ReceiptRevision {
	if c.current.Sequence == 0 {
		return config.ReceiptRevision{}
	}
	return sharedReceiptRevision(c.current.Sequence, c.current.Checksum)
}

// sharedRequest resolves the edits and the expected head sequence.
func sharedRequest(request config.FieldSave) ([]config.FieldEdit, int64, error) {
	edits, err := config.ResolveFieldEdits(request.Edits)
	if err != nil {
		return nil, 0, err
	}
	expected, err := strconv.ParseInt(request.ExpectedRevision, 10, 64)
	if err != nil || expected <= 0 || strconv.FormatInt(expected, 10) != request.ExpectedRevision {
		return nil, 0, &config.Refusal{Reason: config.RefusalInvalidEdit, Message: "expected_revision of a shared save is the decimal head sequence"}
	}
	return edits, expected, nil
}

// candidate applies the edits to the head at the expected sequence. A field
// save keeps the policy identity: it can change source credentials only.
func (s *Service) candidate(ctx context.Context, edits []config.FieldEdit, expected int64) (sharedCandidate, error) {
	current, err := s.store.Current(ctx)
	if err != nil {
		return sharedCandidate{}, storeError(err)
	}
	candidate := sharedCandidate{current: current}
	if current.Authority != configrevision.AuthorityShared {
		return candidate, &config.Refusal{Reason: config.RefusalUnavailable, Message: "the shared configuration head released the shared authority. Run starport config migrate --to shared"}
	}
	if current.Sequence != expected {
		return candidate, config.StaleRevision(strconv.FormatInt(expected, 10), strconv.FormatInt(current.Sequence, 10))
	}
	candidate.values = config.ApplyFieldEdits(current.Values, edits)
	if err := config.ValidateSharedValues(candidate.values); err != nil {
		return candidate, s.invalidEdit(err, candidate.values)
	}
	candidate.checksum = config.SharedValuesChecksum(candidate.values)
	if candidate.checksum != current.Checksum {
		return candidate, &config.Refusal{
			Reason:  config.RefusalPolicyChange,
			Message: "a shared field save changes source credentials only. Change the acquisition policy with starport config apply",
		}
	}
	next := current.Shared()
	next.Sequence, next.Values, next.Checksum = current.Sequence+1, candidate.values, candidate.checksum
	if err := s.cfg.CheckSharedRevision(next); err != nil {
		return candidate, s.invalidEdit(err, candidate.values)
	}
	return candidate, nil
}

// retry returns the original receipt of an exact retry. The checksum omits
// credentials, so the values of the stored revision decide: they must equal
// the edits applied to the revision before it.
func (s *Service) retry(ctx context.Context, operationID string, edits []config.FieldEdit, expected int64) (config.Receipt, bool, error) {
	prior, found, err := s.store.OperationRevision(ctx, operationID)
	if err != nil || !found {
		return config.Receipt{}, false, storeError(err)
	}
	if prior.Authority != configrevision.AuthorityShared || prior.Sequence != expected+1 {
		return config.Receipt{}, false, config.OperationConflict()
	}
	predecessor, found, err := s.store.RevisionAt(ctx, expected)
	if err != nil {
		return config.Receipt{}, false, storeError(err)
	}
	if !found || !maps.Equal(prior.Values, config.ApplyFieldEdits(predecessor.Values, edits)) {
		return config.Receipt{}, false, config.OperationConflict()
	}
	return s.sharedReceipt(prior, predecessor.Values), true, nil
}

// sharedReceipt reports a stored revision. Applied is the revision that this
// process serves. A saved revision that this process could not apply at its
// next start reports the activation error.
func (s *Service) sharedReceipt(revision configrevision.Revision, previous map[string]string) config.Receipt {
	applied := s.cfg.AppliedRevision()
	receipt := config.Receipt{
		OperationID: revision.OperationID, DeploymentID: revision.DeploymentID, Management: config.ManagementShared,
		Status:  config.OperationSaved,
		Saved:   sharedReceiptRevision(revision.Sequence, revision.Checksum),
		Applied: sharedReceiptRevision(applied.Applied, applied.Checksum),
		Actor:   revision.Actor, CreatedAt: revision.CreatedAt,
	}
	if previous != nil {
		receipt.Settings = config.ChangedSettingKeys(previous, revision.Values)
	}
	if applied.Applied == revision.Sequence {
		receipt.Status = config.OperationApplied
		return receipt
	}
	if revision.Values != nil {
		if err := s.cfg.CheckSharedRevision(revision.Shared()); err != nil {
			receipt.ActivationError = scrub(err.Error(), "this process cannot apply the saved revision", sensitiveValues(revision.Values)...)
		}
	}
	return receipt
}

func sharedReceiptRevision(sequence int64, checksum string) config.ReceiptRevision {
	if sequence <= 0 {
		return config.ReceiptRevision{}
	}
	return config.ReceiptRevision{Revision: strconv.FormatInt(sequence, 10), Sequence: sequence, Checksum: checksum}
}

// checkSchema refuses a store behind this binary. A field save never migrates.
func (s *Service) checkSchema(ctx context.Context) error {
	if err := s.store.CheckSchema(ctx); err != nil {
		return storeError(err)
	}
	return nil
}

// invalidEdit returns the refusal of a candidate that fails validation. The
// message never holds a credential.
func (s *Service) invalidEdit(err error, values map[string]string) error {
	refusal := &config.Refusal{Reason: config.RefusalInvalidEdit}
	var setting *config.SharedSettingError
	if errors.As(err, &setting) {
		refusal.Setting = settingKey(setting.Name)
	}
	refusal.Message = scrub(err.Error(), "the edited catalog settings are not valid", sensitiveValues(values)...)
	return refusal
}

// editRefusal reports a refusal that a validation result carries. Other
// refusals are failures of the operation.
func editRefusal(reason string) bool {
	switch reason {
	case config.RefusalInvalidEdit, config.RefusalMigrationBoundary, config.RefusalPolicyChange, config.RefusalStaleRevision:
		return true
	}
	return false
}

// storeError maps a store failure to a refusal. Store messages can name
// connection details, so a refusal never repeats them.
func storeError(err error) error {
	var refusal *config.Refusal
	var stale *configrevision.StaleRevisionError
	var unseal *configrevision.UnsealError
	var unavailable *configrevision.UnavailableError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &refusal):
		return refusal
	case errors.As(err, &stale):
		return config.StaleRevision(strconv.FormatInt(stale.Expected, 10), strconv.FormatInt(stale.Current, 10))
	case errors.Is(err, configrevision.ErrOperationConflict):
		return config.OperationConflict()
	case errors.Is(err, sqlstore.ErrSchemaBehind):
		return &config.Refusal{Reason: config.RefusalSchemaBehind, Message: "the relational schema is behind this binary. Start a gateway process of this version to migrate it, then save again"}
	case errors.Is(err, sqlstore.ErrImportRestricted):
		return &config.Refusal{Reason: config.RefusalUnavailable, Message: "a relational recovery holds the store. Complete the recovery, then save again"}
	case errors.Is(err, configrevision.ErrNotInitialized):
		return &config.Refusal{Reason: config.RefusalUnavailable, Message: "the shared configuration is not initialized. Run starport config init --shared"}
	case errors.As(err, &unseal):
		return &config.Refusal{Reason: config.RefusalUnavailable, Message: "the shared configuration holds a sealed " + safeSetting(unseal.Setting) + " that the master key of this process cannot open"}
	case errors.As(err, &unavailable):
		return &config.Refusal{Reason: config.RefusalUnavailable, Message: "the shared configuration store is unavailable"}
	default:
		return err
	}
}

// settingKey returns the field-save key of a canonical setting name.
func settingKey(name string) string {
	keys := config.ChangedSettingKeys(nil, map[string]string{name: ""})
	if len(keys) == 1 {
		return keys[0]
	}
	return ""
}

// safeSetting names a sealed setting by its field-save key.
func safeSetting(name string) string {
	if key := settingKey(name); key != "" {
		return key
	}
	return "credential"
}

// sensitiveValues returns the credential values of canonical values.
func sensitiveValues(values map[string]string) []string {
	var secrets []string
	for name, value := range values {
		if config.SensitiveSharedSetting(name) {
			secrets = append(secrets, value)
		}
	}
	return secrets
}

// scrub returns the message, or the fallback when the message holds one of
// the secrets.
func scrub(message, fallback string, secrets ...string) string {
	for _, secret := range secrets {
		if strings.TrimSpace(secret) != "" && strings.Contains(message, strings.TrimSpace(secret)) {
			return fallback
		}
	}
	return message
}
