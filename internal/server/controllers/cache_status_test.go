package controllers

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/agentstation/starport/internal/cache"
	"github.com/stretchr/testify/require"
)

func TestSystemInfoReportsOptionalCachePressure(t *testing.T) {
	expected := cache.FillStatus{Enabled: true, EntryLimit: 1024, ByteLimit: 4 << 20, WorkerLimit: 2,
		RetainedEntries: 4, RetainedBytes: 4096, ActiveFills: 2, DroppedFills: 3, FailedFills: 1, CompletedFills: 8}
	extraction := cache.FillStatus{Enabled: true, EntryLimit: 1024, ByteLimit: 4 << 20, WorkerLimit: 2, DroppedFills: 9}
	controller := NewAdminController(nil, nil, nil, WithDeployment(Deployment{ResponseCache: func() cache.FillStatus { return expected }, ExtractionCache: func() cache.FillStatus { return extraction }}))
	response := httptest.NewRecorder()
	controller.SystemInfo(response, httptest.NewRequest("GET", "/api/v1/admin/info", nil))
	require.Equal(t, 200, response.Code)
	var body struct {
		ResponseCache   cache.FillStatus `json:"response_cache"`
		ExtractionCache cache.FillStatus `json:"extraction_cache"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, expected, body.ResponseCache)
	require.Equal(t, extraction, body.ExtractionCache)
}
