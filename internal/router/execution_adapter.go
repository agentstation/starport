package router

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/execution"
	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/providers/keyring"
	"github.com/agentstation/starport/internal/routing"
)

// RouteStream executes the same immutable route plan and total budget as a
// non-streaming request. It can change routes only before the first event.
func (r *modelRouter) RouteStream(ctx context.Context, req *Request) (execution.ManagedStream, error) {
	if req == nil || req.ChatRequest == nil {
		return nil, ErrNoModelsAvailable
	}
	runtime, owned, err := r.acquireRuntime(ctx)
	if err != nil {
		return nil, ErrNoModelsAvailable
	}
	plan, err := r.planRoute(ctx, req, runtime)
	if err != nil {
		if owned {
			runtime.Release()
		}
		if errors.Is(err, routing.ErrNoCandidate) {
			return nil, ErrNoModelsAvailable
		}
		return nil, err
	}
	strategy, tenantID := credentialRequestPolicy(req)
	credentialPolicy, err := newCredentialPolicy(
		strategy, tenantID, runtime, r.storedKeys, r.credentialGate,
	)
	if err != nil {
		if owned {
			runtime.Release()
		}
		return nil, err
	}
	stream, err := r.executor.StartChatStream(ctx, plan, func(attemptCtx context.Context, planned routing.Attempt) (execution.Stream, *failure.Failure, execution.AttemptAction) {
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
		request := prepareChatAttempt(req, boundRoute, true)
		request.Credential = selected.material
		request.Stream = true
		timer := execution.OverheadTimerFrom(attemptCtx)
		// The first event is part of establishment: providers such as Groq
		// reject an attempt inside an established 200 stream (an SSE error
		// frame before any content). Reading it here keeps those
		// rejections on the same fallback-and-status path as a non-200.
		endUpstream := timer.TrackUpstream()
		stream, streamErr := connector.ChatStream(attemptCtx, request)
		var firstChunk *connectors.ChatStreamChunk
		if streamErr == nil {
			firstChunk, streamErr = stream.Recv()
		}
		endUpstream()
		if streamErr != nil {
			if stream != nil {
				_ = stream.Close()
			}
			if errors.Is(streamErr, io.EOF) {
				streamErr = failure.New(
					failure.ProviderUnavailable,
					"The provider returned an empty stream.",
					true,
					failure.ProviderDetails{
						Provider:   planned.Route.ProviderID,
						StateScope: failure.ScopeOffering,
					},
					nil,
				)
			}
			providerFailure := connectors.NormalizeFailure(planned.Route.ProviderID, streamErr)
			return nil, providerFailure, credentialPolicy.afterFailure(planned.Route, providerFailure)
		}
		execution.RecordCredentialAccepted(attemptCtx)
		return &connectorEventStream{
			stream:   stream,
			first:    firstChunk,
			timer:    timer,
			provider: planned.Route.ProviderID,
			modelID:  planned.Route.ID(),
			action: func(providerFailure *failure.Failure) execution.AttemptAction {
				return credentialPolicy.afterFailure(planned.Route, providerFailure)
			},
		}, nil, execution.AttemptActionDefault
	})
	if err != nil {
		if owned {
			runtime.Release()
		}
		return nil, err
	}
	// Snapshot generations are immutable, so the evidence pointer stays valid
	// after the lease releases (same contract as RouteWithFallback).
	snapshot := runtime.Snapshot()
	managed := stream
	if owned {
		managed = &runtimeManagedStream{ManagedStream: stream, runtime: runtime}
	}
	return &evidenceManagedStream{ManagedStream: managed, snapshot: snapshot}, nil
}

// StreamEvidence exposes route evidence from a managed stream for usage
// accounting.
type StreamEvidence interface {
	ProviderUsed() string
	AttemptCount() int
	RoutingDuration() time.Duration
	CatalogSnapshot() *runtimecatalog.RoutableSnapshot
}

// evidenceManagedStream decorates every routed stream with the evidence a
// usage recorder needs after the stream ends.
type evidenceManagedStream struct {
	execution.ManagedStream
	snapshot *runtimecatalog.RoutableSnapshot
}

func (s *evidenceManagedStream) ProviderUsed() string {
	evidence := s.Attempts()
	for index := len(evidence) - 1; index >= 0; index-- {
		if evidence[index].State == execution.StateSkipped {
			continue
		}
		return evidence[index].Route.ProviderID
	}
	return ""
}

func (s *evidenceManagedStream) AttemptCount() int { return len(s.Attempts()) }

func (s *evidenceManagedStream) RoutingDuration() time.Duration {
	var duration time.Duration
	for _, item := range s.Attempts() {
		duration += item.Duration
	}
	return duration
}

func (s *evidenceManagedStream) CatalogSnapshot() *runtimecatalog.RoutableSnapshot {
	return s.snapshot
}

type runtimeManagedStream struct {
	execution.ManagedStream
	runtime connectors.RuntimeLease
	once    sync.Once
}

func (s *runtimeManagedStream) Read() (*inference.StreamEvent, error) {
	event, err := s.ManagedStream.Read()
	if err != nil {
		s.release()
	}
	return event, err
}

func (s *runtimeManagedStream) Close() error {
	err := s.ManagedStream.Close()
	s.release()
	return err
}

func (s *runtimeManagedStream) release() {
	s.once.Do(s.runtime.Release)
}

func prepareChatAttempt(req *Request, route routing.Route, streaming bool) *connectors.ChatRequest {
	request := *req.ChatRequest
	request.Model = route.ProviderModelID
	endpointURL := route.Endpoint.URL
	if streaming && route.Endpoint.StreamURL != "" {
		endpointURL = route.Endpoint.StreamURL
	}
	request.Endpoint = connectors.InferenceEndpoint{
		Type: catalogs.EndpointType(route.Endpoint.Protocol),
		URL:  endpointURL,
	}
	if req.PrepareAttempt != nil {
		if prepared := req.PrepareAttempt(route, &request); prepared != nil {
			request = *prepared
		}
	}
	return &request
}

func bindSelectedEndpoint(
	runtime connectors.RuntimeLease,
	route routing.Route,
	selected credentialSelection,
) (routing.Route, *failure.Failure) {
	if route.Operation == "" {
		return route, nil
	}
	binder, ok := runtime.(connectors.EndpointBinder)
	if !ok {
		snapshot := runtime.Snapshot()
		if snapshot == nil || snapshot.Catalog() == nil {
			return routing.Route{}, failure.New(
				failure.Internal,
				"The provider runtime cannot bind the selected endpoint.",
				false,
				failure.ProviderDetails{Provider: route.ProviderID},
				nil,
			)
		}
		provider, err := snapshot.Catalog().Provider(catalogs.ProviderID(route.ProviderID))
		if err != nil || provider.Inference == nil {
			return routing.Route{}, failure.New(
				failure.Internal,
				"The provider runtime cannot bind the selected endpoint.",
				false,
				failure.ProviderDetails{Provider: route.ProviderID},
				err,
			)
		}
		binder = snapshotEndpointBinder{inference: provider.Inference}
	}
	endpoint, err := binder.BindEndpoint(
		route.ProviderID,
		catalogs.ProviderOfferingEndpoint{
			Operation: catalogs.ProviderOperation(route.Operation),
			Type:      catalogs.EndpointType(route.Endpoint.Protocol),
			URL:       route.Endpoint.URL,
			StreamURL: route.Endpoint.StreamURL,
		},
		selected.material,
		selected.source == keyring.SourceEnvironment,
	)
	if err != nil {
		return routing.Route{}, failure.New(
			failure.Validation,
			"Provider endpoint configuration is invalid.",
			false,
			failure.ProviderDetails{Provider: route.ProviderID},
			err,
		)
	}
	bound := route
	bound.Endpoint = routing.Endpoint{
		Protocol: string(endpoint.Type), URL: endpoint.URL, StreamURL: endpoint.StreamURL,
	}
	return bound, nil
}

type snapshotEndpointBinder struct {
	inference *catalogs.ProviderInference
}

func (b snapshotEndpointBinder) BindEndpoint(
	_ string,
	endpoint catalogs.ProviderOfferingEndpoint,
	material credentials.Material,
	_ bool,
) (catalogs.ProviderOfferingEndpoint, error) {
	return b.inference.BindOfferingEndpoint(endpoint, "", material.EndpointBindings())
}

func executionEvidence(err error) []execution.AttemptEvidence {
	var executionError *execution.Error
	if errors.As(err, &executionError) {
		return executionError.Attempts
	}
	return nil
}

func responseMetadata(evidence []execution.AttemptEvidence, reason string) *Metadata {
	attempts := make([]ModelAttempt, len(evidence))
	var duration time.Duration
	for index, item := range evidence {
		errorMessage := ""
		if item.Failure != nil {
			errorMessage = item.Failure.SafeMessage()
		}
		attempts[index] = ModelAttempt{
			Model:    item.Route.ID(),
			Provider: item.Route.ProviderID,
			Error:    errorMessage,
			Duration: item.Duration,
			Status:   metadataStatus(item.State),
		}
		duration += item.Duration
	}
	return &Metadata{
		ModelsAttempted: attempts,
		RoutingDuration: duration,
		SelectionReason: reason,
	}
}

func metadataStatus(state execution.State) string {
	switch state {
	case execution.StateSucceeded:
		return "success"
	case execution.StateSkipped:
		return "skipped"
	default:
		return "failed"
	}
}

func selectionReason(evidence []execution.AttemptEvidence) string {
	for index := len(evidence) - 1; index >= 0; index-- {
		if evidence[index].State != execution.StateSucceeded {
			continue
		}
		if index == 0 {
			return "primary route succeeded"
		}
		return "fallback route succeeded"
	}
	return "all routes failed"
}

type connectorEventStream struct {
	stream connectors.ChatStream
	// first is the chunk the establishment path already read while
	// verifying the stream opens with content instead of an error frame.
	first    *connectors.ChatStreamChunk
	timer    *execution.OverheadTimer
	provider string
	modelID  string
	pending  []inference.StreamEvent
	terminal error
	action   func(*failure.Failure) execution.AttemptAction
}

func (s *connectorEventStream) Read() (*inference.StreamEvent, error) {
	for {
		if len(s.pending) > 0 {
			event := s.pending[0]
			s.pending = s.pending[1:]
			return &event, nil
		}
		if s.terminal != nil {
			err := s.terminal
			s.terminal = nil
			return nil, err
		}

		var chunk *connectors.ChatStreamChunk
		var err error
		if s.first != nil {
			chunk, s.first = s.first, nil
		} else {
			endUpstream := s.timer.TrackUpstream()
			chunk, err = s.stream.Recv()
			endUpstream()
		}
		if err != nil && chunk == nil {
			if errors.Is(err, io.EOF) {
				return nil, io.EOF
			}
			providerFailure := connectors.NormalizeFailure(s.provider, err)
			return nil, execution.WithAttemptAction(providerFailure, s.failureAction(providerFailure))
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				s.terminal = io.EOF
			} else {
				providerFailure := connectors.NormalizeFailure(s.provider, err)
				s.terminal = execution.WithAttemptAction(providerFailure, s.failureAction(providerFailure))
			}
		}
		events, conversionErr := connectors.StreamEventsToInference(chunk, s.modelID)
		if conversionErr != nil {
			return nil, failure.New(
				failure.Internal,
				"The provider stream was invalid.",
				false,
				failure.ProviderDetails{Provider: s.provider},
				conversionErr,
			)
		}
		s.pending = append(s.pending, events...)
		if len(events) == 0 && errors.Is(s.terminal, io.EOF) {
			s.terminal = nil
			return nil, io.EOF
		}
	}
}

func (s *connectorEventStream) Close() error { return s.stream.Close() }

func (s *connectorEventStream) failureAction(providerFailure *failure.Failure) execution.AttemptAction {
	if s.action == nil {
		return execution.AttemptActionDefault
	}
	return s.action(providerFailure)
}
