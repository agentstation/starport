package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/catalog"
)

type fakeCatalogOperations struct {
	metadata     catalog.SnapshotMetadata
	metadataErr  error
	diff         catalog.Diff
	diffErr      error
	report       catalog.RefreshReport
	refreshErr   error
	refreshCalls int
}

func (f *fakeCatalogOperations) CatalogMetadata(context.Context) (catalog.SnapshotMetadata, error) {
	return f.metadata, f.metadataErr
}

func (f *fakeCatalogOperations) CatalogChanges(context.Context) (catalog.Diff, error) {
	return f.diff, f.diffErr
}

func (f *fakeCatalogOperations) RefreshCatalog(context.Context) (catalog.RefreshReport, error) {
	f.refreshCalls++
	return f.report, f.refreshErr
}

func TestCatalogRefreshEndpointActivatesGeneration(t *testing.T) {
	generatedAt := time.Date(2026, 8, 20, 11, 0, 0, 0, time.UTC)
	operations := &fakeCatalogOperations{report: catalog.RefreshReport{
		PreviousGenerationID: "gen-1",
		GenerationID:         "gen-2",
		GeneratedAt:          generatedAt,
		Changed:              true,
	}}
	controller := NewCatalogController(operations)

	recorder := httptest.NewRecorder()
	controller.Refresh(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/admin/catalog/refresh", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, 1, operations.refreshCalls)
	assert.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	var report catalog.RefreshReport
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &report))
	assert.Equal(t, "gen-1", report.PreviousGenerationID)
	assert.Equal(t, "gen-2", report.GenerationID)
	assert.Equal(t, generatedAt, report.GeneratedAt)
	assert.True(t, report.Changed)

	// A deadline during real acquisition maps to 504, matching the provider
	// refresh contract.
	timedOut := &fakeCatalogOperations{refreshErr: context.DeadlineExceeded}
	recorder = httptest.NewRecorder()
	NewCatalogController(timedOut).Refresh(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/admin/catalog/refresh", nil))
	require.Equal(t, http.StatusGatewayTimeout, recorder.Code)

	// A gateway wired without catalog operations degrades loudly.
	recorder = httptest.NewRecorder()
	NewCatalogController(nil).Refresh(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/admin/catalog/refresh", nil))
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
}

func TestCatalogMetadataAndChangesEndpoints(t *testing.T) {
	operations := &fakeCatalogOperations{
		metadata: catalog.SnapshotMetadata{GenerationID: "gen-2", ManifestAvailable: true},
		diff:     catalog.Diff{Available: true, FromGenerationID: "gen-1", ToGenerationID: "gen-2"},
	}
	controller := NewCatalogController(operations)

	recorder := httptest.NewRecorder()
	controller.Metadata(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/catalog", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	var metadata catalog.SnapshotMetadata
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &metadata))
	assert.Equal(t, "gen-2", metadata.GenerationID)
	assert.True(t, metadata.ManifestAvailable)

	recorder = httptest.NewRecorder()
	controller.Changes(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/catalog/changes", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	var diff catalog.Diff
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &diff))
	assert.True(t, diff.Available)
	assert.Equal(t, "gen-1", diff.FromGenerationID)

	recorder = httptest.NewRecorder()
	NewCatalogController(nil).Metadata(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/catalog", nil))
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
}
