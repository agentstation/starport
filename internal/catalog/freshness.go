package catalog

import (
	"context"
	stderrors "errors"
	"fmt"
	"sort"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	starmaperrors "github.com/agentstation/starmap/pkg/errors"
)

// SnapshotSource supplies the active routable snapshot.
type SnapshotSource interface {
	Current() *RoutableSnapshot
}

// ValidationSummary condenses the generation validation report for operators.
type ValidationSummary struct {
	Status       string    `json:"status"`
	ErrorCount   int       `json:"error_count"`
	WarningCount int       `json:"warning_count"`
	ValidatedAt  time.Time `json:"validated_at"`
}

// SourceObservation reports one acquisition source that fed the generation.
type SourceObservation struct {
	Source       string    `json:"source"`
	ObservedAt   time.Time `json:"observed_at"`
	Completeness string    `json:"completeness"`
	Status       string    `json:"status"`
}

// SnapshotMetadata is the freshness surface of the active catalog snapshot.
// The scalar identity always comes from the snapshot itself. Manifest detail
// comes from the stored generation record; when that record is missing the
// metadata says so instead of silently omitting fields.
type SnapshotMetadata struct {
	GenerationID         string    `json:"generation_id"`
	GeneratedAt          time.Time `json:"generated_at"`
	AgeSeconds           int64     `json:"age_seconds"`
	CatalogSequence      uint64    `json:"catalog_sequence"`
	AvailabilityRevision uint64    `json:"availability_revision"`
	PayloadChecksum      string    `json:"payload_checksum"`

	ManifestAvailable         bool   `json:"manifest_available"`
	ManifestUnavailableReason string `json:"manifest_unavailable_reason,omitempty"`

	SchemaVersion      uint64              `json:"schema_version,omitempty"`
	PayloadSizeBytes   int64               `json:"payload_size_bytes,omitempty"`
	Completeness       string              `json:"completeness,omitempty"`
	Degraded           bool                `json:"degraded"`
	DegradationReasons []string            `json:"degradation_reasons,omitempty"`
	Validation         ValidationSummary   `json:"validation,omitzero"`
	SourceObservations []SourceObservation `json:"source_observations,omitempty"`
	SyncRunID          string              `json:"sync_run_id,omitempty"`
}

// OfferingChange identifies one provider offering added or removed between
// two accepted generations.
type OfferingChange struct {
	Provider        string `json:"provider"`
	ProviderModelID string `json:"provider_model_id"`
	DefinitionID    string `json:"definition_id"`
}

// PriceChange reports one token-price movement on an offering present in both
// generations. Values are USD per one million tokens.
type PriceChange struct {
	Provider        string  `json:"provider"`
	ProviderModelID string  `json:"provider_model_id"`
	DefinitionID    string  `json:"definition_id"`
	Field           string  `json:"field"`
	PreviousPer1M   float64 `json:"previous_per_1m"`
	CurrentPer1M    float64 `json:"current_per_1m"`
}

// Diff compares the previous accepted generation against the current
// one. When only one generation is recorded, Available is false and Reason
// says why — that is a normal state, not an error.
type Diff struct {
	Available         bool      `json:"available"`
	Reason            string    `json:"reason,omitempty"`
	FromGenerationID  string    `json:"from_generation_id,omitempty"`
	ToGenerationID    string    `json:"to_generation_id,omitempty"`
	FromGeneratedAt   time.Time `json:"from_generated_at,omitzero"`
	ToGeneratedAt     time.Time `json:"to_generated_at,omitzero"`
	SemanticallyEqual bool      `json:"semantically_equal"`

	ModelsAdded      []string         `json:"models_added,omitempty"`
	ModelsRemoved    []string         `json:"models_removed,omitempty"`
	OfferingsAdded   []OfferingChange `json:"offerings_added,omitempty"`
	OfferingsRemoved []OfferingChange `json:"offerings_removed,omitempty"`
	PriceChanges     []PriceChange    `json:"price_changes,omitempty"`
}

// RefreshReport summarizes one forced catalog acquisition.
type RefreshReport struct {
	PreviousGenerationID string    `json:"previous_generation_id"`
	GenerationID         string    `json:"generation_id"`
	GeneratedAt          time.Time `json:"generated_at"`
	Changed              bool      `json:"changed"`
}

// FreshnessService reads catalog freshness from the active snapshot and the
// durable generation store. It never mutates either.
type FreshnessService struct {
	snapshots   SnapshotSource
	generations *GenerationStore
	now         func() time.Time
}

// NewFreshnessService creates the freshness read service.
func NewFreshnessService(snapshots SnapshotSource, generations *GenerationStore) *FreshnessService {
	return &FreshnessService{snapshots: snapshots, generations: generations, now: time.Now}
}

// Metadata reports the active snapshot's identity, age, and manifest facts.
func (s *FreshnessService) Metadata(ctx context.Context) (SnapshotMetadata, error) {
	snapshot := s.snapshots.Current()
	if snapshot == nil {
		return SnapshotMetadata{}, stderrors.New("no catalog snapshot is active")
	}
	metadata := SnapshotMetadata{
		GenerationID:         snapshot.GenerationID(),
		GeneratedAt:          snapshot.GeneratedAt(),
		AgeSeconds:           int64(s.now().Sub(snapshot.GeneratedAt()) / time.Second),
		CatalogSequence:      snapshot.CatalogSequence(),
		AvailabilityRevision: snapshot.AvailabilityRevision(),
		PayloadChecksum:      snapshot.PayloadChecksum(),
	}
	generation, err := s.generations.Get(ctx, snapshot.GenerationID())
	if err != nil {
		var notFound *starmaperrors.NotFoundError
		if stderrors.As(err, &notFound) {
			metadata.ManifestUnavailableReason = fmt.Sprintf(
				"no stored generation record for %q, so the snapshot predates durable generation storage",
				snapshot.GenerationID(),
			)
			return metadata, nil
		}
		return SnapshotMetadata{}, err
	}
	manifest := generation.Manifest
	metadata.ManifestAvailable = true
	metadata.SchemaVersion = manifest.SchemaVersion
	metadata.PayloadSizeBytes = manifest.Payload.SizeBytes
	metadata.Completeness = string(manifest.Completeness)
	metadata.Degraded = manifest.Degraded
	metadata.DegradationReasons = append([]string(nil), manifest.DegradationReasons...)
	metadata.SyncRunID = manifest.SyncRunID
	metadata.Validation = ValidationSummary{
		Status:       string(manifest.Validation.Status),
		ErrorCount:   manifest.Validation.ErrorCount,
		WarningCount: manifest.Validation.WarningCount,
		ValidatedAt:  manifest.Validation.ValidatedAt,
	}
	for _, link := range manifest.SourceObservations {
		metadata.SourceObservations = append(metadata.SourceObservations, SourceObservation{
			Source:       string(link.Source),
			ObservedAt:   link.ObservedAt,
			Completeness: string(link.Completeness),
			Status:       string(link.Status),
		})
	}
	return metadata, nil
}

// Changes diffs the previous accepted generation against the current one.
func (s *FreshnessService) Changes(ctx context.Context) (Diff, error) {
	history, err := s.generations.History(ctx)
	if err != nil {
		return Diff{}, err
	}
	if len(history) < 2 {
		return Diff{
			Reason: "fewer than two accepted generations are recorded, so there is nothing to compare yet",
		}, nil
	}
	previous := history[len(history)-2]
	current := history[len(history)-1]
	diff := Diff{
		Available:        true,
		FromGenerationID: previous.GenerationID,
		ToGenerationID:   current.GenerationID,
		FromGeneratedAt:  previous.GeneratedAt,
		ToGeneratedAt:    current.GeneratedAt,
	}
	if previous.SemanticChecksum != "" && previous.SemanticChecksum == current.SemanticChecksum {
		diff.SemanticallyEqual = true
		return diff, nil
	}
	fromGeneration, err := s.generations.Get(ctx, previous.GenerationID)
	if err != nil {
		return Diff{}, fmt.Errorf("load previous generation %q: %w", previous.GenerationID, err)
	}
	toGeneration, err := s.generations.Get(ctx, current.GenerationID)
	if err != nil {
		return Diff{}, fmt.Errorf("load current generation %q: %w", current.GenerationID, err)
	}
	fromCatalog, err := catalogs.DecodeCatalogPayload(fromGeneration.Payload)
	if err != nil {
		return Diff{}, fmt.Errorf("decode previous generation %q: %w", previous.GenerationID, err)
	}
	toCatalog, err := catalogs.DecodeCatalogPayload(toGeneration.Payload)
	if err != nil {
		return Diff{}, fmt.Errorf("decode current generation %q: %w", current.GenerationID, err)
	}
	diffOfferings(&diff, collectOfferings(fromCatalog), collectOfferings(toCatalog))
	return diff, nil
}

type offeringIdentity struct {
	provider        string
	providerModelID string
}

type offeringFacts struct {
	definitionID string
	pricing      *catalogs.ModelPricing
}

func collectOfferings(catalog *catalogs.Catalog) map[offeringIdentity]offeringFacts {
	result := map[offeringIdentity]offeringFacts{}
	for _, provider := range catalog.Providers().List() {
		offerings, err := catalog.ProviderOfferings(provider.ID)
		if err != nil {
			continue
		}
		for _, offering := range offerings {
			identity := offeringIdentity{
				provider:        string(offering.ProviderID),
				providerModelID: string(offering.ProviderModelID),
			}
			result[identity] = offeringFacts{
				definitionID: string(offering.DefinitionID),
				pricing:      offering.Pricing,
			}
		}
	}
	return result
}

// priceFields is the fixed comparison order for token prices.
var priceFields = []string{
	"input", "output", "reasoning", "cache_read", "cache_write",
	"audio_input", "audio_output",
}

func diffOfferings(diff *Diff, from, to map[offeringIdentity]offeringFacts) {
	fromModels := map[string]bool{}
	toModels := map[string]bool{}
	for _, facts := range from {
		fromModels[facts.definitionID] = true
	}
	for _, facts := range to {
		toModels[facts.definitionID] = true
	}
	for definitionID := range toModels {
		if !fromModels[definitionID] {
			diff.ModelsAdded = append(diff.ModelsAdded, definitionID)
		}
	}
	for definitionID := range fromModels {
		if !toModels[definitionID] {
			diff.ModelsRemoved = append(diff.ModelsRemoved, definitionID)
		}
	}
	sort.Strings(diff.ModelsAdded)
	sort.Strings(diff.ModelsRemoved)

	for _, identity := range sortedIdentities(to) {
		if _, exists := from[identity]; !exists {
			diff.OfferingsAdded = append(diff.OfferingsAdded, offeringChange(identity, to[identity]))
		}
	}
	for _, identity := range sortedIdentities(from) {
		if _, exists := to[identity]; !exists {
			diff.OfferingsRemoved = append(diff.OfferingsRemoved, offeringChange(identity, from[identity]))
			continue
		}
		previous := from[identity]
		current := to[identity]
		for _, field := range priceFields {
			previousPrice, previousOK := tokenPrice(previous.pricing, field)
			currentPrice, currentOK := tokenPrice(current.pricing, field)
			// An absent price compares as zero: appearing or disappearing
			// pricing is a change the operator should see.
			if !previousOK && !currentOK {
				continue
			}
			if previousPrice == currentPrice {
				continue
			}
			diff.PriceChanges = append(diff.PriceChanges, PriceChange{
				Provider:        identity.provider,
				ProviderModelID: identity.providerModelID,
				DefinitionID:    current.definitionID,
				Field:           field,
				PreviousPer1M:   previousPrice,
				CurrentPer1M:    currentPrice,
			})
		}
	}
}

func offeringChange(identity offeringIdentity, facts offeringFacts) OfferingChange {
	return OfferingChange{
		Provider:        identity.provider,
		ProviderModelID: identity.providerModelID,
		DefinitionID:    facts.definitionID,
	}
}

func sortedIdentities(offerings map[offeringIdentity]offeringFacts) []offeringIdentity {
	identities := make([]offeringIdentity, 0, len(offerings))
	for identity := range offerings {
		identities = append(identities, identity)
	}
	sort.Slice(identities, func(i, j int) bool {
		if identities[i].provider != identities[j].provider {
			return identities[i].provider < identities[j].provider
		}
		return identities[i].providerModelID < identities[j].providerModelID
	})
	return identities
}

func tokenPrice(pricing *catalogs.ModelPricing, field string) (float64, bool) {
	if pricing == nil || pricing.Tokens == nil {
		return 0, false
	}
	var cost *catalogs.ModelTokenCost
	switch field {
	case "input":
		cost = pricing.Tokens.Input
	case "output":
		cost = pricing.Tokens.Output
	case "reasoning":
		cost = pricing.Tokens.Reasoning
	case "cache_read":
		cost = pricing.Tokens.CacheRead
	case "cache_write":
		cost = pricing.Tokens.CacheWrite
	case "audio_input":
		cost = pricing.Tokens.AudioInput
	case "audio_output":
		cost = pricing.Tokens.AudioOutput
	}
	if cost == nil {
		return 0, false
	}
	return cost.Per1M, true
}
