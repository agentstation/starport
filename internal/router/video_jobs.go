package router

import (
	"context"
	"strconv"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits/admission"
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
	if req.JobSubmission == nil {
		return nil, jobs.ErrSubmissionRecorderRequired
	}
	billing := &videoSubmissionBilling{}
	call := providerCall[*connectors.JobSubmission, *connectors.ProviderJob, connectors.ProviderJob]{
		prepare: billing.prepare, charge: billing.charge,
		transport: jobSubmitTransport,
		build: func() *connectors.JobSubmission {
			var bound int64
			if recorder, ok := req.JobSubmission.(jobs.NativeSubmissionRecorder); ok {
				bound = recorder.AssetBound()
			}
			return &connectors.JobSubmission{
				MaxBytes:       bound,
				Prompt:         req.Request.Prompt,
				NegativePrompt: req.Request.NegativePrompt,
				Size:           req.Request.Size,
				Seconds:        req.Request.Seconds,
				Seed:           req.Request.Seed,
			}
		},
		convert: providerJobAnswer,
		beforeDispatch: func(ctx context.Context, route routing.Route, ticket admission.Ticket) error {
			if billing.native {
				if _, ok := req.JobSubmission.(jobs.NativeSubmissionRecorder); !ok {
					return jobs.ErrSubmissionRecorderRequired
				}
			}
			return req.JobSubmission.BeforeDispatch(ctx, jobs.Dispatch{
				Native: billing.native, Valuation: billing.valuation,
				Provider: route.ProviderID, Model: route.ID(), CatalogGeneration: route.CatalogGenerationID, ReservationID: ticket.ID(),
			})
		},
		afterDispatch: func(ctx context.Context, route routing.Route, answer *connectors.ProviderJob, callErr error) error {
			if billing.native && answer != nil && answer.NativeResult != nil {
				native := answer.NativeResult
				if !native.State.Terminal() {
					return nil
				}
				return req.JobSubmission.Accepted(ctx, jobs.Acceptance{Provider: route.ProviderID, Model: route.ID(), NativeResult: &jobs.NativeResult{
					State: native.State, Reason: native.Reason, RequestID: native.RequestID, Measurement: billing.evidence(answer), Asset: jobs.Asset{ContentType: native.Asset.ContentType, Bytes: native.Asset.Bytes}, AssetURL: native.AssetURL,
				}})
			}
			if callErr != nil {
				return nil
			}
			if answer == nil {
				return jobs.ErrSubmissionUnconfirmed
			}
			return req.JobSubmission.Accepted(ctx, jobs.Acceptance{
				Provider: route.ProviderID, Model: route.ID(), ProviderJobID: answer.ID, State: answer.State, Reason: answer.Reason,
			})
		},
	}
	operation := routing.OperationVideosGenerations
	policy := req.policy(req.Request.Model)
	if native, ok := req.JobSubmission.(jobs.NativeSubmissionRecorder); ok {
		policy.ElapsedBudget = native.ExecutionTimeout()
	}
	return routeOperation(ctx, r, policy, operation,
		connectors.ProviderJob.Clone, call.attempt(operation))
}

func submissionFailure(err error) *failure.Failure {
	return failure.New(failure.GatewayUnavailable, "Durable job submission is unavailable.", false, failure.ProviderDetails{}, err)
}

// RouteVideoPoll asks the provider that holds the job where it got to.
func (r *modelRouter) RouteVideoPoll(
	ctx context.Context,
	req *VideoJobRequest,
) (*VideoJobResponse, error) {
	return r.routeAcceptedJob(ctx, req, billingVideoPoll, jobPollTransport)
}

// RouteVideoCancel asks the provider that holds the job to stop it.
func (r *modelRouter) RouteVideoCancel(
	ctx context.Context,
	req *VideoJobRequest,
) (*VideoJobResponse, error) {
	return r.routeAcceptedJob(ctx, req, billingVideoCancel, jobCancelTransport)
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
	policy.Purpose = billingVideoContent
	call := providerCall[*connectors.JobAssetRef, *connectors.JobAsset, connectors.JobAsset]{
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
	return routeOperation(ctx, r, policy, operation,
		connectors.JobAsset.Clone, call.attempt(operation))
}

// acceptedJobPolicy pins one plan to the provider that already holds the job.
//
// A key whose provider restriction no longer names the accepting provider
// reaches no route at all. Answering "no models available" is the same answer
// the key would get for a model it may not use.
func acceptedJobPolicy(policy operationPolicy, reference VideoJobReference) (operationPolicy, error) {
	if reference.Model == "" || reference.Provider == "" || reference.ProviderJobID == "" {
		return operationPolicy{}, ErrNoModelsAvailable
	}
	if !policy.allows(reference.Provider) {
		return operationPolicy{}, ErrNoModelsAvailable
	}
	policy.Provider = reference.Provider
	return policy, nil
}

// routeAcceptedJob runs a poll or a cancel against the one provider that
// accepted the work. The two differ only in the transport method they call.
func (r *modelRouter) routeAcceptedJob(
	ctx context.Context,
	req *VideoJobRequest,
	purpose billingPurpose,
	transport func(connectors.Connector, catalogs.EndpointType) (providerInvoke[*connectors.ProviderJobRef, *connectors.ProviderJob], bool),
) (*VideoJobResponse, error) {
	if req == nil {
		return nil, ErrNoModelsAvailable
	}
	policy, err := acceptedJobPolicy(req.policy(req.Request.Model), req.Request)
	if err != nil {
		return nil, err
	}
	policy.Purpose = purpose
	call := providerCall[*connectors.ProviderJobRef, *connectors.ProviderJob, connectors.ProviderJob]{
		transport: transport,
		build: func() *connectors.ProviderJobRef {
			return &connectors.ProviderJobRef{ProviderJobID: req.Request.ProviderJobID}
		},
		convert: providerJobAnswer,
	}
	operation := routing.OperationVideosGenerations
	return routeOperation(ctx, r, policy, operation,
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
) (providerInvoke[*connectors.JobSubmission, *connectors.ProviderJob], bool) {
	generator, native := connectors.NativeVideoGeneratorFor(connector, endpointType)
	if native {
		return func(ctx context.Context, request *connectors.JobSubmission) (*connectors.ProviderJob, error) {
			seconds, err := strconv.ParseInt(request.Seconds, 10, 64)
			if err != nil {
				return nil, err
			}
			result, err := generator.GenerateVideo(ctx, &connectors.NativeVideoRequest{MediaTarget: request.MediaTarget, Prompt: request.Prompt, NegativePrompt: request.NegativePrompt, Size: request.Size, Seconds: seconds, Seed: request.Seed, MaxBytes: request.MaxBytes})
			if result == nil {
				return nil, err
			}
			return &connectors.ProviderJob{ID: result.RequestID, State: result.State, Reason: result.Reason, NativeResult: result}, err
		}, true
	}
	runner, implemented := connectors.JobRunnerFor(connector, endpointType)
	if !implemented {
		return nil, false
	}
	return runner.SubmitJob, true
}

func jobPollTransport(
	connector connectors.Connector,
	endpointType catalogs.EndpointType,
) (providerInvoke[*connectors.ProviderJobRef, *connectors.ProviderJob], bool) {
	runner, implemented := connectors.JobRunnerFor(connector, endpointType)
	if !implemented {
		return nil, false
	}
	return runner.PollJob, true
}

func jobCancelTransport(
	connector connectors.Connector,
	endpointType catalogs.EndpointType,
) (providerInvoke[*connectors.ProviderJobRef, *connectors.ProviderJob], bool) {
	runner, implemented := connectors.JobRunnerFor(connector, endpointType)
	if !implemented {
		return nil, false
	}
	return runner.CancelJob, true
}

func jobAssetTransport(
	connector connectors.Connector,
	endpointType catalogs.EndpointType,
) (providerInvoke[*connectors.JobAssetRef, *connectors.JobAsset], bool) {
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
