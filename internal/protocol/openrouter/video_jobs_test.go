package openrouter

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/inference"
)

// OpenRouter answers a video job with the same two facts the OpenAI family
// reports and two differences that run through the whole family: it names the
// serving provider, and it calls a job it has not started "pending".

// TestEncodeVideoJobHoldsTheOpenRouterShape pins the object a caller polls.
func TestEncodeVideoJobHoldsTheOpenRouterShape(t *testing.T) {
	encoded, err := json.Marshal(EncodeVideoJob(inference.VideoJob{
		ID:          "job-9f2c",
		Model:       "deepinfra/wan-2.2",
		Provider:    "deepinfra",
		State:       "queued",
		CreatedUnix: 1767225600,
	}))
	require.NoError(t, err)

	var wire map[string]any
	require.NoError(t, json.Unmarshal(encoded, &wire))
	require.Equal(t, "job-9f2c", wire["id"])
	require.Equal(t, "deepinfra/wan-2.2", wire["model"])
	// Naming the serving provider is what separates every OpenRouter answer
	// from its OpenAI counterpart, and a routed job is exactly the answer a
	// caller needs it on.
	require.Equal(t, "deepinfra", wire["provider"])
	// This family has no envelope word, so an object field would be a value the
	// OpenAI family publishes and this one does not.
	require.NotContains(t, wire, "object")
	// The canonical word is "queued". This family publishes "pending".
	require.Equal(t, "pending", wire["status"])
	require.NotContains(t, wire, "completed_at")
	require.NotContains(t, wire, "error")
}

// TestEncodeVideoJobRendersTheWorkingWord covers the state both families spell
// the same way from two different canonical words.
func TestEncodeVideoJobRendersTheWorkingWord(t *testing.T) {
	wire := EncodeVideoJob(inference.VideoJob{
		ID: "job-9f2c", Model: "deepinfra/wan-2.2", Provider: "deepinfra",
		State: "running", CreatedUnix: 1767225600,
	})
	require.Equal(t, "in_progress", wire.Status)
}

// TestEncodeVideoJobStatesWhyAJobFailed pins where a caller reads a failure.
func TestEncodeVideoJobStatesWhyAJobFailed(t *testing.T) {
	wire := EncodeVideoJob(inference.VideoJob{
		ID:            "job-9f2c",
		Model:         "deepinfra/wan-2.2",
		Provider:      "deepinfra",
		State:         "failed",
		Reason:        "the provider discarded the asset before it was fetched",
		CreatedUnix:   1767225600,
		CompletedUnix: 1767225700,
	})
	require.Equal(t, "failed", wire.Status)
	require.Equal(t, int64(1767225700), wire.CompletedAt)
	require.NotNil(t, wire.Error)
	require.Equal(t, "the provider discarded the asset before it was fetched", wire.Error.Message)
}

// TestEncodeVideoJobsHoldsTheListShape pins the listing. This family carries
// the records under `data` and wraps them in no envelope word.
func TestEncodeVideoJobsHoldsTheListShape(t *testing.T) {
	encoded, err := json.Marshal(EncodeVideoJobs([]inference.VideoJob{
		{ID: "job-1", Model: "deepinfra/wan-2.2", Provider: "deepinfra",
			State: "completed", CreatedUnix: 1767225600, CompletedUnix: 1767225700},
	}))
	require.NoError(t, err)

	var wire map[string]any
	require.NoError(t, json.Unmarshal(encoded, &wire))
	require.NotContains(t, wire, "object")
	records, ok := wire["data"].([]any)
	require.True(t, ok)
	require.Len(t, records, 1)
	record, ok := records[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "completed", record["status"])
	require.Equal(t, "deepinfra", record["provider"])
}

// TestDecodeVideoJobReadsTheSubmission covers the request side. Both families
// read the same submission, which is what lets one canonical request serve a
// caller that submits through either prefix.
func TestDecodeVideoJobReadsTheSubmission(t *testing.T) {
	request, err := DecodeVideoJob(strings.NewReader(
		`{"model":"deepinfra/wan-2.2","prompt":"a kite over a harbour","seconds":"8"}`))
	require.NoError(t, err)
	require.Equal(t, "deepinfra/wan-2.2", request.Model)
	require.Equal(t, "a kite over a harbour", request.Prompt)
	require.Equal(t, "8", request.Seconds)
	require.Nil(t, request.Seed)
}

// TestDecodeVideoJobRefusesAnUnknownField keeps the submission strict.
func TestDecodeVideoJobRefusesAnUnknownField(t *testing.T) {
	_, err := DecodeVideoJob(strings.NewReader(
		`{"model":"deepinfra/wan-2.2","prompt":"a kite","aspect_ratio":"16:9"}`))
	require.Error(t, err)
}
