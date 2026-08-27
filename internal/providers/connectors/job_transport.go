package connectors

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/jobs"
)

var (
	// ErrJobsUnsupported reports a transport that cannot run work outliving the
	// request that started it. A descriptor that declares the video operation
	// without a JobRunner raises it at activation, and a probe on a transport
	// the route selected raises it before a submission spends a credential.
	ErrJobsUnsupported = errors.New("provider inference transport cannot run a provider job")
	// ErrUnknownProviderState reports a state word no provider vocabulary this
	// package records names. It exists so an unrecognized word stops the job
	// rather than reading as one of the states Starport knows. A word that fell
	// through to running would poll a finished job forever, and one that fell
	// through to failed would discard an asset the caller paid for.
	ErrUnknownProviderState = errors.New("provider reported an unknown job state")
)

// JobSubmission is one request to start work that outlives its request. It
// carries the fields both provider families read on a video submission and
// nothing a caller cannot state, because a field no provider reads would be a
// promise Starport does not keep.
type JobSubmission struct {
	MediaTarget
	Prompt         string `json:"prompt"`
	NegativePrompt string `json:"negative_prompt,omitempty"`
	Size           string `json:"size,omitempty"`
	Seconds        string `json:"seconds,omitempty"`
	Seed           *int64 `json:"seed,omitempty"`
}

// ProviderJobRef binds one job a provider already accepted to the route that
// polls it. The identifier is the provider's own, which is why nothing outside
// this package and the job record store holds one.
type ProviderJobRef struct {
	MediaTarget
	ProviderJobID string `json:"-"`
}

// ProviderJob is one provider answer about one job, read into the vocabulary
// Starport keeps. The state is already canonical here rather than at a later
// seam, because the provider's word is the only thing that knows which
// provider reported it.
type ProviderJob struct {
	// ID is the provider's identifier for the job.
	ID string
	// State is the canonical state the provider's word names.
	State jobs.JobState
	// Reason states why a failed job produced no asset. It is set whenever
	// State is jobs.JobStateFailed, because the record refuses a failed job
	// that states none.
	Reason string
}

// Clone returns a copy that shares nothing with the original. The shared media
// path clones every answer before it leaves the attempt budget, so a retry
// cannot hand back a value an earlier attempt still holds.
func (j ProviderJob) Clone() ProviderJob { return j }

// JobRunner is the narrow optional interface a transport implements to submit,
// poll, and cancel one provider job.
//
// Connector does not carry these methods. A chat-only transport would have to
// answer three calls it cannot perform, and a stub that reports "unsupported"
// reads the same to the compiler as a real implementation. The registry probes
// for this interface instead, so a descriptor that claims the operation without
// it fails at activation rather than once per request.
type JobRunner interface {
	SubmitJob(ctx context.Context, request *JobSubmission) (*ProviderJob, error)
	PollJob(ctx context.Context, reference *ProviderJobRef) (*ProviderJob, error)
	CancelJob(ctx context.Context, reference *ProviderJobRef) (*ProviderJob, error)
}

// JobRunnerFor returns the job transport a route selected.
func JobRunnerFor(
	connector Connector,
	endpointType catalogs.EndpointType,
) (JobRunner, bool) {
	transport, found := selectTransport(connector, endpointType)
	if !found {
		return nil, false
	}
	runner, implemented := transport.(JobRunner)
	return runner, implemented
}

// providerStateWords maps every state word AMJ0 read from the two provider
// video surfaces onto the state set this deployment keeps. Nothing else appears
// here on purpose: a word added on a guess would classify a real provider
// answer with no evidence, and the whole value of the map is that an
// unrecorded word stops the job loudly.
var providerStateWords = map[string]struct {
	state  jobs.JobState
	reason string
}{
	"queued":      {state: jobs.JobStateQueued},
	"pending":     {state: jobs.JobStateQueued},
	"in_progress": {state: jobs.JobStateRunning},
	"completed":   {state: jobs.JobStateCompleted},
	"failed":      {state: jobs.JobStateFailed},
	// A provider that expired its own job produced no asset, which is what
	// failed means here. Starport's asset expiry is a different fact and never
	// a state: a completed job stays completed after its bytes go.
	"expired":   {state: jobs.JobStateFailed, reason: "the provider expired the job before Starport collected its asset"},
	"cancelled": {state: jobs.JobStateCancelled},
}

// ProviderJobState reads one provider state word into the canonical set and
// returns the reason that word carries on its own. A caller adds the provider's
// own error text to the reason when there is one.
func ProviderJobState(word string) (jobs.JobState, string, error) {
	mapping, known := providerStateWords[strings.ToLower(strings.TrimSpace(word))]
	if !known {
		return "", "", fmt.Errorf("%w: %q", ErrUnknownProviderState, word)
	}
	return mapping.state, mapping.reason, nil
}

// ProviderStateWords lists every word the map names, so a test and an operator
// document read the same source the decoder reads.
func ProviderStateWords() []string {
	words := make([]string, 0, len(providerStateWords))
	for word := range providerStateWords {
		words = append(words, word)
	}
	sort.Strings(words)
	return words
}
