package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/audit"
	"github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/localauth"
)

// fakeCatalogOperations answers the controller with canned values and counts
// what the controller asked for.
type fakeCatalogOperations struct {
	summary    catalog.Summary
	summaryErr error
	status     catalog.AdminStatus
	statusErr  error
	diff       catalog.Diff
	diffErr    error

	accepted   catalog.Operation
	joined     bool
	startErr   error
	startCalls int

	operation    catalog.Operation
	operationErr error

	canceled    catalog.Operation
	cancelErr   error
	cancelCalls int
}

func (f *fakeCatalogOperations) CatalogSummary(context.Context) (catalog.Summary, error) {
	return f.summary, f.summaryErr
}

func (f *fakeCatalogOperations) CatalogChanges(context.Context) (catalog.Diff, error) {
	return f.diff, f.diffErr
}

func (f *fakeCatalogOperations) CatalogStatus(context.Context) (catalog.AdminStatus, error) {
	return f.status, f.statusErr
}

func (f *fakeCatalogOperations) StartCatalogRefresh(
	context.Context,
) (catalog.Operation, bool, error) {
	f.startCalls++
	return f.accepted, f.joined, f.startErr
}

func (f *fakeCatalogOperations) CatalogOperation(
	context.Context,
	string,
) (catalog.Operation, error) {
	return f.operation, f.operationErr
}

func (f *fakeCatalogOperations) CancelCatalogOperation(
	context.Context,
	string,
) (catalog.Operation, error) {
	f.cancelCalls++
	return f.canceled, f.cancelErr
}

// runningOperation is the operation a started refresh answers with.
func runningOperation(id string) catalog.Operation {
	return catalog.Operation{
		ID:         id,
		Kind:       catalog.KindCatalogUpdate,
		State:      catalog.OperationAccepted,
		AcceptedAt: time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC),
	}
}

// TestAdminRefreshReturnsAcceptedOperation is the CAT-V44 acceptance walk. The
// admin refresh accepts the work and answers 202 with the operation that
// carries it. A second request that overlaps the first joins one run. The run
// route reads that operation and the cancel route ends it. Every mutation
// leaves an audit record that names the actor.
func TestAdminRefreshReturnsAcceptedOperation(t *testing.T) {
	t.Run("the refresh answers 202 with the operation identifier", func(t *testing.T) {
		operations := &fakeCatalogOperations{accepted: runningOperation("run-1")}
		controller := NewCatalogController(operations)
		trail := &recordingAuditRecorder{}
		controller.audit = trail

		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/catalog/refresh", nil)
		controller.Refresh(recorder, consoleContext(request, localauth.GrantLocalToken, ""))

		require.Equal(t, http.StatusAccepted, recorder.Code, recorder.Body.String())
		assert.Equal(t, 1, operations.startCalls)
		assert.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
		assert.Equal(
			t,
			"/api/v1/admin/catalog/refreshes/run-1",
			recorder.Header().Get("Location"),
		)

		var body acceptedOperationResponse
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
		assert.Equal(t, "run-1", body.ID)
		assert.Equal(t, catalog.KindCatalogUpdate, body.Kind)
		assert.Equal(t, catalog.OperationAccepted, body.State)
		assert.False(t, body.Joined)

		require.Len(t, trail.records, 1)
		assert.Equal(t, "catalog.refresh", trail.records[0].Action)
		assert.Equal(t, "run-1", trail.records[0].Subject)
		assert.Equal(t, audit.OutcomeOK, trail.records[0].Outcome)
		assert.Equal(
			t,
			audit.ActorConsolePrefix+string(localauth.GrantLocalToken),
			trail.records[0].Actor,
		)
	})

	t.Run("an overlapping refresh joins the run in flight", func(t *testing.T) {
		operations := &fakeCatalogOperations{
			accepted: runningOperation("run-1"),
			joined:   true,
		}
		controller := NewCatalogController(operations)

		recorder := httptest.NewRecorder()
		controller.Refresh(
			recorder,
			httptest.NewRequest(http.MethodPost, "/api/v1/admin/catalog/refresh", nil),
		)

		require.Equal(t, http.StatusAccepted, recorder.Code, recorder.Body.String())
		var body acceptedOperationResponse
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
		assert.Equal(t, "run-1", body.ID)
		assert.True(t, body.Joined, "the second request must name the run in flight")
	})

	t.Run("the run route reports the operation", func(t *testing.T) {
		operations := &fakeCatalogOperations{operation: catalog.Operation{
			ID:           "run-1",
			Kind:         catalog.KindCatalogUpdate,
			State:        catalog.OperationSucceeded,
			GenerationID: "gen-2",
			Changed:      true,
		}}
		router := chi.NewRouter()
		router.Get("/refreshes/{run_id}", NewCatalogController(operations).RefreshStatus)

		recorder := httptest.NewRecorder()
		router.ServeHTTP(
			recorder,
			httptest.NewRequest(http.MethodGet, "/refreshes/run-1", nil),
		)

		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		var operation catalog.Operation
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &operation))
		assert.Equal(t, "run-1", operation.ID)
		assert.Equal(t, catalog.OperationSucceeded, operation.State)
		assert.Equal(t, "gen-2", operation.GenerationID)
	})

	t.Run("the cancel route ends the run and records the actor", func(t *testing.T) {
		operations := &fakeCatalogOperations{canceled: catalog.Operation{
			ID:     "run-1",
			Kind:   catalog.KindCatalogUpdate,
			State:  catalog.OperationCanceled,
			Reason: catalog.ReasonCanceled,
		}}
		controller := NewCatalogController(operations)
		trail := &recordingAuditRecorder{}
		controller.audit = trail
		router := chi.NewRouter()
		router.Delete("/refreshes/{run_id}", controller.CancelRefresh)

		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodDelete, "/refreshes/run-1", nil)
		router.ServeHTTP(recorder, consoleContext(request, localauth.GrantLocalToken, ""))

		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		assert.Equal(t, 1, operations.cancelCalls)
		var operation catalog.Operation
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &operation))
		assert.Equal(t, catalog.OperationCanceled, operation.State)
		assert.Equal(t, catalog.ReasonCanceled, operation.Reason)

		require.Len(t, trail.records, 1)
		assert.Equal(t, "catalog.refresh.cancel", trail.records[0].Action)
		assert.Equal(t, "run-1", trail.records[0].Subject)
	})

	t.Run("an unknown run identifier answers 404", func(t *testing.T) {
		operations := &fakeCatalogOperations{operationErr: catalog.ErrOperationNotFound}
		router := chi.NewRouter()
		router.Get("/refreshes/{run_id}", NewCatalogController(operations).RefreshStatus)

		recorder := httptest.NewRecorder()
		router.ServeHTTP(
			recorder,
			httptest.NewRequest(http.MethodGet, "/refreshes/missing", nil),
		)

		require.Equal(t, http.StatusNotFound, recorder.Code)
	})

	t.Run("a gateway with no catalog operations degrades loudly", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		NewCatalogController(nil).Refresh(
			recorder,
			httptest.NewRequest(http.MethodPost, "/api/v1/admin/catalog/refresh", nil),
		)
		require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	})
}

// TestCatalogReadRoutesServeSummaryAndChanges proves the two read routes and
// the sanitized answer the summary gives when no catalog is present.
func TestCatalogReadRoutesServeSummaryAndChanges(t *testing.T) {
	tests := []struct {
		name       string
		operations *fakeCatalogOperations
		route      func(*CatalogController) http.HandlerFunc
		want       int
		assert     func(*testing.T, *httptest.ResponseRecorder)
	}{
		{
			name: "the summary serves the allowlisted view",
			operations: &fakeCatalogOperations{summary: catalog.Summary{
				GenerationID: "gen-2",
				Usable:       true,
				Providers:    3,
				Models:       9,
			}},
			route: func(c *CatalogController) http.HandlerFunc { return c.Summary },
			want:  http.StatusOK,
			assert: func(t *testing.T, recorder *httptest.ResponseRecorder) {
				t.Helper()
				var summary catalog.Summary
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &summary))
				assert.Equal(t, "gen-2", summary.GenerationID)
				assert.True(t, summary.Usable)
				assert.Equal(t, 3, summary.Providers)
			},
		},
		{
			name:       "a summary failure answers a sanitized 503",
			operations: &fakeCatalogOperations{summaryErr: catalog.ErrRouteValidationFailed},
			route:      func(c *CatalogController) http.HandlerFunc { return c.Summary },
			want:       http.StatusServiceUnavailable,
			assert: func(t *testing.T, recorder *httptest.ResponseRecorder) {
				t.Helper()
				assert.Equal(t, "30", recorder.Header().Get("Retry-After"))
				assert.Contains(t, recorder.Body.String(), catalogUnavailableMessage)
				assert.NotContains(t, recorder.Body.String(), "route validation")
			},
		},
		{
			name: "the changes route diffs the two newest generations",
			operations: &fakeCatalogOperations{diff: catalog.Diff{
				Available:        true,
				FromGenerationID: "gen-1",
				ToGenerationID:   "gen-2",
			}},
			route: func(c *CatalogController) http.HandlerFunc { return c.Changes },
			want:  http.StatusOK,
			assert: func(t *testing.T, recorder *httptest.ResponseRecorder) {
				t.Helper()
				var diff catalog.Diff
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &diff))
				assert.True(t, diff.Available)
				assert.Equal(t, "gen-1", diff.FromGenerationID)
			},
		},
		{
			name: "the admin status serves the operator view",
			operations: &fakeCatalogOperations{status: catalog.AdminStatus{
				RouteValidation: catalog.RouteValidation{
					State: catalog.RouteValidationAccepted,
				},
			}},
			route: func(c *CatalogController) http.HandlerFunc { return c.Status },
			want:  http.StatusOK,
			assert: func(t *testing.T, recorder *httptest.ResponseRecorder) {
				t.Helper()
				var status catalog.AdminStatus
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &status))
				assert.Equal(
					t,
					catalog.RouteValidationAccepted,
					status.RouteValidation.State,
				)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controller := NewCatalogController(test.operations)
			recorder := httptest.NewRecorder()
			test.route(controller)(
				recorder,
				httptest.NewRequest(http.MethodGet, "/api/v1/catalog", nil),
			)
			require.Equal(t, test.want, recorder.Code, recorder.Body.String())
			assert.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
			test.assert(t, recorder)
		})
	}
}
