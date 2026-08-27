package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/jobs"
)

// SubmitJob starts one video generation against an OpenAI-compatible API and
// returns the identifier the provider answered with.
//
// The catalog names the collection path, so a submission posts to it directly.
// A poll and a cancel address one job under that same path, which is the shape
// all three video surfaces AMJ0 read publish.
func (c *OpenAICompatibleConnector) SubmitJob(
	ctx context.Context,
	req *JobSubmission,
	setHeaders setHeadersFunc,
	handleError handleErrorFunc,
) (*ProviderJob, error) {
	if strings.TrimSpace(req.Prompt) == "" {
		return nil, fmt.Errorf("%w: a video submission carries no prompt", ErrInvalidMediaRequest)
	}
	endpoint, err := selectedEndpoint(req.Endpoint, catalogs.EndpointTypeOpenAI)
	if err != nil {
		return nil, err
	}
	httpReq, err := jsonRequest(ctx, endpoint, req)
	if err != nil {
		return nil, err
	}
	job, err := c.readJob(ctx, httpReq, req.Credential, setHeaders, handleError)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(job.ID) == "" {
		return nil, fmt.Errorf("%w: the provider accepted the job and named none", ErrInvalidMediaRequest)
	}
	return job, nil
}

// PollJob reads the current state of one accepted job.
func (c *OpenAICompatibleConnector) PollJob(
	ctx context.Context,
	ref *ProviderJobRef,
	setHeaders setHeadersFunc,
	handleError handleErrorFunc,
) (*ProviderJob, error) {
	endpoint, err := jobEndpoint(ref)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	return c.readJob(ctx, httpReq, ref.Credential, setHeaders, handleError)
}

// CancelJob stops one accepted job.
//
// Neither provider surface AMJ0 read publishes a cancel route. Both stop a job
// by deleting it, so cancellation is a Starport idea mapped onto the delete.
// The answer is stated here rather than decoded, because a deleted job is gone
// and a provider has nothing left to report about it.
func (c *OpenAICompatibleConnector) CancelJob(
	ctx context.Context,
	ref *ProviderJobRef,
	setHeaders setHeadersFunc,
	handleError handleErrorFunc,
) (*ProviderJob, error) {
	endpoint, err := jobEndpoint(ref)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	resp, err := c.send(ctx, httpReq, ref.Credential, setHeaders, handleError)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return &ProviderJob{ID: ref.ProviderJobID, State: jobs.JobStateCancelled}, nil
}

// readJob performs one request and reads the provider's job answer into the
// canonical vocabulary. Every job method uses it, so no path can decode a state
// word its own way.
func (c *OpenAICompatibleConnector) readJob(
	ctx context.Context,
	httpReq *http.Request,
	credential credentials.Material,
	setHeaders setHeadersFunc,
	handleError handleErrorFunc,
) (*ProviderJob, error) {
	resp, err := c.send(ctx, httpReq, credential, setHeaders, handleError)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var payload providerJobPayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	state, reason, err := ProviderJobState(payload.Status)
	if err != nil {
		return nil, err
	}
	job := &ProviderJob{ID: payload.ID, State: state, Reason: reason}
	if state != jobs.JobStateFailed {
		return job, nil
	}
	if reported := jobFailureReason(payload.Error); reported != "" {
		job.Reason = reported
	}
	if job.Reason == "" {
		// The record refuses a failed job that states nothing, and a provider
		// is free to report a bare failure. Saying so is more honest than
		// inventing a cause.
		job.Reason = "the provider reported a failure and stated no reason"
	}
	return job, nil
}

// providerJobPayload is the part of a provider job answer Starport reads. The
// three surfaces AMJ0 read agree on the identifier, the status word, and an
// error, and disagree about everything else.
type providerJobPayload struct {
	ID     string          `json:"id"`
	Status string          `json:"status"`
	Error  json.RawMessage `json:"error,omitempty"`
}

// jobFailureReason reads the provider's error under either shape it takes. One
// family reports a bare string and another an object with a message, so a
// decoder that knew one shape would leave half of all failed jobs with no
// reason for the caller to read.
func jobFailureReason(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var object struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &object); err == nil {
		if message := strings.TrimSpace(object.Message); message != "" {
			return message
		}
		if code := strings.TrimSpace(object.Code); code != "" {
			return code
		}
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// jobEndpoint addresses one job under the collection path the catalog names.
func jobEndpoint(ref *ProviderJobRef) (string, error) {
	if strings.TrimSpace(ref.ProviderJobID) == "" {
		return "", fmt.Errorf("%w: no provider job was named", ErrInvalidMediaRequest)
	}
	collection, err := selectedEndpoint(ref.Endpoint, catalogs.EndpointTypeOpenAI)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(collection, "/") + "/" + url.PathEscape(ref.ProviderJobID), nil
}
