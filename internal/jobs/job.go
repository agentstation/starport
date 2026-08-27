// Package jobs owns work that outlives the request that started it.
//
// Every other Starport concept finishes inside one request. A video generation
// does not: a caller submits it, leaves, and comes back with an identifier. The
// record here is what the caller comes back to, so it holds who owns the work,
// what produced it, where it got to, and nothing a caller must not see.
//
// One transition table names every legal state change. A state write that does
// not pass through it is a bug, because the accounting rule, the retention
// rule, and the caller's answer all read off the state.
package jobs

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agentstation/starport/internal/routing"
)

var (
	// ErrInvalidJob reports a record that cannot be stored as given.
	ErrInvalidJob = errors.New("jobs: invalid job")
	// ErrIllegalTransition reports a state change the transition table refuses.
	ErrIllegalTransition = errors.New("jobs: illegal state transition")
)

// JobState is where one job sits between its submission and its end.
//
// Five states carry every word the two provider families report. OpenAI names
// four of them and no cancellation. OpenRouter names six, and its expiry maps
// onto JobStateFailed, because a provider that discarded its own asset
// produced none. Starport's own asset expiry is a different fact and never a
// state: a completed job stays completed after its bytes go.
type JobState string

const (
	// JobStateQueued marks a job a provider accepted and has not started.
	JobStateQueued JobState = "queued"
	// JobStateRunning marks a job a provider is working on.
	JobStateRunning JobState = "running"
	// JobStateCompleted marks a job that produced an asset.
	JobStateCompleted JobState = "completed"
	// JobStateFailed marks a job that ended without an asset.
	JobStateFailed JobState = "failed"
	// JobStateCancelled marks a job its owner stopped. It is separate from
	// JobStateFailed because a caller that stops its own work did not fail,
	// and the two answer the caller differently even though neither costs.
	JobStateCancelled JobState = "cancelled"
)

// JobStates lists every state in the order a job moves through them.
func JobStates() []JobState {
	return []JobState{
		JobStateQueued,
		JobStateRunning,
		JobStateCompleted,
		JobStateFailed,
		JobStateCancelled,
	}
}

// Valid reports whether the word names a state this package knows.
func (s JobState) Valid() bool {
	_, known := legalTransitions[s]
	return known
}

// Terminal reports whether the state accepts no further change. A terminal
// state is the one point at which a job draws its cost and starts its
// retention window, so nothing may move a job out of one.
func (s JobState) Terminal() bool {
	return s.Valid() && len(legalTransitions[s]) == 0
}

// legalTransitions is the one transition table. Every state this package knows
// appears as a key, so a missing key means an unknown word rather than a
// terminal state, and every terminal state maps to an empty set.
//
// JobStateQueued reaches JobStateCompleted directly because a provider may
// report a finished job on the first poll and never report a running one. A
// table that demanded JobStateRunning first would refuse a real answer.
var legalTransitions = map[JobState][]JobState{
	JobStateQueued: {
		JobStateRunning,
		JobStateCompleted,
		JobStateFailed,
		JobStateCancelled,
	},
	JobStateRunning: {
		JobStateCompleted,
		JobStateFailed,
		JobStateCancelled,
	},
	JobStateCompleted: {},
	JobStateFailed:    {},
	JobStateCancelled: {},
}

// CanTransition reports whether the table allows the change. A state never
// transitions to itself: a repeated poll that reports the same word is not a
// change, and treating it as one would rewrite the terminal time.
func CanTransition(from, to JobState) bool {
	allowed, known := legalTransitions[from]
	if !known || !to.Valid() {
		return false
	}
	for _, candidate := range allowed {
		if candidate == to {
			return true
		}
	}
	return false
}

// Job is one unit of work that outlives its request.
//
// Every field a caller may read is exported. The provider job identifier is
// not one of them. It stays unexported, so no encoder, no template, and no
// response body carries it out of this package by accident. A caller that
// learned it could poll the provider directly, outside every limit and every
// usage record Starport keeps.
type Job struct {
	ID        string
	Tenant    string
	Model     string
	Operation routing.Operation
	// Provider names who accepted the work. A poll carries an identifier only
	// the accepting provider issued, so a record that did not name the provider
	// could be polled at one that never saw the job.
	Provider string
	State    JobState
	// Reason states why a failed job produced no asset. A caller that polls a
	// failed job reads it, so JobStateFailed is the one state a record cannot
	// reach without one.
	Reason     string
	CreatedAt  time.Time
	TerminalAt time.Time

	// AssetKey names the stored bytes inside internal/blob. It is a Starport
	// storage key and never a provider link. A provider serves a finished video
	// from a URL that expires, so a caller holding a Starport identifier reads
	// the bytes from Starport or reads nothing.
	AssetKey string
	// AssetBytes is how large the stored asset is. An operator sizes storage
	// from the sum of these, and the content route states it as a length.
	AssetBytes int64
	// AssetContentType is the media type the content route serves the bytes
	// with. A provider states it once, at the fetch, and nothing later guesses.
	AssetContentType string
	// AssetExpiresAt is when the retention window ends. The record stores the
	// end rather than computing it, so a job keeps the window it was promised
	// after an operator changes the setting.
	AssetExpiresAt time.Time
	// AssetExpiredAt is when the bytes went. The record stays behind it: a
	// caller that comes back late reads a completed job whose asset expired,
	// which is a different answer from a job that produced none.
	AssetExpiredAt time.Time

	providerJobID string
}

// String renders the record for a reader without its provider identifier.
//
// Go prints unexported fields under %v, so a record with no String method
// would put the provider job identifier in the first log line that took a
// whole job. Keeping the field unexported is not enough on its own.
func (j Job) String() string {
	return fmt.Sprintf(
		"job %s tenant %s provider %s model %s operation %s state %s",
		j.ID, j.Tenant, j.Provider, j.Model, j.Operation, j.State,
	)
}

// Validate reports whether the record can be stored.
func (j Job) Validate() error {
	switch {
	case strings.TrimSpace(j.ID) == "":
		return fmt.Errorf("%w: it has no identifier", ErrInvalidJob)
	case strings.TrimSpace(j.Tenant) == "":
		return fmt.Errorf("%w: it names no tenant", ErrInvalidJob)
	case strings.TrimSpace(j.Model) == "":
		return fmt.Errorf("%w: it names no model", ErrInvalidJob)
	case strings.TrimSpace(j.Provider) == "":
		return fmt.Errorf("%w: it names no provider", ErrInvalidJob)
	case strings.TrimSpace(string(j.Operation)) == "":
		return fmt.Errorf("%w: it names no operation", ErrInvalidJob)
	case j.CreatedAt.IsZero():
		return fmt.Errorf("%w: it has no creation time", ErrInvalidJob)
	case !j.State.Valid():
		return fmt.Errorf("%w: unknown state %q", ErrInvalidJob, j.State)
	case j.State.Terminal() && j.TerminalAt.IsZero():
		return fmt.Errorf("%w: state %q records no end time", ErrInvalidJob, j.State)
	case !j.State.Terminal() && !j.TerminalAt.IsZero():
		return fmt.Errorf("%w: state %q records an end time", ErrInvalidJob, j.State)
	case j.State == JobStateFailed && strings.TrimSpace(j.Reason) == "":
		return fmt.Errorf("%w: a failed job states no reason", ErrInvalidJob)
	case j.State != JobStateFailed && strings.TrimSpace(j.Reason) != "":
		return fmt.Errorf("%w: state %q states a failure reason", ErrInvalidJob, j.State)
	case j.AssetKey != "" && j.State != JobStateCompleted:
		return fmt.Errorf("%w: state %q holds a stored asset", ErrInvalidJob, j.State)
	case j.AssetKey != "" && strings.TrimSpace(j.AssetContentType) == "":
		return fmt.Errorf("%w: the stored asset names no media type", ErrInvalidJob)
	case j.AssetKey != "" && j.AssetExpiresAt.IsZero():
		return fmt.Errorf("%w: the stored asset states no retention window", ErrInvalidJob)
	case j.AssetKey == "" && !j.AssetExpiredAt.IsZero():
		return fmt.Errorf("%w: an expiry marker names no stored asset", ErrInvalidJob)
	}
	return nil
}

// New returns a queued job. A job starts queued because a provider has already
// accepted it by the time a record exists.
func New(id, tenant, provider, model string, operation routing.Operation, now time.Time) (Job, error) {
	job := Job{
		ID:        id,
		Tenant:    tenant,
		Model:     model,
		Operation: operation,
		Provider:  provider,
		State:     JobStateQueued,
		CreatedAt: now,
	}
	if err := job.Validate(); err != nil {
		return Job{}, err
	}
	return job, nil
}

// Transition moves the job through the table and stamps the end of a terminal
// move. It is how a state changes, so a caller cannot reach a terminal state
// without recording when it arrived.
//
// It refuses JobStateFailed. That state carries a reason a caller reads, and a
// door that takes no reason would let a record reach storage without one. Fail
// is the door for it.
func (j *Job) Transition(to JobState, now time.Time) error {
	if to == JobStateFailed {
		return fmt.Errorf("%w: a failed job states its reason, so use Fail", ErrInvalidJob)
	}
	return j.transition(to, now)
}

// Fail moves the job to its terminal failed state and records why. Every path
// that ends a job without an asset passes through here: a provider rejection, a
// provider state word that names a failure, and a job that outlived its
// polling budget.
func (j *Job) Fail(reason string, now time.Time) error {
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("%w: a failed job states no reason", ErrInvalidJob)
	}
	if err := j.transition(JobStateFailed, now); err != nil {
		return err
	}
	j.Reason = reason
	return nil
}

func (j *Job) transition(to JobState, now time.Time) error {
	if !CanTransition(j.State, to) {
		return fmt.Errorf("%w: %q to %q", ErrIllegalTransition, j.State, to)
	}
	j.State = to
	if to.Terminal() {
		j.TerminalAt = now
	}
	return nil
}

// AdoptProviderJob records the identifier the provider answered with. The
// value goes in and never comes back out: this package polls on the caller's
// behalf, and the caller holds the Starport identifier alone.
func (j *Job) AdoptProviderJob(id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: the provider named no job", ErrInvalidJob)
	}
	j.providerJobID = id
	return nil
}

// HasProviderJob reports whether the provider named a job to poll. It answers
// the only question anything outside this package has to ask about the
// identifier, and it answers it without disclosing the value.
func (j Job) HasProviderJob() bool { return j.providerJobID != "" }

// StoreAsset records bytes this gateway now holds for a completed job.
//
// The window is a parameter rather than a setting this type reads, because the
// record keeps the promise it was given. An operator who shortens the window
// tomorrow shortens it for the jobs that start tomorrow.
func (j *Job) StoreAsset(key, contentType string, size int64, expiresAt time.Time) error {
	switch {
	case j.State != JobStateCompleted:
		return fmt.Errorf("%w: state %q produced no asset", ErrInvalidJob, j.State)
	case strings.TrimSpace(key) == "":
		return fmt.Errorf("%w: the stored asset has no key", ErrInvalidJob)
	case strings.TrimSpace(contentType) == "":
		return fmt.Errorf("%w: the stored asset names no media type", ErrInvalidJob)
	case size < 0:
		return fmt.Errorf("%w: the stored asset reports a negative size", ErrInvalidJob)
	case expiresAt.IsZero():
		return fmt.Errorf("%w: the stored asset states no retention window", ErrInvalidJob)
	case j.AssetKey != "":
		// A second fetch would spend the tenant's credential again and
		// overwrite bytes a caller may be reading.
		return fmt.Errorf("%w: %s already holds an asset", ErrInvalidJob, j.ID)
	}
	j.AssetKey = key
	j.AssetContentType = contentType
	j.AssetBytes = size
	j.AssetExpiresAt = expiresAt
	return nil
}

// ExpireAsset marks the stored bytes gone and keeps the record.
//
// The state does not move. A completed job stays completed after its asset
// expires, because the work happened and the tenant paid for it. Only the
// answer the content route gives changes.
func (j *Job) ExpireAsset(now time.Time) error {
	if j.AssetKey == "" {
		return fmt.Errorf("%w: %s holds no asset to expire", ErrInvalidJob, j.ID)
	}
	if now.IsZero() {
		return fmt.Errorf("%w: an expiry states no time", ErrInvalidJob)
	}
	if j.AssetExpiredAt.IsZero() {
		j.AssetExpiredAt = now
	}
	return nil
}

// HasAsset reports whether this gateway holds readable bytes for the job.
func (j Job) HasAsset() bool {
	return j.AssetKey != "" && j.AssetExpiredAt.IsZero()
}

// AssetExpired reports whether the stored asset has passed its window. It reads
// true both for an asset already swept away and for one whose window ended and
// whose sweep has not yet run, so a read never serves bytes past the window the
// caller was promised.
func (j Job) AssetExpired(now time.Time) bool {
	if j.AssetKey == "" {
		return false
	}
	if !j.AssetExpiredAt.IsZero() {
		return true
	}
	return !j.AssetExpiresAt.IsZero() && !now.Before(j.AssetExpiresAt)
}
