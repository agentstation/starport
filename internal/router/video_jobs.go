package router

import (
	"context"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/routing"
)

// A video job takes three provider calls rather than one, and all three run the
// same plan the image and speech paths run. The submission picks a route the
// ordinary way. The poll and the cancel do not: they carry an identifier only
// the accepting provider issued, so they pin the plan to that provider and let
// everything else, the credential policy, the endpoint binding, and the attempt
// budget, stay exactly as it is for every other operation.

// VideoSubmitRequest routes one video generation submission.
type VideoSubmitRequest = MediaRequest[inference.VideoJobRequest]

// VideoJobReference names one job a provider already accepted.
type VideoJobReference struct {
	// Provider is who accepted the job. Planning is pinned to it.
	Provider string
	// Model is the catalog model the job runs. The plan needs it to find the
	// offering that names the endpoint.
	Model string
	// ProviderJobID is the provider's own identifier. It reaches this package
	// from the job record and travels no further than the request body.
	ProviderJobID string
}

// VideoJobRequest routes one poll or one cancel of an accepted job.
type VideoJobRequest = MediaRequest[VideoJobReference]

// VideoAssetReference names the finished output of one accepted job and states
// the bound the record store is willing to hold.
type VideoAssetReference struct {
	VideoJobReference
	// MaxBytes is the largest asset the caller will store. It travels with the
	// request rather than living here, because the half that stores the bytes
	// is the half that decides what it is willing to store.
	MaxBytes int64
}

// VideoAssetRequest routes one read of a finished job's asset.
type VideoAssetRequest = MediaRequest[VideoAssetReference]

// VideoAssetResponse is one finished asset with route evidence.
type VideoAssetResponse = MediaResponse[connectors.JobAsset]

// VideoJobResponse is one provider job answer with route evidence.
type VideoJobResponse = MediaResponse[connectors.ProviderJob]

// RouteVideoSubmit starts one video generation at a provider that serves it.
func (r *modelRouter) RouteVideoSubmit(
	ctx context.Context,
	req *VideoSubmitRequest,
) (*VideoJobResponse, error) {
	if req == nil || req.Request.Model == "" {
		return nil, ErrNoModelsAvailable
	}
	call := mediaCall[*connectors.JobSubmission, *connectors.ProviderJob, connectors.ProviderJob]{
		transport: jobSubmitTransport,
		build: func() *connectors.JobSubmission {
			return &connectors.JobSubmission{
				Prompt:         req.Request.Prompt,
				NegativePrompt: req.Request.NegativePrompt,
				Size:           req.Request.Size,
				Seconds:        req.Request.Seconds,
				Seed:           req.Request.Seed,
			}
		},
		convert: providerJobAnswer,
	}
	operation := routing.OperationVideosGenerations
	return routeMedia(ctx, r, req.policy(req.Request.Model), operation,
		connectors.ProviderJob.Clone, call.attempt(operation))
}

// RouteVideoPoll asks the provider that holds the job where it got to.
func (r *modelRouter) RouteVideoPoll(
	ctx context.Context,
	req *VideoJobRequest,
) (*VideoJobResponse, error) {
	return r.routeAcceptedJob(ctx, req, jobPollTransport)
}

// RouteVideoCancel asks the provider that holds the job to stop it.
func (r *modelRouter) RouteVideoCancel(
	ctx context.Context,
	req *VideoJobRequest,
) (*VideoJobResponse, error) {
	return r.routeAcceptedJob(ctx, req, jobCancelTransport)
}

// RouteVideoContent reads the finished asset from the provider that produced it.
//
// It pins to the accepting provider exactly as a poll does. A second provider
// would not hold the asset, so the fan-out every other media operation relies on
// has nothing to reach here.
func (r *modelRouter) RouteVideoContent(
	ctx context.Context,
	req *VideoAssetRequest,
) (*VideoAssetResponse, error) {
	if req == nil || req.Request.MaxBytes <= 0 {
		return nil, ErrNoModelsAvailable
	}
	policy, err := acceptedJobPolicy(req.policy(req.Request.Model), req.Request.VideoJobReference)
	if err != nil {
		return nil, err
	}
	reference := req.Request
	call := mediaCall[*connectors.JobAssetRef, *connectors.JobAsset, connectors.JobAsset]{
		transport: jobAssetTransport,
		build: func() *connectors.JobAssetRef {
			return &connectors.JobAssetRef{
				ProviderJobRef: connectors.ProviderJobRef{ProviderJobID: reference.ProviderJobID},
				MaxBytes:       reference.MaxBytes,
			}
		},
		convert: providerJobAsset,
	}
	operation := routing.OperationVideosGenerations
	return routeMedia(ctx, r, policy, operation,
		connectors.JobAsset.Clone, call.attempt(operation))
}

// acceptedJobPolicy pins one plan to the provider that already holds the job.
//
// A key whose provider restriction no longer names the accepting provider
// reaches no route at all. Answering "no models available" is the same answer
// the key would get for a model it may not use.
func acceptedJobPolicy(policy mediaPolicy, reference VideoJobReference) (mediaPolicy, error) {
	if reference.Model == "" || reference.Provider == "" || reference.ProviderJobID == "" {
		return mediaPolicy{}, ErrNoModelsAvailable
	}
	if !policy.allows(reference.Provider) {
		return mediaPolicy{}, ErrNoModelsAvailable
	}
	policy.Provider = reference.Provider
	return policy, nil
}

// routeAcceptedJob runs a poll or a cancel against the one provider that
// accepted the work. The two differ only in the transport method they call.
func (r *modelRouter) routeAcceptedJob(
	ctx context.Context,
	req *VideoJobRequest,
	transport func(connectors.Connector, catalogs.EndpointType) (mediaInvoke[*connectors.ProviderJobRef, *connectors.ProviderJob], bool),
) (*VideoJobResponse, error) {
	if req == nil {
		return nil, ErrNoModelsAvailable
	}
	policy, err := acceptedJobPolicy(req.policy(req.Request.Model), req.Request)
	if err != nil {
		return nil, err
	}
	call := mediaCall[*connectors.ProviderJobRef, *connectors.ProviderJob, connectors.ProviderJob]{
		transport: transport,
		build: func() *connectors.ProviderJobRef {
			return &connectors.ProviderJobRef{ProviderJobID: req.Request.ProviderJobID}
		},
		convert: providerJobAnswer,
	}
	operation := routing.OperationVideosGenerations
	return routeMedia(ctx, r, policy, operation,
		connectors.ProviderJob.Clone, call.attempt(operation))
}

// providerJobAnswer unwraps the transport answer. The transport already read
// the provider state word into the canonical set, so nothing is converted here
// beyond the pointer.
func providerJobAnswer(answer *connectors.ProviderJob) (connectors.ProviderJob, error) {
	if answer == nil {
		return connectors.ProviderJob{}, ErrNoModelsAvailable
	}
	return *answer, nil
}

func jobSubmitTransport(
	connector connectors.Connector,
	endpointType catalogs.EndpointType,
) (mediaInvoke[*connectors.JobSubmission, *connectors.ProviderJob], bool) {
	runner, implemented := connectors.JobRunnerFor(connector, endpointType)
	if !implemented {
		return nil, false
	}
	return runner.SubmitJob, true
}

func jobPollTransport(
	connector connectors.Connector,
	endpointType catalogs.EndpointType,
) (mediaInvoke[*connectors.ProviderJobRef, *connectors.ProviderJob], bool) {
	runner, implemented := connectors.JobRunnerFor(connector, endpointType)
	if !implemented {
		return nil, false
	}
	return runner.PollJob, true
}

func jobCancelTransport(
	connector connectors.Connector,
	endpointType catalogs.EndpointType,
) (mediaInvoke[*connectors.ProviderJobRef, *connectors.ProviderJob], bool) {
	runner, implemented := connectors.JobRunnerFor(connector, endpointType)
	if !implemented {
		return nil, false
	}
	return runner.CancelJob, true
}

func jobAssetTransport(
	connector connectors.Connector,
	endpointType catalogs.EndpointType,
) (mediaInvoke[*connectors.JobAssetRef, *connectors.JobAsset], bool) {
	runner, implemented := connectors.JobRunnerFor(connector, endpointType)
	if !implemented {
		return nil, false
	}
	return runner.FetchJobAsset, true
}

// providerJobAsset unwraps the transport answer.
func providerJobAsset(answer *connectors.JobAsset) (connectors.JobAsset, error) {
	if answer == nil {
		return connectors.JobAsset{}, ErrNoModelsAvailable
	}
	return *answer, nil
}
