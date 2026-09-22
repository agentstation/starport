package catalog

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/runtime"
)

// TestAdminStatusSeparatesEveryOperationalValue proves the operator view
// reports the runtime state, the health of the source this instance reads, the
// health the upstream reports about itself, the acquisition state, the
// freshness grades, and both provenances as separate values.
func TestAdminStatusSeparatesEveryOperationalValue(t *testing.T) {
	observedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	generatedAt := time.Date(2026, 9, 1, 11, 0, 0, 0, time.UTC)
	status := runtime.Status{
		Usable:               true,
		GenerationID:         "gen-4",
		PayloadChecksum:      "sha256:abc",
		CatalogAge:           90 * time.Second,
		Freshness:            runtime.FreshnessCurrent,
		ChannelAge:           30 * time.Minute,
		ChannelFreshness:     runtime.FreshnessWarn,
		SourceCheckAge:       2 * time.Minute,
		SourceCheckFreshness: runtime.FreshnessCurrent,
		AcquisitionAge:       4 * time.Hour,
		AcquisitionFreshness: runtime.FreshnessCritical,
		SourceHealth:         runtime.HealthOK,
		UpstreamHealth:       runtime.HealthDegraded,
		AcquisitionHealth:    runtime.HealthUnavailable,
		SourceIdentity:       "starmap_cascade",
		SourceKind:           runtime.SourceStarmap,
		ChannelUpdatedAt:     generatedAt,
		Chain: []runtime.SourceHop{
			{Identity: "starmap_cascade", Health: runtime.HealthOK},
			{Identity: "starmap_edge", Health: runtime.HealthDegraded},
		},
		Lease:      "instance-1",
		LastRunID:  "run-9",
		ObservedAt: observedAt,
	}
	validation := RouteValidation{
		State: RouteValidationAccepted,
		Accepted: GenerationRef{
			GenerationID:    "gen-4",
			PayloadChecksum: "sha256:abc",
			GeneratedAt:     generatedAt,
			LeaseEpoch:      7,
		},
	}

	report := NewAdminStatus(
		status,
		validation,
		true,
		Counts{Providers: 5, Models: 20},
		SnapshotMetadata{GenerationID: "gen-4", ManifestAvailable: true},
		observedAt.Add(15*time.Minute),
		nil,
	)

	// The runtime state and the two health readings stay apart.
	assert.True(t, report.Runtime.Usable)
	assert.Equal(t, "starmap", report.Runtime.SourceKind)
	assert.Equal(t, "instance-1", report.Runtime.Lease)
	assert.Equal(t, "ok", report.SourceHealth)
	assert.Equal(t, "degraded", report.UpstreamHealth)
	assert.NotEqual(t, report.SourceHealth, report.UpstreamHealth)

	// The acquisition state is its own value, not a freshness grade.
	assert.True(t, report.Acquisition.Enabled)
	assert.Equal(t, "unavailable", report.Acquisition.Health)
	assert.Equal(t, "critical", report.Acquisition.Freshness)
	assert.Equal(t, int64(14400), report.Acquisition.AgeSeconds)

	// The three freshness grades stay apart.
	assert.Equal(t, "current", report.Freshness.Catalog)
	assert.Equal(t, int64(90), report.Freshness.CatalogAgeSeconds)
	assert.Equal(t, "warn", report.Freshness.Channel)
	assert.Equal(t, "current", report.Freshness.SourceCheck)

	// Both provenances are reported: what this instance routes, and what the
	// upstream published.
	assert.Equal(t, "gen-4", report.Provenance.Effective.GenerationID)
	assert.Equal(t, "sha256:abc", report.Provenance.Effective.PayloadChecksum)
	assert.Equal(t, "starmap_cascade", report.Provenance.Upstream.SourceIdentity)
	assert.Equal(t, generatedAt, report.Provenance.Upstream.ChannelUpdatedAt)
	require.Len(t, report.Provenance.Upstream.Chain, 2)
	assert.Equal(t, "degraded", report.Provenance.Upstream.Chain[1].Health)

	assert.Equal(t, 5, report.Catalog.Providers)
	assert.Equal(t, "gen-4", report.Snapshot.GenerationID)
	assert.NotNil(t, report.Operations, "an empty history is a list, not a null")
}

// TestEffectiveProvenanceStaysAtTheAcceptedHead proves the effective
// generation the operator reads is the head this gateway routes on. A newer
// candidate the connected runtime published, and a candidate route validation
// refused, both leave the reference where it stands.
func TestEffectiveProvenanceStaysAtTheAcceptedHead(t *testing.T) {
	t.Parallel()

	accepted := GenerationRef{
		GenerationID:    "gen-4",
		PayloadChecksum: "sha256:accepted",
		GeneratedAt:     time.Date(2026, 9, 1, 11, 0, 0, 0, time.UTC),
		LeaseEpoch:      7,
	}
	candidate := GenerationRef{
		GenerationID:    "gen-5",
		PayloadChecksum: "sha256:candidate",
		GeneratedAt:     time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
		LeaseEpoch:      7,
	}
	// The connected runtime already serves the newer candidate, so the status
	// names it. Acceptance is the separate step this gateway owns.
	status := runtime.Status{
		Usable:          true,
		GenerationID:    candidate.GenerationID,
		PayloadChecksum: candidate.PayloadChecksum,
	}

	tests := []struct {
		name       string
		validation RouteValidation
		want       GenerationRef
	}{
		{
			name: "an accepted candidate is the head",
			validation: RouteValidation{
				State:     RouteValidationAccepted,
				Candidate: accepted,
				Accepted:  accepted,
			},
			want: accepted,
		},
		{
			name: "a pending candidate does not move the head",
			validation: RouteValidation{
				State:     RouteValidationPending,
				Candidate: candidate,
				Accepted:  accepted,
			},
			want: accepted,
		},
		{
			name: "a refused candidate does not move the head",
			validation: RouteValidation{
				State:     RouteValidationRejected,
				Candidate: candidate,
				Accepted:  accepted,
				Rejected: Rejection{
					Generation: candidate,
					Reason:     ReasonInternalError,
				},
			},
			want: accepted,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			report := NewAdminStatus(
				status,
				test.validation,
				true,
				Counts{},
				SnapshotMetadata{},
				time.Time{},
				nil,
			)
			assert.Equal(t, test.want, report.Provenance.Effective)
			assert.Equal(
				t, test.validation.Candidate,
				report.RouteValidation.Candidate,
				"the newest candidate stays a separate value",
			)
		})
	}
}

// TestAdminStatusMapsEveryVocabularyOntoTheClosedSet proves a value the
// runtime did not produce never reaches an operator, a reader, or a metric
// label.
func TestAdminStatusMapsEveryVocabularyOntoTheClosedSet(t *testing.T) {
	invented := "s3://private-bucket/catalog"
	report := NewAdminStatus(
		runtime.Status{
			SourceKind:           runtime.SourceKind(invented),
			SourceHealth:         runtime.Health(invented),
			UpstreamHealth:       runtime.Health(invented),
			AcquisitionHealth:    runtime.Health(invented),
			Freshness:            runtime.Freshness(invented),
			ChannelFreshness:     runtime.Freshness(invented),
			SourceCheckFreshness: runtime.Freshness(invented),
			AcquisitionFreshness: runtime.Freshness(invented),
			FallbackReason:       invented,
			CatalogAge:           -time.Hour,
		},
		RouteValidation{State: RouteValidationUnknown},
		false,
		Counts{},
		SnapshotMetadata{},
		time.Time{},
		nil,
	)

	assert.Equal(t, "unknown", report.Runtime.SourceKind)
	assert.Equal(t, "unknown", report.Runtime.FallbackReason)
	assert.Equal(t, "unknown", report.SourceHealth)
	assert.Equal(t, "unknown", report.UpstreamHealth)
	assert.Equal(t, "unknown", report.Acquisition.Health)
	assert.Equal(t, "unknown", report.Freshness.Catalog)
	assert.Equal(t, "unknown", report.Freshness.Channel)
	assert.Equal(t, "unknown", report.Freshness.SourceCheck)
	assert.Equal(t, "unknown", report.Acquisition.Freshness)
	assert.Equal(
		t, int64(0), report.Freshness.CatalogAgeSeconds,
		"a clock that runs backward is not an age",
	)

	summary := report.Summary()
	assert.Equal(t, "unknown", summary.SourceKind)
	assert.Equal(t, "unknown", summary.Freshness)
}

// TestValidationRecordReportsFourDistinctStates walks the record through what
// one instance does with a candidate. The accepted head does not move when a
// candidate is refused, so the record names both at once.
func TestValidationRecordReportsFourDistinctStates(t *testing.T) {
	at := time.Date(2026, 9, 1, 13, 0, 0, 0, time.UTC)
	record := validationRecord{now: func() time.Time { return at }}

	first := Candidate{State: starmap.CatalogState{GenerationID: "gen-1"}, Epoch: 3}
	second := Candidate{State: starmap.CatalogState{GenerationID: "gen-2"}, Epoch: 3}

	assert.Equal(t, RouteValidationUnknown, record.snapshot().State)

	record.observe(first)
	pending := record.snapshot()
	assert.Equal(t, RouteValidationPending, pending.State)
	assert.Equal(t, "gen-1", pending.Candidate.GenerationID)
	assert.Empty(t, pending.Accepted.GenerationID)

	record.accept(first)
	accepted := record.snapshot()
	assert.Equal(t, RouteValidationAccepted, accepted.State)
	assert.Equal(t, "gen-1", accepted.Accepted.GenerationID)
	assert.Equal(t, uint64(3), accepted.Accepted.LeaseEpoch)

	// A repeated publication of the accepted head is not pending work.
	record.observe(first)
	assert.Equal(t, RouteValidationAccepted, record.snapshot().State)

	record.observe(second)
	assert.Equal(t, RouteValidationPending, record.snapshot().State)

	record.reject(second, errors.New("s3://private-bucket refused"))
	rejected := record.snapshot()
	assert.Equal(t, RouteValidationRejected, rejected.State)
	assert.Equal(t, "gen-2", rejected.Rejected.Generation.GenerationID)
	assert.Equal(t, ReasonInternalError, rejected.Rejected.Reason)
	assert.Equal(t, at, rejected.Rejected.At)
	assert.Equal(
		t, "gen-1", rejected.Accepted.GenerationID,
		"the accepted head still routes after a refusal",
	)
	assert.NotContains(t, string(rejected.Rejected.Reason), "private-bucket")

	// A candidate with no generation identifier changes nothing.
	record.observe(Candidate{})
	assert.Equal(t, RouteValidationRejected, record.snapshot().State)
}
