package proxy

import (
	"context"

	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/router"
)

// A video job takes three provider calls, and the provider's own job
// identifier is the argument to two of them. That identifier stops here: the
// record store holds it, this package spends it on one request, and no type
// above this seam names it.

// VideoSubmitRequest is one gateway video generation submission.
type VideoSubmitRequest = MediaRequest[inference.VideoJobRequest]

// VideoJobReference names one job a provider already accepted. It is the
// router type rather than a copy of it, because a copy would be one more place
// a provider job identifier has to be carried by hand.
type VideoJobReference = router.VideoJobReference

// VideoJobRequest is one gateway poll or cancel of an accepted job.
type VideoJobRequest = MediaRequest[VideoJobReference]

// VideoJobAnswer is what a provider reported about one job, with the route
// evidence that names who reported it.
type VideoJobAnswer struct {
	ProviderJobID string
	State         jobs.JobState
	Reason        string
	ModelUsed     string
	ProviderUsed  string
}

// SubmitVideoJob starts one video generation.
func (p *proxy) SubmitVideoJob(ctx context.Context, req *VideoSubmitRequest) (*VideoJobAnswer, error) {
	if err := ValidateVideoJobRequest(req); err != nil {
		return nil, err
	}
	return videoJobAnswer(processMedia(ctx, req, req.Request.Model, p.router.RouteVideoSubmit))
}

// PollVideoJob asks the provider that accepted a job where it got to.
func (p *proxy) PollVideoJob(ctx context.Context, req *VideoJobRequest) (*VideoJobAnswer, error) {
	if err := ValidateVideoJobReference(req); err != nil {
		return nil, err
	}
	return videoJobAnswer(processMedia(ctx, req, req.Request.Model, p.router.RouteVideoPoll))
}

// CancelVideoJob asks the provider that accepted a job to stop it.
func (p *proxy) CancelVideoJob(ctx context.Context, req *VideoJobRequest) (*VideoJobAnswer, error) {
	if err := ValidateVideoJobReference(req); err != nil {
		return nil, err
	}
	return videoJobAnswer(processMedia(ctx, req, req.Request.Model, p.router.RouteVideoCancel))
}

// VideoJobRunner hands the record store the provider side of one caller's
// jobs. The controller asks the gateway for it rather than assembling one, so
// nothing outside this package names a transport, a credential policy, or the
// caching wrapper the deployment happens to have put in front of the gateway.
func (p *proxy) VideoJobRunner(req *VideoSubmitRequest) jobs.Runner {
	return videoJobRunner(p, req)
}

// videoJobAnswer flattens the routed media answer into the shape the record's
// provider side reads.
func videoJobAnswer(
	result *MediaResponse[connectors.ProviderJob],
	err error,
) (*VideoJobAnswer, error) {
	if err != nil {
		return nil, err
	}
	return &VideoJobAnswer{
		ProviderJobID: result.Response.ID,
		State:         result.Response.State,
		Reason:        result.Response.Reason,
		ModelUsed:     result.ModelUsed,
		ProviderUsed:  result.ProviderUsed,
	}, nil
}

// VideoJobRunner is the provider side of one caller's video jobs.
//
// It exists so internal/jobs can start, poll, and stop work without importing a
// provider transport or a credential policy. The record hands it a handle for
// one call, it spends the handle on one request, and it answers in the
// vocabulary the record keeps.
type VideoJobRunner struct {
	service Proxy
	caller  VideoSubmitRequest
}

// videoJobRunner binds the gateway to one caller. request carries the gateway
// identity every call reads, and the submission the first call sends. A poll or
// a cancel passes a request whose submission is empty.
//
// It answers the interface rather than the concrete value so that an absent
// argument reads as an absent runner. A typed nil handed to internal/jobs would
// pass the check that exists to catch exactly this and fail on the first call.
func videoJobRunner(service Proxy, request *VideoSubmitRequest) jobs.Runner {
	if service == nil || request == nil {
		return nil
	}
	return &VideoJobRunner{service: service, caller: *request}
}

// Submit starts the work this runner was built for.
func (r *VideoJobRunner) Submit(ctx context.Context) (jobs.Acceptance, error) {
	request := r.caller
	answer, err := r.service.SubmitVideoJob(ctx, &request)
	if err != nil {
		return jobs.Acceptance{}, err
	}
	return jobs.Acceptance{
		Provider:      answer.ProviderUsed,
		Model:         r.caller.Request.Model,
		ProviderJobID: answer.ProviderJobID,
		State:         answer.State,
		Reason:        answer.Reason,
	}, nil
}

// Poll reports where the accepted job got to.
func (r *VideoJobRunner) Poll(ctx context.Context, handle jobs.Handle) (jobs.Report, error) {
	return r.report(ctx, handle, r.service.PollVideoJob)
}

// Cancel asks the provider to stop the accepted job.
func (r *VideoJobRunner) Cancel(ctx context.Context, handle jobs.Handle) (jobs.Report, error) {
	return r.report(ctx, handle, r.service.CancelVideoJob)
}

func (r *VideoJobRunner) report(
	ctx context.Context,
	handle jobs.Handle,
	call func(context.Context, *VideoJobRequest) (*VideoJobAnswer, error),
) (jobs.Report, error) {
	request := VideoJobRequest{
		Request: VideoJobReference{
			Provider:      handle.Provider,
			Model:         handle.Model,
			ProviderJobID: handle.ProviderJobID,
		},
		APIKey:       r.caller.APIKey,
		TenantID:     r.caller.TenantID,
		KeyID:        r.caller.KeyID,
		APIKeyConfig: r.caller.APIKeyConfig,
		RequestID:    r.caller.RequestID,
		Protocol:     r.caller.Protocol,
	}
	answer, err := call(ctx, &request)
	if err != nil {
		return jobs.Report{}, err
	}
	return jobs.Report{State: answer.State, Reason: answer.Reason}, nil
}
