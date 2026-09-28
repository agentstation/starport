package app

import (
	"encoding/json/v2"
	"os"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type budgetDispatchObservation struct {
	At         time.Time
	Operations budgetOperationSample
}

type budgetTimingSample struct {
	BeforeProviderNS int64                 `json:"client_start_to_provider_ns"`
	ResponseNS       int64                 `json:"client_response_complete_ns"`
	Admission        budgetOperationSample `json:"admission"`
	Settlement       budgetOperationSample `json:"settlement"`
}

type budgetProcessMemory struct {
	AllocatedBytes uint64 `json:"allocated_bytes"`
	Allocations    uint64 `json:"allocations"`
	HeapBefore     uint64 `json:"heap_alloc_before_bytes"`
	HeapAfter      uint64 `json:"heap_alloc_after_bytes"`
}

func budgetMeasurementCount(t *testing.T) int {
	t.Helper()
	count := 1
	if value := os.Getenv("STARPORT_BUDGET_PROFILE_SAMPLES"); value != "" {
		parsed, err := strconv.Atoi(value)
		require.NoError(t, err)
		require.GreaterOrEqual(t, parsed, 1)
		require.LessOrEqual(t, parsed, 1000)
		count = parsed
	}
	return count
}

// writeBudgetMeasurement records the complete instrumented request and its ledger footprint.
// The encoded footprint excludes backend prefixes, indexes, allocator overhead, and replication.
func writeBudgetMeasurement(t *testing.T, application *App, samples []budgetTimingSample, requests int, backgroundQueries int64, memory budgetProcessMemory) {
	t.Helper()
	path := os.Getenv("STARPORT_BUDGET_PROFILE_REPORT")
	if path == "" {
		return
	}
	bound := 4*requests + 64
	keys, err := application.store.ScanWithPrefix(t.Context(), "budget:v1:", bound)
	require.NoError(t, err)
	require.Less(t, len(keys), bound, "the ledger footprint must include every scoped key")
	var encodedBytes int64
	for _, key := range keys {
		value, err := application.store.Get(t.Context(), key)
		require.NoError(t, err)
		encodedBytes += int64(len(key) + len(value))
	}
	snapshot := application.catalog.Current()
	report := map[string]any{
		"schema_version": 1, "qualification": "UNVERIFIED", "profile": "fleet-budget-sequence-diagnostic-v1",
		"go_version": runtime.Version(), "os": runtime.GOOS, "architecture": runtime.GOARCH,
		"gomaxprocs": runtime.GOMAXPROCS(0), "concurrency": 1, "replicas": 1,
		"catalog_generation": snapshot.GenerationID(), "catalog_checksum": snapshot.PayloadChecksum(),
		"catalog_routes": len(snapshot.Routes()), "settled_attempts": requests, "meters_per_attempt": 5,
		"storage": "native Valkey and PostgreSQL", "provider": "loopback HTTP fixture",
		"load": "one warmup followed by sequential requests", "response_cache": "disabled",
		"maintenance": "App.Run loops are not started; catalog runtime background work remains active", "warm_samples": samples,
		"background_sql_queries_during_samples":    backgroundQueries,
		"whole_test_process_memory_during_samples": memory,
		"ledger_keys": len(keys), "ledger_encoded_key_value_bytes": encodedBytes,
		"limitations": "Timing and allocation counts include the client, loopback provider, and test instrumentation. The ledger size excludes backend prefixes, indexes, allocator overhead, and replication. This diagnostic does not qualify production latency, gateway allocation limits, peak process memory, or fleet capacity.",
	}
	data, err := json.Marshal(report)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, append(data, '\n'), 0600))
}
