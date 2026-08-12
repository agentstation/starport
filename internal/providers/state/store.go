// Package state projects safe provider runtime state from its concept
// owners. It does not own adapter activation, credential resolution, or
// offering circuit transitions.
package state

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/availability"
	"github.com/agentstation/starport/internal/execution"
	"github.com/agentstation/starport/internal/failure"
)

var (
	// ErrCatalogRequired reports an incomplete catalog-state projection.
	ErrCatalogRequired = errors.New("provider-state catalog is required")
	// ErrAdapterProjectionIncomplete reports missing or duplicate adapter state.
	ErrAdapterProjectionIncomplete = errors.New("provider adapter projection is incomplete")
)

// AdapterState identifies compiled support for one catalog provider.
type AdapterState string

// Adapter support states are a closed projection of compiled primitives and
// catalog offerings.
const (
	AdapterReady                     AdapterState = "ready"
	AdapterUnsupportedTransport      AdapterState = "unsupported_transport"
	AdapterUnsupportedAuthentication AdapterState = "unsupported_authentication"
	AdapterNoOfferings               AdapterState = "no_offerings"
)

// CredentialState identifies operator inference credential state.
type CredentialState string

// Operator credential states are a closed lifecycle projection.
const (
	CredentialReady         CredentialState = "ready"
	CredentialNotConfigured CredentialState = "not_configured"
	CredentialDenied        CredentialState = "denied"
	CredentialInvalid       CredentialState = "invalid"
	CredentialUnavailable   CredentialState = "unavailable"
	CredentialRefreshing    CredentialState = "refreshing"
)

// ReasonCode is a stable, secret-free operator reason.
type ReasonCode string

// Provider-state reason codes are stable and contain no provider diagnostics
// or credential material.
const (
	ReasonNone                      ReasonCode = ""
	ReasonTransportUnsupported      ReasonCode = "transport_unsupported"
	ReasonAuthenticationUnsupported ReasonCode = "authentication_unsupported"
	ReasonNoOfferings               ReasonCode = "no_offerings"
	ReasonOperatorNotRequired       ReasonCode = "credential_not_required"
	ReasonOperatorNotConfigured     ReasonCode = "credential_not_configured"
	ReasonOperatorSourceDenied      ReasonCode = "credential_source_denied"
	ReasonOperatorSourceInvalid     ReasonCode = "credential_source_invalid"
	ReasonOperatorSourceUnavailable ReasonCode = "credential_source_unavailable"
	ReasonOperatorRefreshRetained   ReasonCode = "credential_refresh_failed_retained"
	ReasonOperatorRefreshing        ReasonCode = "credential_refreshing"
	ReasonAuthenticationFailed      ReasonCode = "authentication_failed"
	ReasonPermissionDenied          ReasonCode = "permission_denied"
	ReasonQuotaExceeded             ReasonCode = "quota_exceeded"
	ReasonBillingUnavailable        ReasonCode = "billing_unavailable"
	ReasonCatalogUnavailable        ReasonCode = "catalog_unavailable"
	ReasonCatalogRetired            ReasonCode = "catalog_retired"
	ReasonOfferingNotFound          ReasonCode = "offering_not_found"
	ReasonRateLimited               ReasonCode = "rate_limited"
	ReasonProviderUnavailable       ReasonCode = "provider_unavailable"
	ReasonProviderTimeout           ReasonCode = "provider_timeout"
)

// AdapterObservation is the activation owner's safe projection.
type AdapterObservation struct {
	ProviderID catalogs.ProviderID
	State      AdapterState
	Reason     ReasonCode
}

// CredentialObservation is the reconciler's safe lifecycle projection.
// MaterialVersion is opaque internal evidence and never enters Snapshot.
type CredentialObservation struct {
	ProviderID      catalogs.ProviderID
	State           CredentialState
	Reason          ReasonCode
	Usable          bool
	MaterialVersion string
}

// CredentialGeneration replaces the complete operator credential projection
// for one exact catalog generation.
type CredentialGeneration struct {
	CatalogGenerationID string
	Observations        []CredentialObservation
}

// CredentialPublisher accepts complete reconciler projections.
type CredentialPublisher interface {
	PublishCredentials(CredentialGeneration)
}

// AdapterStatus is safe compiled support state.
type AdapterStatus struct {
	State  AdapterState `json:"state"`
	Reason ReasonCode   `json:"reason,omitempty"`
}

// CredentialStatus is safe operator credential state.
type CredentialStatus struct {
	State     CredentialState `json:"state"`
	Reason    ReasonCode      `json:"reason,omitempty"`
	Usable    bool            `json:"usable"`
	UpdatedAt time.Time       `json:"updated_at,omitempty"`
}

// OfferingStatus is the projected state of one exact opaque provider model ID.
type OfferingStatus struct {
	ProviderModelID catalogs.ProviderModelID `json:"provider_model_id"`
	State           availability.State       `json:"state"`
	Reason          ReasonCode               `json:"reason,omitempty"`
}

// ProviderStatus separates adapter, operator credential, and offering state.
type ProviderStatus struct {
	ProviderID         catalogs.ProviderID `json:"provider_id"`
	Adapter            AdapterStatus       `json:"adapter"`
	OperatorCredential CredentialStatus    `json:"operator_credential"`
	Offerings          []OfferingStatus    `json:"offerings"`
}

// Snapshot is one caller-owned immutable provider-state generation.
type Snapshot struct {
	Revision            uint64           `json:"revision"`
	CatalogGenerationID string           `json:"catalog_generation_id"`
	Providers           []ProviderStatus `json:"providers"`
}

type clock interface{ Now() time.Time }
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

type credentialEntry struct {
	status          CredentialStatus
	materialVersion string
	failedVersion   string
}

// Store retains safe projections and exact opaque versions needed to reject
// stale inference outcomes.
type Store struct {
	mu sync.RWMutex

	clock                clock
	revision             uint64
	catalogGenerationID  string
	availabilityRevision uint64
	adapters             map[catalogs.ProviderID]AdapterStatus
	credentials          map[catalogs.ProviderID]credentialEntry
	catalogOfferings     map[availability.Offering]OfferingStatus
	offerings            map[availability.Offering]OfferingStatus
}

// New creates an empty provider-state projection.
func New() *Store { return newWithClock(systemClock{}) }

func newWithClock(source clock) *Store {
	if source == nil {
		source = systemClock{}
	}
	return &Store{
		clock:            source,
		adapters:         make(map[catalogs.ProviderID]AdapterStatus),
		credentials:      make(map[catalogs.ProviderID]credentialEntry),
		catalogOfferings: make(map[availability.Offering]OfferingStatus),
		offerings:        make(map[availability.Offering]OfferingStatus),
	}
}

// PublishCatalog replaces the complete catalog and adapter projection.
func (s *Store) PublishCatalog(
	generationID string,
	catalog *catalogs.Catalog,
	observations []AdapterObservation,
) error {
	if s == nil || catalog == nil || strings.TrimSpace(generationID) == "" {
		return ErrCatalogRequired
	}
	observed := make(map[catalogs.ProviderID]AdapterStatus, len(observations))
	for _, item := range observations {
		if item.ProviderID == "" || !validAdapterObservation(item) {
			return ErrAdapterProjectionIncomplete
		}
		if _, duplicate := observed[item.ProviderID]; duplicate {
			return fmt.Errorf("%s: %w", item.ProviderID, ErrAdapterProjectionIncomplete)
		}
		observed[item.ProviderID] = AdapterStatus{State: item.State, Reason: item.Reason}
	}

	adapters := make(map[catalogs.ProviderID]AdapterStatus)
	catalogOfferings := make(map[availability.Offering]OfferingStatus)
	providerRecords := catalog.Providers().List()
	if len(observed) != len(providerRecords) {
		return ErrAdapterProjectionIncomplete
	}
	for _, provider := range providerRecords {
		status, exists := observed[provider.ID]
		if !exists {
			return fmt.Errorf("%s: %w", provider.ID, ErrAdapterProjectionIncomplete)
		}
		adapters[provider.ID] = status
		providerOfferings, err := catalog.ProviderOfferings(provider.ID)
		if err != nil {
			return fmt.Errorf("project %s offerings: %w", provider.ID, err)
		}
		for _, offering := range providerOfferings {
			identity := availability.Offering{
				ProviderID: string(provider.ID), ProviderModelID: string(offering.ProviderModelID),
			}
			status := OfferingStatus{
				ProviderModelID: offering.ProviderModelID, State: availability.StateHealthy,
			}
			switch {
			case offering.Lifecycle == catalogs.OfferingLifecycleRetired:
				status.State = availability.StateUnavailable
				status.Reason = ReasonCatalogRetired
			case offering.Availability == catalogs.OfferingAvailabilityUnavailable:
				status.State = availability.StateUnavailable
				status.Reason = ReasonCatalogUnavailable
			}
			catalogOfferings[identity] = status
		}
	}
	offerings := cloneOfferings(catalogOfferings)

	s.mu.Lock()
	for identity, prior := range s.offerings {
		if current, exists := offerings[identity]; exists &&
			current.State == availability.StateHealthy &&
			prior.State != availability.StateHealthy {
			current.State = prior.State
			current.Reason = prior.Reason
			offerings[identity] = current
		}
	}
	credentials := make(map[catalogs.ProviderID]credentialEntry, len(adapters))
	for providerID := range adapters {
		if prior, exists := s.credentials[providerID]; exists {
			credentials[providerID] = prior
			continue
		}
		credentials[providerID] = credentialEntry{status: CredentialStatus{
			State: CredentialNotConfigured, Reason: ReasonOperatorNotConfigured,
		}}
	}
	changed := s.catalogGenerationID != generationID ||
		!equalAdapters(s.adapters, adapters) || !equalOfferings(s.offerings, offerings)
	s.catalogGenerationID = generationID
	s.adapters = adapters
	s.credentials = credentials
	s.catalogOfferings = catalogOfferings
	s.offerings = offerings
	if changed {
		s.revision++
	}
	s.mu.Unlock()
	return nil
}

func validAdapterObservation(observation AdapterObservation) bool {
	switch observation.State {
	case AdapterReady:
		return observation.Reason == ReasonNone
	case AdapterUnsupportedTransport:
		return observation.Reason == ReasonTransportUnsupported
	case AdapterUnsupportedAuthentication:
		return observation.Reason == ReasonAuthenticationUnsupported
	case AdapterNoOfferings:
		return observation.Reason == ReasonNoOfferings
	default:
		return false
	}
}

// PublishCredentials replaces the reconciler projection. A successful read of
// the same material version does not erase an inference-proved failure.
func (s *Store) PublishCredentials(generation CredentialGeneration) {
	if s == nil || strings.TrimSpace(generation.CatalogGenerationID) == "" {
		return
	}
	now := s.clock.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.catalogGenerationID != "" && s.catalogGenerationID != generation.CatalogGenerationID {
		return
	}
	observed := make(map[catalogs.ProviderID]CredentialObservation, len(generation.Observations))
	for _, observation := range generation.Observations {
		if _, exists := s.adapters[observation.ProviderID]; !exists ||
			!validCredentialObservation(observation) {
			continue
		}
		observed[observation.ProviderID] = observation
	}
	changed := false
	for providerID := range s.adapters {
		observation, exists := observed[providerID]
		if !exists {
			observation = CredentialObservation{
				ProviderID: providerID,
				State:      CredentialNotConfigured,
				Reason:     ReasonOperatorNotConfigured,
			}
		}
		prior := s.credentials[providerID]
		next := credentialEntry{
			status: CredentialStatus{
				State: observation.State, Reason: observation.Reason,
				Usable: observation.Usable,
			},
			materialVersion: observation.MaterialVersion,
		}
		if observation.State == CredentialRefreshing &&
			observation.MaterialVersion != "" &&
			observation.MaterialVersion == prior.failedVersion {
			next = prior
		} else if observation.State == CredentialRefreshing {
			next.failedVersion = prior.failedVersion
		} else if observation.MaterialVersion != "" &&
			observation.MaterialVersion == prior.failedVersion &&
			observation.State == CredentialReady {
			next = prior
		}
		if equalCredentialEntryWithoutTime(prior, next) {
			continue
		}
		next.status.UpdatedAt = now
		s.credentials[providerID] = next
		changed = true
	}
	if changed {
		s.revision++
	}
}

func validCredentialObservation(observation CredentialObservation) bool {
	switch observation.State {
	case CredentialReady:
		return observation.Usable && (observation.Reason == ReasonNone ||
			observation.Reason == ReasonOperatorNotRequired ||
			observation.Reason == ReasonOperatorRefreshRetained)
	case CredentialNotConfigured:
		return !observation.Usable && observation.Reason == ReasonOperatorNotConfigured
	case CredentialDenied:
		return !observation.Usable && (observation.Reason == ReasonOperatorSourceDenied ||
			observation.Reason == ReasonPermissionDenied)
	case CredentialInvalid:
		return !observation.Usable && (observation.Reason == ReasonOperatorSourceInvalid ||
			observation.Reason == ReasonAuthenticationFailed)
	case CredentialUnavailable:
		return !observation.Usable && (observation.Reason == ReasonOperatorSourceUnavailable ||
			observation.Reason == ReasonQuotaExceeded ||
			observation.Reason == ReasonBillingUnavailable)
	case CredentialRefreshing:
		return observation.Reason == ReasonOperatorRefreshing
	default:
		return false
	}
}

// PublishAvailability projects the availability owner's complete snapshot.
func (s *Store) PublishAvailability(snapshot availability.Snapshot) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if snapshot.Revision <= s.availabilityRevision {
		return nil
	}
	next := cloneOfferings(s.catalogOfferings)
	for _, record := range snapshot.Records {
		status, exists := next[record.Offering]
		if !exists || status.State != availability.StateHealthy {
			continue
		}
		status.State = record.State
		status.Reason = offeringReason(record.FailureKind)
		next[record.Offering] = status
	}
	s.availabilityRevision = snapshot.Revision
	if !equalOfferings(s.offerings, next) {
		s.offerings = next
		s.revision++
	}
	return nil
}

// PublishOutcome applies only explicitly scoped operator credential evidence.
func (s *Store) PublishOutcome(outcome execution.AttemptOutcome) {
	if s == nil || outcome.Credential.Owner != execution.CredentialOwnerOperator ||
		strings.TrimSpace(outcome.Credential.MaterialVersion) == "" {
		return
	}
	providerID := catalogs.ProviderID(outcome.Route.ProviderID)
	now := s.clock.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, exists := s.credentials[providerID]
	if !exists || entry.materialVersion != outcome.Credential.MaterialVersion {
		return
	}
	if outcome.Credential.Accepted {
		if entry.failedVersion != outcome.Credential.MaterialVersion {
			return
		}
		entry.failedVersion = ""
		entry.status = CredentialStatus{State: CredentialReady, Usable: true, UpdatedAt: now}
		s.credentials[providerID] = entry
		s.revision++
		return
	}
	if outcome.Failure == nil {
		return
	}
	if outcome.Failure.StateScope() != failure.ScopeCredential {
		return
	}
	state, reason, supported := credentialFailureState(outcome.Failure.Kind())
	if !supported {
		return
	}
	next := CredentialStatus{State: state, Reason: reason, Usable: false}
	if entry.failedVersion == outcome.Credential.MaterialVersion &&
		equalCredentialStatusWithoutTime(entry.status, next) {
		return
	}
	next.UpdatedAt = now
	entry.status = next
	entry.failedVersion = outcome.Credential.MaterialVersion
	s.credentials[providerID] = entry
	s.revision++
}

// OperatorMaterialReady reports whether an exact resolved material version is
// eligible for a new inference attempt. A version not yet observed by the
// projection is admitted so an atomic runtime publication cannot be blocked by
// a lagging status projection.
func (s *Store) OperatorMaterialReady(providerID string, materialVersion string) bool {
	if s == nil || strings.TrimSpace(materialVersion) == "" {
		return true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, exists := s.credentials[catalogs.ProviderID(providerID)]
	if !exists || entry.materialVersion != materialVersion {
		return true
	}
	return entry.failedVersion != materialVersion
}

// Snapshot returns a caller-owned, deterministically ordered projection.
func (s *Store) Snapshot() Snapshot {
	if s == nil {
		return Snapshot{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	providerIDs := make([]catalogs.ProviderID, 0, len(s.adapters))
	for providerID := range s.adapters {
		providerIDs = append(providerIDs, providerID)
	}
	sort.Slice(providerIDs, func(left, right int) bool { return providerIDs[left] < providerIDs[right] })
	providers := make([]ProviderStatus, 0, len(providerIDs))
	for _, providerID := range providerIDs {
		credential := s.credentials[providerID].status
		provider := ProviderStatus{
			ProviderID: providerID, Adapter: s.adapters[providerID],
			OperatorCredential: credential,
		}
		for identity, offering := range s.offerings {
			if identity.ProviderID == string(providerID) {
				provider.Offerings = append(provider.Offerings, offering)
			}
		}
		sort.Slice(provider.Offerings, func(left, right int) bool {
			return provider.Offerings[left].ProviderModelID < provider.Offerings[right].ProviderModelID
		})
		providers = append(providers, provider)
	}
	return Snapshot{
		Revision: s.revision, CatalogGenerationID: s.catalogGenerationID, Providers: providers,
	}
}

func credentialFailureState(kind failure.Kind) (CredentialState, ReasonCode, bool) {
	switch kind {
	case failure.Authentication:
		return CredentialInvalid, ReasonAuthenticationFailed, true
	case failure.Permission:
		return CredentialDenied, ReasonPermissionDenied, true
	case failure.Quota:
		return CredentialUnavailable, ReasonQuotaExceeded, true
	case failure.Billing:
		return CredentialUnavailable, ReasonBillingUnavailable, true
	default:
		return "", "", false
	}
}

func offeringReason(kind failure.Kind) ReasonCode {
	switch kind {
	case failure.NotFound:
		return ReasonOfferingNotFound
	case failure.RateLimit:
		return ReasonRateLimited
	case failure.Quota:
		return ReasonQuotaExceeded
	case failure.Timeout:
		return ReasonProviderTimeout
	case failure.ProviderUnavailable:
		return ReasonProviderUnavailable
	default:
		return ReasonNone
	}
}

func equalAdapters(left, right map[catalogs.ProviderID]AdapterStatus) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func equalOfferings(left, right map[availability.Offering]OfferingStatus) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func cloneOfferings(
	items map[availability.Offering]OfferingStatus,
) map[availability.Offering]OfferingStatus {
	cloned := make(map[availability.Offering]OfferingStatus, len(items))
	for identity, status := range items {
		cloned[identity] = status
	}
	return cloned
}

func equalCredentialEntryWithoutTime(left, right credentialEntry) bool {
	return equalCredentialStatusWithoutTime(left.status, right.status) &&
		left.materialVersion == right.materialVersion &&
		left.failedVersion == right.failedVersion
}

func equalCredentialStatusWithoutTime(left, right CredentialStatus) bool {
	return left.State == right.State && left.Reason == right.Reason && left.Usable == right.Usable
}
