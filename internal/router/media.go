package router

import (
	"context"
	"errors"
	"fmt"

	"github.com/agentstation/starmap/pkg/catalogs"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/execution"
	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/routing"
)

// The three media routes reuse the chat route plan, credential policy,
// availability state, and total-attempt budget. What differs is the operation
// they plan for and the narrow transport interface they call, so only the
// provider call itself is written once per operation.

// MediaRequest carries one canonical media request plus the tenant routing and
// credential policy every operation reads.
type MediaRequest[Request any] struct {
	Request      Request
	APIKeyConfig *APIKeyConfig
	TenantID     string
}

// policy names the model the plan asks for and the key restrictions that apply
// to it. Each canonical media request carries its own Model field and no
// interface unites them, so the caller states the model and the shared path
// stays free of a constraint that would exist only to read one string.
func (r *MediaRequest[Request]) policy(model string) mediaPolicy {
	return mediaPolicy{Model: model, APIKeyConfig: r.APIKeyConfig, TenantID: r.TenantID}
}

// mediaPolicy is everything the shared media path reads that is not the
// provider call itself.
type mediaPolicy struct {
	Model        string
	APIKeyConfig *APIKeyConfig
	TenantID     string
}

// MediaResponse is one media result with the same route evidence a chat or an
// embedding answer carries.
type MediaResponse[Response any] struct {
	Response         Response
	ModelUsed        string
	ProviderUsed     string
	CredentialSource string
	Attempts         int
	Metadata         *Metadata
	CatalogSnapshot  *runtimecatalog.RoutableSnapshot
}

// ImagesRequest routes one image generation or image edit.
type ImagesRequest = MediaRequest[inference.ImagesRequest]

// ImagesResponse is one image result with route evidence.
type ImagesResponse = MediaResponse[inference.ImagesResponse]

// SpeechRequest routes one text-to-speech call.
type SpeechRequest = MediaRequest[inference.SpeechRequest]

// SpeechResponse is one speech result with route evidence.
type SpeechResponse = MediaResponse[inference.SpeechResponse]

// TranscriptionRequest routes one speech-to-text call.
type TranscriptionRequest = MediaRequest[inference.TranscriptionRequest]

// TranscriptionResponse is one transcript with route evidence.
type TranscriptionResponse = MediaResponse[inference.TranscriptionResponse]

// RouteImages executes one image generation or image edit request. The
// presence of a source image selects the operation, because the catalog names
// a separate one for an edit and a provider serves it at its own path.
func (r *modelRouter) RouteImages(ctx context.Context, req *ImagesRequest) (*ImagesResponse, error) {
	if req == nil || req.Request.Model == "" {
		return nil, ErrNoModelsAvailable
	}
	operation := routing.OperationImagesGenerations
	if req.Request.IsEdit() {
		operation = routing.OperationImagesEdits
	}
	call := mediaCall[*connectors.ImagesRequest, *connectors.ImagesResponse, inference.ImagesResponse]{
		transport: imageTransport,
		build:     func() *connectors.ImagesRequest { return connectors.ImagesRequestFromInference(req.Request) },
		convert:   connectors.ImagesResponseToInference,
	}
	return routeMedia(ctx, r, req.policy(req.Request.Model), operation,
		inference.ImagesResponse.Clone, call.attempt(operation))
}

// RouteSpeech executes one text-to-speech request.
func (r *modelRouter) RouteSpeech(ctx context.Context, req *SpeechRequest) (*SpeechResponse, error) {
	if req == nil || req.Request.Model == "" {
		return nil, ErrNoModelsAvailable
	}
	call := mediaCall[*connectors.SpeechRequest, *connectors.SpeechResponse, inference.SpeechResponse]{
		transport: speechTransport,
		build:     func() *connectors.SpeechRequest { return connectors.SpeechRequestFromInference(req.Request) },
		convert:   connectors.SpeechResponseToInference,
	}
	return routeMedia(ctx, r, req.policy(req.Request.Model), routing.OperationAudioSpeech,
		inference.SpeechResponse.Clone, call.attempt(routing.OperationAudioSpeech))
}

// RouteTranscription executes one speech-to-text request. The request states
// whether it wants the spoken language or English, and that answer selects the
// operation, because a provider exposes the two at separate paths.
func (r *modelRouter) RouteTranscription(
	ctx context.Context,
	req *TranscriptionRequest,
) (*TranscriptionResponse, error) {
	if req == nil || req.Request.Model == "" {
		return nil, ErrNoModelsAvailable
	}
	operation := routing.OperationAudioTranscriptions
	if req.Request.Translate {
		operation = routing.OperationAudioTranslations
	}
	call := mediaCall[*connectors.TranscriptionRequest, *connectors.TranscriptionResponse, inference.TranscriptionResponse]{
		transport: transcriptionTransport,
		build: func() *connectors.TranscriptionRequest {
			return connectors.TranscriptionRequestFromInference(req.Request)
		},
		convert: connectors.TranscriptionResponseToInference,
	}
	return routeMedia(ctx, r, req.policy(req.Request.Model), operation,
		inference.TranscriptionResponse.Clone, call.attempt(operation))
}

// mediaAttempt is the provider call of one media operation. Everything around
// it, the plan, the credential, the endpoint binding, and the budget, is the
// same for all three, so only this part is written per operation.
type mediaAttempt[Response any] func(
	ctx context.Context,
	connector connectors.Connector,
	route routing.Route,
	selected credentialSelection,
) (*Response, *failure.Failure, execution.AttemptAction)

// routeMedia runs one media operation through the shared route plan and
// budget. It is written once over a type parameter rather than three times,
// because three copies of a retry and credential policy drift into three
// policies.
func routeMedia[Response any](
	ctx context.Context,
	r *modelRouter,
	policy mediaPolicy,
	operation routing.Operation,
	clone func(Response) Response,
	attempt mediaAttempt[Response],
) (*MediaResponse[Response], error) {
	runtime, owned, err := r.acquireRuntime(ctx)
	if err != nil {
		return nil, ErrNoModelsAvailable
	}
	if owned {
		defer runtime.Release()
	}

	planningRequest := mediaPlanningRequest(policy, r.config.EnableCostOptimization)
	plan, err := r.planOperation(ctx, planningRequest, operation, runtime, nil)
	if err != nil {
		if errors.Is(err, routing.ErrNoCandidate) {
			return nil, ErrNoModelsAvailable
		}
		return nil, fmt.Errorf("plan %s route: %w", operation, err)
	}
	credentialPolicy, err := newCredentialPolicy(
		policy.APIKeyConfig.credentialStrategy(), policy.TenantID,
		runtime, r.storedKeys, r.credentialGate,
	)
	if err != nil {
		return nil, err
	}

	result, err := execution.Execute(ctx, r.executor, plan, func(
		attemptCtx context.Context,
		planned routing.Attempt,
	) (*Response, *failure.Failure, execution.AttemptAction) {
		connector := runtime.Get(planned.Route.ProviderID)
		if connector == nil {
			return nil, failure.New(
				failure.ProviderUnavailable,
				"No provider adapter is available.",
				true,
				failure.ProviderDetails{Provider: planned.Route.ProviderID},
				nil,
			), execution.AttemptActionDefault
		}
		selected, materialFailure, action := credentialPolicy.resolve(attemptCtx, planned.Route)
		if materialFailure != nil {
			return nil, materialFailure, action
		}
		boundRoute, bindFailure := bindSelectedEndpoint(runtime, planned.Route, selected)
		if bindFailure != nil {
			return nil, bindFailure, execution.AttemptActionStop
		}
		response, attemptFailure, action := attempt(attemptCtx, connector, boundRoute, selected)
		if attemptFailure != nil {
			if action == execution.AttemptActionDefault {
				action = credentialPolicy.afterFailure(planned.Route, attemptFailure)
			}
			return nil, attemptFailure, action
		}
		execution.RecordCredentialAccepted(attemptCtx)
		return response, nil, action
	}, clone)
	if err != nil {
		evidence := executionEvidence(err)
		return &MediaResponse[Response]{Metadata: responseMetadata(evidence, "all models failed")},
			fmt.Errorf("%w: %w", ErrAllModelsFailed, err)
	}

	return &MediaResponse[Response]{
		Response:         result.Response,
		ModelUsed:        result.Route.ID(),
		ProviderUsed:     result.Route.ProviderID,
		CredentialSource: credentialSourceUsed(result.Attempts),
		Attempts:         len(result.Attempts),
		Metadata:         responseMetadata(result.Attempts, selectionReason(result.Attempts)),
		CatalogSnapshot:  runtime.Snapshot(),
	}, nil
}

// mediaPlanningRequest builds the same planning request the embedding path
// builds. Tenant model and provider restrictions apply to a media call exactly
// as they apply to a chat call, because they are properties of the key.
func mediaPlanningRequest(policy mediaPolicy, preferLowestCost bool) routing.Request {
	request := routing.Request{
		Models: []string{policy.Model},
		Optimization: routing.OptimizationPolicy{
			PreferLowestCost:    preferLowestCost,
			PreferLowestLatency: true,
		},
	}
	if policy.APIKeyConfig != nil {
		request.Tenant = routing.TenantPolicy{
			AllowedModels:    wildcardAsUnrestricted(policy.APIKeyConfig.AllowedModels),
			AllowedProviders: normalizeProviders(policy.APIKeyConfig.AllowedProviders),
			ModelOverrides:   cloneModelOverrides(policy.APIKeyConfig.ModelOverrides),
		}
	}
	return request
}

// mediaBinder is a provider media request that can take the route the planner
// chose and the credential the policy selected. Every media request embeds
// connectors.MediaTarget, so the binding is one call rather than three field
// writes repeated per operation.
type mediaBinder interface {
	Bind(model string, endpoint connectors.InferenceEndpoint, credential credentials.Material)
}

// mediaCall names the three steps that differ between the media operations:
// finding the narrow transport interface, building the provider request, and
// converting the answer back. Everything around them is one shape, written
// once in attempt below, so all three operations report a missing transport, a
// provider error, and an unreadable answer the same way.
type mediaCall[Request mediaBinder, ProviderResponse, Response any] struct {
	transport func(connectors.Connector, catalogs.EndpointType) (mediaInvoke[Request, ProviderResponse], bool)
	build     func() Request
	convert   func(ProviderResponse) (Response, error)
}

// mediaInvoke is one provider media call, taken from the transport interface
// that serves the operation.
type mediaInvoke[Request, ProviderResponse any] func(context.Context, Request) (ProviderResponse, error)

// attempt turns one media call into the attempt the shared route path runs.
func (call mediaCall[Request, ProviderResponse, Response]) attempt(
	operation routing.Operation,
) mediaAttempt[Response] {
	return func(
		attemptCtx context.Context,
		connector connectors.Connector,
		route routing.Route,
		selected credentialSelection,
	) (*Response, *failure.Failure, execution.AttemptAction) {
		invoke, implemented := call.transport(connector, endpointTypeOf(route))
		if !implemented {
			return nil, mediaInterfaceMissing(route, string(operation)), execution.AttemptActionDefault
		}
		request := call.build()
		request.Bind(
			route.ProviderModelID,
			connectors.InferenceEndpoint{Type: endpointTypeOf(route), URL: route.Endpoint.URL},
			selected.material,
		)
		response, requestErr := invoke(attemptCtx, request)
		if requestErr != nil {
			return nil, connectors.NormalizeFailure(route.ProviderID, requestErr), execution.AttemptActionDefault
		}
		canonical, convertErr := call.convert(response)
		return mediaAnswer(route, canonical, convertErr)
	}
}

func imageTransport(
	connector connectors.Connector,
	endpointType catalogs.EndpointType,
) (mediaInvoke[*connectors.ImagesRequest, *connectors.ImagesResponse], bool) {
	generator, implemented := connectors.ImageGeneratorFor(connector, endpointType)
	if !implemented {
		return nil, false
	}
	return generator.GenerateImages, true
}

func speechTransport(
	connector connectors.Connector,
	endpointType catalogs.EndpointType,
) (mediaInvoke[*connectors.SpeechRequest, *connectors.SpeechResponse], bool) {
	synthesizer, implemented := connectors.SpeechSynthesizerFor(connector, endpointType)
	if !implemented {
		return nil, false
	}
	return synthesizer.SynthesizeSpeech, true
}

func transcriptionTransport(
	connector connectors.Connector,
	endpointType catalogs.EndpointType,
) (mediaInvoke[*connectors.TranscriptionRequest, *connectors.TranscriptionResponse], bool) {
	transcriber, implemented := connectors.TranscriberFor(connector, endpointType)
	if !implemented {
		return nil, false
	}
	return transcriber.Transcribe, true
}

// mediaAnswer names the routed model on a converted response, and reports an
// unconvertible provider answer as an internal failure rather than as a result.
func mediaAnswer[Response any](
	route routing.Route,
	canonical Response,
	err error,
) (*Response, *failure.Failure, execution.AttemptAction) {
	if err != nil {
		return nil, failure.New(
			failure.Internal,
			"The provider response was invalid.",
			false,
			failure.ProviderDetails{Provider: route.ProviderID},
			err,
		), execution.AttemptActionDefault
	}
	return &canonical, nil, execution.AttemptActionDefault
}

func endpointTypeOf(route routing.Route) catalogs.EndpointType {
	return catalogs.EndpointType(route.Endpoint.Protocol)
}

// mediaInterfaceMissing reports a route whose transport does not implement the
// media interface the operation needs. Activation refuses a descriptor in that
// state, so reaching this means the catalog named an operation the compiled
// transport does not serve. The next route may serve it, so the attempt is
// retryable rather than fatal.
func mediaInterfaceMissing(route routing.Route, operation string) *failure.Failure {
	return failure.New(
		failure.ProviderUnavailable,
		fmt.Sprintf("The provider transport does not serve %s.", operation),
		true,
		failure.ProviderDetails{Provider: route.ProviderID},
		nil,
	)
}
