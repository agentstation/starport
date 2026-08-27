package inference

// A video generation outlives the request that starts it, so it carries two
// canonical shapes rather than one. VideoJobRequest is what a caller submits.
// VideoJob is what the same caller reads back, once or many times, from an
// identifier Starport issued.
//
// Neither shape names a provider job. The record in internal/jobs holds that
// identifier and never hands it out, and a canonical type that carried it
// would put it one encoder away from a response body.

// VideoJobRequest is one canonical request to generate a video.
type VideoJobRequest struct {
	// Model is the catalog model identifier the caller asked for.
	Model string
	// Prompt describes the video to generate.
	Prompt string
	// NegativePrompt describes what to keep out of the result.
	NegativePrompt string
	// Size is the requested frame size, such as "1280x720".
	Size string
	// Seconds is the requested duration. It is a string because the two
	// published surfaces both send it as one, and a number would force a
	// guess at the unit.
	Seconds string
	// Seed makes a generation repeatable. It is a pointer because zero is a
	// seed a caller may ask for.
	Seed *int64
}

// Clone returns a copy that shares nothing with the original.
func (r VideoJobRequest) Clone() VideoJobRequest {
	copied := r
	if r.Seed != nil {
		seed := *r.Seed
		copied.Seed = &seed
	}
	return copied
}

// VideoJob is the canonical answer a caller reads about one job it submitted.
type VideoJob struct {
	// ID is the Starport job identifier, and the only identifier a caller ever
	// sees for this work.
	ID string
	// Model is the catalog model identifier the job runs.
	Model string
	// Provider names who is running the work. A chat answer already reports
	// this, so a job answer reports it too.
	Provider string
	// State is the canonical job state word.
	State string
	// Reason states why a failed job produced no asset. It is empty for every
	// other state.
	Reason string
	// CreatedUnix is when Starport recorded the job.
	CreatedUnix int64
	// CompletedUnix is when the job reached a terminal state, or zero while it
	// has not.
	CompletedUnix int64
}

// Clone returns a copy that shares nothing with the original.
func (j VideoJob) Clone() VideoJob { return j }
