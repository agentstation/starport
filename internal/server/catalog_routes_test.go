package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
)

// catalogOperationsStub answers the catalog routes. The summary is the admin
// status passed through the reader projection, exactly as the composition does
// it, so a route test proves the projection and not a second hand-written view.
type catalogOperationsStub struct {
	status     runtimecatalog.AdminStatus
	statusErr  error
	summaryErr error
	diff       runtimecatalog.Diff
	operation  runtimecatalog.Operation
	joined     bool
	startErr   error
}

func (s *catalogOperationsStub) CatalogSummary(
	context.Context,
) (runtimecatalog.Summary, error) {
	if s.summaryErr != nil {
		return runtimecatalog.Summary{}, s.summaryErr
	}
	return s.status.Summary(), nil
}

func (s *catalogOperationsStub) CatalogChanges(
	context.Context,
) (runtimecatalog.Diff, error) {
	return s.diff, nil
}

func (s *catalogOperationsStub) CatalogStatus(
	context.Context,
) (runtimecatalog.AdminStatus, error) {
	if s.statusErr != nil {
		return runtimecatalog.AdminStatus{}, s.statusErr
	}
	return s.status, nil
}

func (s *catalogOperationsStub) StartCatalogRefresh(
	context.Context,
) (runtimecatalog.Operation, bool, error) {
	return s.operation, s.joined, s.startErr
}

func (s *catalogOperationsStub) CatalogOperation(
	context.Context,
	string,
) (runtimecatalog.Operation, error) {
	return s.operation, nil
}

func (s *catalogOperationsStub) CancelCatalogOperation(
	context.Context,
	string,
) (runtimecatalog.Operation, error) {
	return s.operation, nil
}

// sentinel names one value that belongs to the operator alone. Each one sits
// in a different operational field of the admin status, so a reader response
// that holds any of them names which field leaked.
const (
	sentinelLease     = "instance-7.internal.example"
	sentinelIdentity  = "starmap_cascade_private_edge"
	sentinelRunID     = "run-93f1c0"
	sentinelReason    = "the manifest signature is not valid"
	sentinelChecksum  = "sha256:private-payload-checksum"
	sentinelSourceURL = "https://catalog.internal.example/generations"
)

// operatorStatus is an admin status with an operator-only value in every field
// the reader must not receive.
func operatorStatus() runtimecatalog.AdminStatus {
	generatedAt := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	return runtimecatalog.AdminStatus{
		Runtime: runtimecatalog.RuntimeReport{
			Usable:     true,
			SourceKind: "starmap",
			Lease:      sentinelLease,
			LastRunID:  sentinelRunID,
			ObservedAt: generatedAt,
		},
		RouteValidation: runtimecatalog.RouteValidation{
			State: runtimecatalog.RouteValidationAccepted,
			Accepted: runtimecatalog.GenerationRef{
				GenerationID:    "gen-2",
				PayloadChecksum: sentinelChecksum,
			},
		},
		SourceHealth:   "ok",
		UpstreamHealth: "degraded",
		Acquisition: runtimecatalog.AcquisitionReport{
			Enabled:   true,
			Health:    "degraded",
			Freshness: "warn",
		},
		Freshness: runtimecatalog.FreshnessReport{
			Catalog:           "current",
			CatalogAgeSeconds: 42,
			Channel:           "warn",
			SourceCheck:       "current",
		},
		Provenance: runtimecatalog.Provenance{
			Effective: runtimecatalog.GenerationRef{
				GenerationID:    "gen-2",
				PayloadChecksum: sentinelChecksum,
				GeneratedAt:     generatedAt,
			},
			Upstream: runtimecatalog.UpstreamProvenance{
				SourceIdentity: sentinelIdentity,
				SourceKind:     "starmap",
				Chain: []runtimecatalog.Hop{
					{Identity: sentinelIdentity, Health: "ok"},
				},
			},
		},
		Catalog: runtimecatalog.Counts{Providers: 4, Models: 12},
		Snapshot: runtimecatalog.SnapshotMetadata{
			GenerationID:       "gen-2",
			ManifestAvailable:  true,
			DegradationReasons: []string{sentinelReason},
		},
		Operations: []runtimecatalog.Operation{{
			ID:     sentinelRunID,
			Kind:   runtimecatalog.KindCatalogUpdate,
			State:  runtimecatalog.OperationSucceeded,
			Reason: runtimecatalog.ReasonNone,
		}},
	}
}

// TestSafeCatalogRouteProjectsAllowlistedSummaryOnly is the CAT-V51 reader
// half. GET /api/v1/catalog serves an allowlist: the fields it names and
// nothing else. An operator-only value in any field of the admin status does
// not reach a reader, and a field added to the admin status later does not
// reach one either, because the projection names every field it serves.
func TestSafeCatalogRouteProjectsAllowlistedSummaryOnly(t *testing.T) {
	operations := &catalogOperationsStub{status: operatorStatus()}
	server := newTestServer(
		t, &Config{MaxRequestSize: 1 << 20}, withTestCatalogOperations(operations),
	)
	secret := createServerAPIKey(t, server, "catalog-reader", []string{"models:read"})

	recorder := serveAuthorized(
		server, http.MethodGet, "/api/v1/catalog", secret, t.Context(),
	)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))

	var body map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))

	allowed := []string{
		"generation_id", "generated_at", "age_seconds", "usable", "freshness",
		"source_kind", "fallback", "providers", "models", "next_update_at",
	}
	for name := range body {
		assert.Contains(t, allowed, name, "the reader view serves an allowlist")
	}
	assert.Equal(t, "gen-2", body["generation_id"])
	assert.Equal(t, float64(42), body["age_seconds"])
	assert.Equal(t, "current", body["freshness"])
	assert.Equal(t, "starmap", body["source_kind"])
	assert.Equal(t, float64(4), body["providers"])

	for _, sentinel := range []string{
		sentinelLease, sentinelIdentity, sentinelRunID, sentinelReason,
		sentinelChecksum,
	} {
		assert.NotContains(t, recorder.Body.String(), sentinel)
	}
}

// TestSafeCatalogRouteAnswersMissingCatalogWithSanitized503 is the CAT-V51
// absent-catalog half. A gateway that holds no catalog answers one sentence
// that names no source, no address, and no failure, and it says when to ask
// again.
func TestSafeCatalogRouteAnswersMissingCatalogWithSanitized503(t *testing.T) {
	tests := []struct {
		name       string
		operations *catalogOperationsStub
		compose    bool
	}{
		{
			name: "the catalog read failed",
			operations: &catalogOperationsStub{summaryErr: errors.New(
				"read " + sentinelSourceURL + ": " + sentinelReason,
			)},
			compose: true,
		},
		{
			name:    "the gateway holds no catalog surface",
			compose: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := []testServerOption{}
			if test.compose {
				options = append(options, withTestCatalogOperations(test.operations))
			}
			server := newTestServer(t, &Config{MaxRequestSize: 1 << 20}, options...)
			secret := createServerAPIKey(
				t, server, "catalog-reader", []string{"models:read"},
			)

			recorder := serveAuthorized(
				server, http.MethodGet, "/api/v1/catalog", secret, t.Context(),
			)
			require.Equal(
				t, http.StatusServiceUnavailable, recorder.Code, recorder.Body.String(),
			)
			assertOpenRouterError(
				t, recorder, http.StatusServiceUnavailable, "server_error",
			)
			assert.Equal(t, "30", recorder.Header().Get("Retry-After"))
			assert.Contains(t, recorder.Body.String(), "The catalog is not available.")
			assert.NotContains(t, recorder.Body.String(), sentinelSourceURL)
			assert.NotContains(t, recorder.Body.String(), sentinelReason)
		})
	}
}

// TestAdminCatalogStatusRequiresAdminScope is the CAT-V51 scope half. The
// operator view sits behind the admin scope. A reader key that opens the safe
// route does not open the status route, and an unauthenticated caller opens
// neither.
func TestAdminCatalogStatusRequiresAdminScope(t *testing.T) {
	operations := &catalogOperationsStub{status: operatorStatus()}
	server := newTestServer(
		t, &Config{MaxRequestSize: 1 << 20}, withTestCatalogOperations(operations),
	)

	tests := []struct {
		name      string
		scopes    []string
		id        string
		want      int
		errorType string
	}{
		{name: "no credential", want: http.StatusUnauthorized, errorType: "authentication_error"},
		{
			name:      "a reader scope",
			scopes:    []string{"models:read"},
			id:        "catalog-reader",
			want:      http.StatusForbidden,
			errorType: "permission_error",
		},
		{
			name:   "the admin scope",
			scopes: []string{"admin"},
			id:     "catalog-admin",
			want:   http.StatusOK,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.scopes == nil {
				recorder := httptest.NewRecorder()
				server.Router().ServeHTTP(recorder, httptest.NewRequest(
					http.MethodGet, "/api/v1/admin/catalog/status", nil,
				))
				require.Equal(t, test.want, recorder.Code)
				assertOpenRouterError(t, recorder, test.want, test.errorType)
				return
			}
			secret := createServerAPIKey(t, server, test.id, test.scopes)
			recorder := serveAuthorized(
				server, http.MethodGet, "/api/v1/admin/catalog/status", secret, t.Context(),
			)
			require.Equal(t, test.want, recorder.Code, recorder.Body.String())
			if test.errorType != "" {
				assertOpenRouterError(t, recorder, test.want, test.errorType)
				return
			}
			var status runtimecatalog.AdminStatus
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &status))
			assert.Equal(t, sentinelLease, status.Runtime.Lease)
			assert.Equal(t, sentinelIdentity, status.Provenance.Upstream.SourceIdentity)
			assert.Equal(t, []string{sentinelReason}, status.Snapshot.DegradationReasons)
		})
	}
}

// TestAdminCatalogStatusReportsRouteValidationState is CAT-V62. The operator
// view reports candidate, accepted, rejected, and pending route-validation
// state as four distinct values. A refused candidate does not erase the
// accepted head: the status names both at once, so an operator reads what
// routes beside what did not become routable.
func TestAdminCatalogStatusReportsRouteValidationState(t *testing.T) {
	accepted := runtimecatalog.GenerationRef{GenerationID: "gen-2", LeaseEpoch: 4}
	candidate := runtimecatalog.GenerationRef{GenerationID: "gen-3", LeaseEpoch: 4}
	rejectedAt := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		validation runtimecatalog.RouteValidation
		state      runtimecatalog.RouteValidationState
		assert     func(*testing.T, runtimecatalog.RouteValidation)
	}{
		{
			name:       "no candidate observed yet",
			validation: runtimecatalog.RouteValidation{State: runtimecatalog.RouteValidationUnknown},
			state:      runtimecatalog.RouteValidationUnknown,
			assert: func(t *testing.T, validation runtimecatalog.RouteValidation) {
				t.Helper()
				assert.Empty(t, validation.Candidate.GenerationID)
				assert.Empty(t, validation.Accepted.GenerationID)
			},
		},
		{
			name: "a candidate waits for route validation",
			validation: runtimecatalog.RouteValidation{
				State:     runtimecatalog.RouteValidationPending,
				Candidate: candidate,
				Accepted:  accepted,
			},
			state: runtimecatalog.RouteValidationPending,
			assert: func(t *testing.T, validation runtimecatalog.RouteValidation) {
				t.Helper()
				assert.Equal(t, "gen-3", validation.Candidate.GenerationID)
				assert.Equal(t, "gen-2", validation.Accepted.GenerationID)
				assert.NotEqual(
					t, validation.Candidate.GenerationID, validation.Accepted.GenerationID,
					"a pending candidate is not the accepted head",
				)
			},
		},
		{
			name: "the candidate became the accepted head",
			validation: runtimecatalog.RouteValidation{
				State:     runtimecatalog.RouteValidationAccepted,
				Candidate: candidate,
				Accepted:  candidate,
			},
			state: runtimecatalog.RouteValidationAccepted,
			assert: func(t *testing.T, validation runtimecatalog.RouteValidation) {
				t.Helper()
				assert.Equal(
					t, validation.Candidate.GenerationID, validation.Accepted.GenerationID,
				)
				assert.Empty(t, validation.Rejected.Generation.GenerationID)
			},
		},
		{
			name: "the candidate did not become routable",
			validation: runtimecatalog.RouteValidation{
				State:     runtimecatalog.RouteValidationRejected,
				Candidate: candidate,
				Accepted:  accepted,
				Rejected: runtimecatalog.Rejection{
					Generation: candidate,
					Reason:     runtimecatalog.ReasonRouteValidationFailed,
					At:         rejectedAt,
				},
			},
			state: runtimecatalog.RouteValidationRejected,
			assert: func(t *testing.T, validation runtimecatalog.RouteValidation) {
				t.Helper()
				assert.Equal(t, "gen-3", validation.Rejected.Generation.GenerationID)
				assert.Equal(
					t,
					runtimecatalog.ReasonRouteValidationFailed,
					validation.Rejected.Reason,
				)
				assert.Equal(
					t, "gen-2", validation.Accepted.GenerationID,
					"the accepted head still routes after a refusal",
				)
			},
		},
	}

	seen := map[runtimecatalog.RouteValidationState]bool{}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status := operatorStatus()
			status.RouteValidation = test.validation
			operations := &catalogOperationsStub{status: status}
			server := newTestServer(
				t, &Config{MaxRequestSize: 1 << 20},
				withTestCatalogOperations(operations),
			)
			secret := createServerAPIKey(
				t, server, "catalog-admin", []string{"admin"},
			)

			recorder := serveAuthorized(
				server, http.MethodGet, "/api/v1/admin/catalog/status", secret, t.Context(),
			)
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

			var reported runtimecatalog.AdminStatus
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &reported))
			assert.Equal(t, test.state, reported.RouteValidation.State)
			assert.False(t, seen[test.state], "each state is a distinct value")
			seen[test.state] = true
			test.assert(t, reported.RouteValidation)

			// The reader view never carries the validation record.
			readerSecret := createServerAPIKey(
				t, server, "catalog-reader", []string{"models:read"},
			)
			reader := serveAuthorized(
				server, http.MethodGet, "/api/v1/catalog", readerSecret, t.Context(),
			)
			require.Equal(t, http.StatusOK, reader.Code, reader.Body.String())
			assert.NotContains(t, reader.Body.String(), "route_validation")
		})
	}
	assert.Len(t, seen, 4, "four distinct route-validation states")
}
