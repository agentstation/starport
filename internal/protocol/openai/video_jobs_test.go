package openai

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/inference"
)

// A video job is the one answer a caller reads more than once. An SDK written
// against the OpenAI API switches on the status word on every poll, so the word
// this codec publishes is the contract rather than a rendering choice.

// TestEncodeVideoJobHoldsTheOpenAIShape pins the object a caller polls. The
// OpenAI API names the type `video`, reports progress through `status`, and
// carries no provider field at all.
func TestEncodeVideoJobHoldsTheOpenAIShape(t *testing.T) {
	encoded, err := json.Marshal(EncodeVideoJob(inference.VideoJob{
		ID:          "job-9f2c",
		Model:       "openai/sora-2",
		Provider:    "openai",
		State:       "running",
		CreatedUnix: 1767225600,
	}))
	require.NoError(t, err)

	var wire map[string]any
	require.NoError(t, json.Unmarshal(encoded, &wire))
	require.Equal(t, "job-9f2c", wire["id"])
	require.Equal(t, "video", wire["object"])
	require.Equal(t, "openai/sora-2", wire["model"])
	require.Equal(t, float64(1767225600), wire["created_at"])
	// The canonical word is "running". This family publishes "in_progress", and
	// a client switching on the OpenAI vocabulary would read the canonical word
	// as an unknown state forever.
	require.Equal(t, "in_progress", wire["status"])
	// The serving provider is a fact the OpenAI object does not carry, and a
	// strict client rejects a field its schema does not name.
	require.NotContains(t, wire, "provider")
	// An unfinished job states no end time and no error.
	require.NotContains(t, wire, "completed_at")
	require.NotContains(t, wire, "error")
}

// TestEncodeVideoJobStatesWhyAJobFailed covers the one state a caller cannot
// act on without a reason. The reason travels in `error.message`, which is
// where an OpenAI client reads a failure.
func TestEncodeVideoJobStatesWhyAJobFailed(t *testing.T) {
	wire := EncodeVideoJob(inference.VideoJob{
		ID:            "job-9f2c",
		Model:         "openai/sora-2",
		State:         "failed",
		Reason:        "the prompt was refused by the safety filter",
		CreatedUnix:   1767225600,
		CompletedUnix: 1767225700,
	})
	require.Equal(t, "failed", wire.Status)
	require.Equal(t, int64(1767225700), wire.CompletedAt)
	require.NotNil(t, wire.Error)
	require.Equal(t, "the prompt was refused by the safety filter", wire.Error.Message)
}

// TestEncodeVideoJobKeepsTheCancelledWord covers the state OpenAI does not
// publish. A cancelled job keeps the canonical word rather than borrowing
// "failed", which means something the caller did not do.
func TestEncodeVideoJobKeepsTheCancelledWord(t *testing.T) {
	wire := EncodeVideoJob(inference.VideoJob{
		ID: "job-9f2c", Model: "openai/sora-2", State: "cancelled",
		CreatedUnix: 1767225600, CompletedUnix: 1767225620,
	})
	require.Equal(t, "cancelled", wire.Status)
	require.Nil(t, wire.Error)
}

// TestEncodeVideoJobsHoldsTheListShape pins the listing. The OpenAI API wraps
// every listing in an object named `list` with the records under `data`.
func TestEncodeVideoJobsHoldsTheListShape(t *testing.T) {
	encoded, err := json.Marshal(EncodeVideoJobs([]inference.VideoJob{
		{ID: "job-1", Model: "openai/sora-2", State: "queued", CreatedUnix: 1767225600},
	}))
	require.NoError(t, err)

	var wire struct {
		Object string           `json:"object"`
		Data   []map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(encoded, &wire))
	require.Equal(t, "list", wire.Object)
	require.Len(t, wire.Data, 1)
	require.Equal(t, "queued", wire.Data[0]["status"])
}

// TestDecodeVideoJobReadsTheSubmission covers the request side. The seed is a
// pointer because zero is a seed a caller may ask for, and a value type would
// read an absent seed and a zero seed the same way.
func TestDecodeVideoJobReadsTheSubmission(t *testing.T) {
	request, err := DecodeVideoJob(strings.NewReader(
		`{"model":"openai/sora-2","prompt":"a kite over a harbour","size":"1280x720","seconds":"8","seed":0}`))
	require.NoError(t, err)
	require.Equal(t, "openai/sora-2", request.Model)
	require.Equal(t, "a kite over a harbour", request.Prompt)
	require.Equal(t, "1280x720", request.Size)
	require.Equal(t, "8", request.Seconds)
	require.NotNil(t, request.Seed)
	require.Equal(t, int64(0), *request.Seed)

	absent, err := DecodeVideoJob(strings.NewReader(`{"model":"openai/sora-2","prompt":"a kite"}`))
	require.NoError(t, err)
	require.Nil(t, absent.Seed)
}

// TestDecodeVideoJobRefusesAnUnknownField keeps the submission strict. A field
// this gateway silently dropped would look accepted to the caller and never
// reach the provider.
func TestDecodeVideoJobRefusesAnUnknownField(t *testing.T) {
	_, err := DecodeVideoJob(strings.NewReader(
		`{"model":"openai/sora-2","prompt":"a kite","resolution":"4k"}`))
	require.Error(t, err)
}
