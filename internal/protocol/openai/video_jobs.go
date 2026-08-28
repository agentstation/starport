package openai

import (
	"io"

	"github.com/agentstation/starport/internal/inference"
)

// A video job is the one answer a caller reads more than once, so the wire
// words matter more here than anywhere else in the media surface. A client
// written against OpenAI switches on the status word, and a word this family
// does not publish would leave that switch on its default branch forever.

// VideoJobRequest is the OpenAI video wire request.
type VideoJobRequest struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	NegativePrompt string `json:"negative_prompt,omitempty"`
	Size           string `json:"size,omitempty"`
	Seconds        string `json:"seconds,omitempty"`
	Seed           *int64 `json:"seed,omitempty"`
}

// DecodeVideoJob decodes one strict OpenAI video generation request.
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

// VideoJob is the OpenAI video wire object.
type VideoJob struct {
	ID          string         `json:"id"`
	Object      string         `json:"object"`
	Model       string         `json:"model"`
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

// VideoJobList is the OpenAI listing of one caller's video jobs.
type VideoJobList struct {
	Object string     `json:"object"`
	Data   []VideoJob `json:"data"`
}

// EncodeVideoJob converts one canonical job to OpenAI wire values.
func EncodeVideoJob(job inference.VideoJob) VideoJob {
	wire := VideoJob{
		ID:          job.ID,
		Object:      "video",
		Model:       job.Model,
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

// EncodeVideoJobs converts one listing to OpenAI wire values.
func EncodeVideoJobs(records []inference.VideoJob) VideoJobList {
	data := make([]VideoJob, len(records))
	for index, job := range records {
		data[index] = EncodeVideoJob(job)
	}
	return VideoJobList{Object: ListObject, Data: data}
}

// videoJobStatus renders the canonical state as the word this family
// publishes. OpenAI names a working job in_progress, and it names no
// cancellation at all, so a cancelled job keeps the canonical word rather than
// borrowing one that means something else.
func videoJobStatus(state string) string {
	if state == "running" {
		return "in_progress"
	}
	return state
}
