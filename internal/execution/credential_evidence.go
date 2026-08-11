package execution

import (
	"context"
	"sync"
)

type credentialContextKey struct{}

type credentialRecorder struct {
	mu       sync.Mutex
	evidence CredentialEvidence
}

// RecordCredential binds secret-free selection evidence to the current
// provider attempt. Calls outside an executor-owned attempt are ignored.
func RecordCredential(ctx context.Context, evidence CredentialEvidence) {
	if ctx == nil {
		return
	}
	recorder, _ := ctx.Value(credentialContextKey{}).(*credentialRecorder)
	if recorder == nil {
		return
	}
	recorder.mu.Lock()
	recorder.evidence = evidence
	recorder.mu.Unlock()
}

// RecordCredentialAccepted records that the provider accepted the selected
// material far enough to return a provider response or stream.
func RecordCredentialAccepted(ctx context.Context) {
	if ctx == nil {
		return
	}
	recorder, _ := ctx.Value(credentialContextKey{}).(*credentialRecorder)
	if recorder == nil {
		return
	}
	recorder.mu.Lock()
	recorder.evidence.Accepted = true
	recorder.mu.Unlock()
}

func (r *credentialRecorder) snapshot() CredentialEvidence {
	if r == nil {
		return CredentialEvidence{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.evidence
}
