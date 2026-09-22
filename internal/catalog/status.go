package catalog

import (
	"errors"
	"time"

	"github.com/agentstation/starmap/runtime"
)

// ErrRouteValidationFailed marks a candidate that did not become routable.
// Composition wraps the validation failure with it, so the operation registry
// and the admin surface name the cause without reading the failure text.
var ErrRouteValidationFailed = errors.New("catalog candidate failed route validation")

// RouteValidationState says where the newest candidate stands between the
// source and the routable head. The set is closed.
type RouteValidationState string

const (
	// RouteValidationUnknown means this instance observed no candidate yet.
	RouteValidationUnknown RouteValidationState = "unknown"
	// RouteValidationPending means a candidate is ahead of the accepted head
	// and validation has not finished.
	RouteValidationPending RouteValidationState = "pending"
	// RouteValidationAccepted means the newest candidate became the accepted
	// head.
	RouteValidationAccepted RouteValidationState = "accepted"
	// RouteValidationRejected means the newest candidate did not become
	// routable, and the accepted head still serves every request.
	RouteValidationRejected RouteValidationState = "rejected"
)

// GenerationRef names one catalog generation without disclosing its content.
type GenerationRef struct {
	// GenerationID identifies the generation.
	GenerationID string `json:"generation_id,omitempty"`
	// PayloadChecksum is the digest of the generation payload.
	PayloadChecksum string `json:"payload_checksum,omitempty"`
	// GeneratedAt is when the generation was built.
	GeneratedAt time.Time `json:"generated_at,omitzero"`
	// LeaseEpoch is the runtime lease epoch the generation was produced
	// under. Zero means the deployment shares no lease.
	LeaseEpoch uint64 `json:"lease_epoch,omitempty"`
}

// Rejection is the newest candidate this instance refused, with the safe cause
// of the refusal.
type Rejection struct {
	// Generation names the refused candidate.
	Generation GenerationRef `json:"generation,omitzero"`
	// Reason is the safe cause. It never carries a failure text.
	Reason OperationReason `json:"reason,omitempty"`
	// At is when the refusal happened.
	At time.Time `json:"at,omitzero"`
}

// RouteValidation reports the four states of the candidate-to-accepted
// transaction as separate values: what the source offered, what routes now,
// what this instance refused, and where the newest candidate stands.
type RouteValidation struct {
	// State is where the newest candidate stands.
	State RouteValidationState `json:"state"`
	// Candidate is the newest generation the connected runtime published.
	Candidate GenerationRef `json:"candidate,omitzero"`
	// Accepted is the generation that routes every request now.
	Accepted GenerationRef `json:"accepted,omitzero"`
	// Rejected is the newest candidate this instance refused.
	Rejected Rejection `json:"rejected,omitzero"`
}

// RuntimeReport is the state of the connected Starmap runtime.
type RuntimeReport struct {
	// Usable reports whether the runtime serves a catalog now.
	Usable bool `json:"usable"`
	// SourceKind names the selected source. It reads from the closed source
	// vocabulary.
	SourceKind string `json:"source_kind"`
	// Fallback reports whether the runtime serves the embedded catalog
	// because no upstream generation is active.
	Fallback bool `json:"fallback"`
	// FallbackReason names why the runtime fell back. It reads from the
	// closed fallback vocabulary.
	FallbackReason string `json:"fallback_reason,omitempty"`
	// Lease reports the runtime lease state.
	Lease string `json:"lease,omitempty"`
	// LastRunID identifies the last refresh run.
	LastRunID string `json:"last_run_id,omitempty"`
	// StartedAt is when the runtime opened.
	StartedAt time.Time `json:"started_at,omitzero"`
	// ObservedAt is when the runtime built this report.
	ObservedAt time.Time `json:"observed_at,omitzero"`
}

// AcquisitionReport is the state of provider acquisition on this instance. It
// stays separate from source health, because an instance reads a source and
// observes providers as two independent pieces of work.
type AcquisitionReport struct {
	// Enabled reports whether this instance observes providers itself.
	Enabled bool `json:"enabled"`
	// Health is the state of the last acquisition run.
	Health string `json:"health"`
	// AgeSeconds is the age of the last acquisition success.
	AgeSeconds int64 `json:"age_seconds"`
	// Freshness grades that age.
	Freshness string `json:"freshness"`
}

// FreshnessReport grades three independent ages: the served generation, the
// propagated origin publication, and the last source check.
type FreshnessReport struct {
	// Catalog grades the age of the served generation.
	Catalog string `json:"catalog"`
	// CatalogAgeSeconds is that age.
	CatalogAgeSeconds int64 `json:"catalog_age_seconds"`
	// Channel grades the age of the propagated origin publication.
	Channel string `json:"channel"`
	// ChannelAgeSeconds is that age.
	ChannelAgeSeconds int64 `json:"channel_age_seconds"`
	// SourceCheck grades the age of the last upstream check.
	SourceCheck string `json:"source_check"`
	// SourceCheckAgeSeconds is that age.
	SourceCheckAgeSeconds int64 `json:"source_check_age_seconds"`
}

// Hop is one sanitized upstream publication step.
type Hop struct {
	// Identity is the safe identity of the hop.
	Identity string `json:"identity,omitempty"`
	// Health is the health that hop reported about itself.
	Health string `json:"health"`
	// PublishedAt is when that hop published.
	PublishedAt time.Time `json:"published_at,omitzero"`
	// ObservedAt is when this instance observed the hop.
	ObservedAt time.Time `json:"observed_at,omitzero"`
}

// UpstreamProvenance is what the source said about the generation it
// published. It stays separate from the effective provenance, because this
// runtime composes its own generation over the one it received.
type UpstreamProvenance struct {
	// SourceIdentity is the safe identity of the selected source.
	SourceIdentity string `json:"source_identity,omitempty"`
	// SourceKind names the selected source.
	SourceKind string `json:"source_kind"`
	// ChannelUpdatedAt is the origin publication time the chain propagated.
	ChannelUpdatedAt time.Time `json:"channel_updated_at,omitzero"`
	// Chain is the sanitized publication chain, nearest hop first.
	Chain []Hop `json:"chain,omitempty"`
}

// Provenance carries both provenances of the served catalog: the generation
// this runtime composed and the upstream publication it came from.
type Provenance struct {
	// Effective is the accepted head this gateway routes on. It stays where
	// it stands when a newer candidate fails route validation.
	Effective GenerationRef `json:"effective,omitzero"`
	// Upstream is the publication the source reported.
	Upstream UpstreamProvenance `json:"upstream"`
}

// Counts is how much the accepted head holds.
type Counts struct {
	// Providers is the number of providers the accepted head carries.
	Providers int `json:"providers"`
	// Models is the number of routable models the accepted head carries.
	Models int `json:"models"`
}

// AdminStatus is the operator view of the catalog runtime. Every concept is a
// separate value: a degraded upstream never hides a healthy transfer, and a
// rejected candidate never hides the accepted head that still routes.
type AdminStatus struct {
	// Runtime is the state of the connected Starmap runtime.
	Runtime RuntimeReport `json:"runtime"`
	// RouteValidation reports candidate, accepted, rejected, and pending as
	// distinct values.
	RouteValidation RouteValidation `json:"route_validation"`
	// SourceHealth is what this instance observed while it read its source.
	SourceHealth string `json:"source_health"`
	// UpstreamHealth is the health the upstream reported about itself.
	UpstreamHealth string `json:"upstream_health"`
	// Acquisition is the state of provider acquisition on this instance.
	Acquisition AcquisitionReport `json:"acquisition"`
	// Freshness grades the three independent ages.
	Freshness FreshnessReport `json:"freshness"`
	// Provenance carries the effective and the upstream provenance.
	Provenance Provenance `json:"provenance"`
	// Catalog is how much the accepted head holds.
	Catalog Counts `json:"catalog"`
	// Snapshot is the manifest detail of the accepted head: its validation
	// result, its degradation reasons, and the sources that fed it.
	Snapshot SnapshotMetadata `json:"snapshot,omitzero"`
	// NextUpdateAt is when this instance next reads its source.
	NextUpdateAt time.Time `json:"next_update_at,omitzero"`
	// Operations is the recent catalog work, newest first.
	Operations []Operation `json:"operations"`
}

// Summary is the allowlisted catalog view of a reader that holds no admin
// scope. It is an allowlist and not a redaction: a field reaches it only
// because this projection names it, so a new operational value on the admin
// status never appears here by accident.
//
// It carries no source address, no source identity, no publication chain, no
// lease, no run identifier, no failure reason, and no operation.
type Summary struct {
	// GenerationID identifies the served catalog generation.
	GenerationID string `json:"generation_id"`
	// GeneratedAt is when the served generation was built.
	GeneratedAt time.Time `json:"generated_at,omitzero"`
	// AgeSeconds is the age of the served generation.
	AgeSeconds int64 `json:"age_seconds"`
	// Usable reports whether the gateway routes on a catalog now.
	Usable bool `json:"usable"`
	// Freshness grades the age of the served generation.
	Freshness string `json:"freshness"`
	// SourceKind names the selected source from the closed vocabulary.
	SourceKind string `json:"source_kind"`
	// Fallback reports whether the gateway serves the embedded baseline.
	Fallback bool `json:"fallback"`
	// Providers is how many providers the served catalog carries.
	Providers int `json:"providers"`
	// Models is how many routable models the served catalog carries.
	Models int `json:"models"`
	// NextUpdateAt is when this instance next reads its source.
	NextUpdateAt time.Time `json:"next_update_at,omitzero"`
}

// Summary projects the allowlisted reader view of the admin status. Every
// vocabulary value passes through the closed-set maps, so a value the runtime
// did not produce reads as "unknown" rather than reaching a reader.
func (s AdminStatus) Summary() Summary {
	return Summary{
		GenerationID: s.Provenance.Effective.GenerationID,
		GeneratedAt:  s.Provenance.Effective.GeneratedAt,
		AgeSeconds:   s.Freshness.CatalogAgeSeconds,
		Usable:       s.Runtime.Usable,
		Freshness:    SafeFreshness(runtime.Freshness(s.Freshness.Catalog)),
		SourceKind:   SafeSourceKind(runtime.SourceKind(s.Runtime.SourceKind)),
		Fallback:     s.Runtime.Fallback,
		Providers:    s.Catalog.Providers,
		Models:       s.Catalog.Models,
		NextUpdateAt: s.NextUpdateAt,
	}
}

// NewAdminStatus projects one connected runtime status onto the operator view.
// The caller supplies what the runtime does not know: the validation record,
// whether this instance acquires, how much the accepted head holds, when the
// next source read happens, and the recent operations.
func NewAdminStatus(
	status runtime.Status,
	validation RouteValidation,
	acquisitionEnabled bool,
	counts Counts,
	snapshot SnapshotMetadata,
	nextUpdateAt time.Time,
	operations []Operation,
) AdminStatus {
	if operations == nil {
		operations = []Operation{}
	}
	return AdminStatus{
		Runtime: RuntimeReport{
			Usable:         status.Usable,
			SourceKind:     SafeSourceKind(status.SourceKind),
			Fallback:       status.Fallback,
			FallbackReason: safeFallbackReason(status.FallbackReason),
			Lease:          status.Lease,
			LastRunID:      status.LastRunID,
			StartedAt:      status.StartedAt,
			ObservedAt:     status.ObservedAt,
		},
		RouteValidation: validation,
		SourceHealth:    SafeHealth(status.SourceHealth),
		UpstreamHealth:  SafeHealth(status.UpstreamHealth),
		Acquisition: AcquisitionReport{
			Enabled:    acquisitionEnabled,
			Health:     SafeHealth(status.AcquisitionHealth),
			AgeSeconds: wholeSeconds(status.AcquisitionAge),
			Freshness:  SafeFreshness(status.AcquisitionFreshness),
		},
		Freshness: FreshnessReport{
			Catalog:               SafeFreshness(status.Freshness),
			CatalogAgeSeconds:     wholeSeconds(status.CatalogAge),
			Channel:               SafeFreshness(status.ChannelFreshness),
			ChannelAgeSeconds:     wholeSeconds(status.ChannelAge),
			SourceCheck:           SafeFreshness(status.SourceCheckFreshness),
			SourceCheckAgeSeconds: wholeSeconds(status.SourceCheckAge),
		},
		Provenance: Provenance{
			// The effective generation is the accepted head, not the newest
			// candidate. A refused candidate must leave this reference where
			// it stands, because the accepted head is what routes a request.
			Effective: validation.Accepted,
			Upstream: UpstreamProvenance{
				SourceIdentity:   status.SourceIdentity,
				SourceKind:       SafeSourceKind(status.SourceKind),
				ChannelUpdatedAt: status.ChannelUpdatedAt,
				Chain:            safeChain(status.Chain),
			},
		},
		Catalog:      counts,
		Snapshot:     snapshot,
		NextUpdateAt: nextUpdateAt,
		Operations:   operations,
	}
}

// SafeSourceKind maps one source kind onto the closed vocabulary. A kind
// outside the set reads as "unknown", so a metric label and a reader response
// never carry a value the deployment invented.
func SafeSourceKind(kind runtime.SourceKind) string {
	switch kind {
	case runtime.SourcePublic,
		runtime.SourceGitHub,
		runtime.SourceStarmap,
		runtime.SourceFile,
		runtime.SourceEmbedded:
		return string(kind)
	default:
		return "unknown"
	}
}

// SafeHealth maps one health value onto the closed vocabulary.
func SafeHealth(health runtime.Health) string {
	switch health {
	case runtime.HealthOK,
		runtime.HealthDegraded,
		runtime.HealthUnavailable,
		runtime.HealthUnknown:
		return string(health)
	default:
		return string(runtime.HealthUnknown)
	}
}

// SafeFreshness maps one freshness grade onto the closed vocabulary.
func SafeFreshness(freshness runtime.Freshness) string {
	switch freshness {
	case runtime.FreshnessCurrent,
		runtime.FreshnessWarn,
		runtime.FreshnessCritical,
		runtime.FreshnessUnknown:
		return string(freshness)
	default:
		return string(runtime.FreshnessUnknown)
	}
}

// safeFallbackReason maps one fallback reason onto the closed vocabulary.
func safeFallbackReason(reason string) string {
	switch reason {
	case runtime.FallbackNone,
		runtime.FallbackAwaitingSource,
		runtime.FallbackSourceUnavailable:
		return reason
	default:
		return "unknown"
	}
}

// safeChain projects the publication chain. Each hop keeps its sanitized
// identity and its own reported health.
func safeChain(hops []runtime.SourceHop) []Hop {
	if len(hops) == 0 {
		return nil
	}
	chain := make([]Hop, 0, len(hops))
	for _, hop := range hops {
		chain = append(chain, Hop{
			Identity:    hop.Identity,
			Health:      SafeHealth(hop.Health),
			PublishedAt: hop.PublishedAt,
			ObservedAt:  hop.ObservedAt,
		})
	}
	return chain
}

// wholeSeconds reports one duration in whole seconds. A negative age reads as
// zero, because a clock that runs backward is not an age.
func wholeSeconds(age time.Duration) int64 {
	if age <= 0 {
		return 0
	}
	return int64(age / time.Second)
}
