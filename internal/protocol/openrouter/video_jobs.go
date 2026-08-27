package openrouter

import (
	"io"

	"github.com/agentstation/starport/internal/inference"
)

// The two families answer a video job with different words for the same two
// states. OpenRouter calls a job it has not started pending, and it names the
// provider on every answer the way its chat answer does. A client written
// against one family and served the other family's words would read a working
// job as an unknown one.

// VideoJobRequest is the OpenRouter video wire request.
type VideoJobRequest struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	NegativePrompt string `json:"negative_prompt,omitempty"`
	Size           string `json:"size,omitempty"`
	Seconds        string `json:"seconds,omitempty"`
	Seed           *int64 `json:"seed,omitempty"`
}

// DecodeVideoJob decodes one strict OpenRouter video generation request.
func DecodeVideoJob(reader io.Reader) (inference.VideoJobRequest, error) {
	var wire VideoJobRequest
	if err := decodeStrict(reader, &wire); err != nil {
		return inference.VideoJobRequest{}, err
	}
	return inference.VideoJobRequest{
		Model: wire.Model, Prompt: wire.Prompt, NegativePrompt: wire.NegativePrompt,
		Size: wire.Size, Seconds: wire.Seconds, Seed: wire.Seed,
	}, nil
}

// VideoJob is the OpenRouter video wire object.
type VideoJob struct {
	ID          string         `json:"id"`
	Model       string         `json:"model"`
	Provider    string         `json:"provider,omitempty"`
	Status      string         `json:"status"`
	CreatedAt   int64          `json:"created_at"`
	CompletedAt int64          `json:"completed_at,omitempty"`
	ExpiresAt   int64          `json:"expires_at,omitempty"`
	Error       *VideoJobError `json:"error,omitempty"`
}

// VideoJobError states why a failed job produced no video.
type VideoJobError struct {
	Message string `json:"message"`
}

// VideoJobList is the OpenRouter listing of one caller's video jobs.
type VideoJobList struct {
	Data []VideoJob `json:"data"`
}

// EncodeVideoJob converts one canonical job to OpenRouter wire values.
func EncodeVideoJob(job inference.VideoJob) VideoJob {
	wire := VideoJob{
		ID:          job.ID,
		Model:       job.Model,
		Provider:    job.Provider,
		Status:      videoJobStatus(job.State),
		CreatedAt:   job.CreatedUnix,
		CompletedAt: job.CompletedUnix,
		ExpiresAt:   job.ExpiresUnix,
	}
	if job.Reason != "" {
		wire.Error = &VideoJobError{Message: job.Reason}
	}
	return wire
}

// EncodeVideoJobs converts one listing to OpenRouter wire values.
func EncodeVideoJobs(records []inference.VideoJob) VideoJobList {
	data := make([]VideoJob, len(records))
	for index, job := range records {
		data[index] = EncodeVideoJob(job)
	}
	return VideoJobList{Data: data}
}

// videoJobStatus renders the canonical state as the word this family
// publishes.
func videoJobStatus(state string) string {
	switch state {
	case "queued":
		return "pending"
	case "running":
		return "in_progress"
	default:
		return state
	}
}
