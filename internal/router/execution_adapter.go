package router

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/execution"
	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/routing"
)

// RouteStream executes the same immutable route plan and total budget as a
// non-streaming request. It can change routes only before the first event.
func (r *modelRouter) RouteStream(ctx context.Context, req *Request) (execution.ManagedStream, error) {
	if req == nil || req.ChatRequest == nil {
		return nil, ErrNoModelsAvailable
	}
	plan, err := r.planRoute(ctx, req)
	if err != nil {
		if errors.Is(err, routing.ErrNoCandidate) {
			return nil, ErrNoModelsAvailable
		}
		return nil, err
	}
	return r.executor.StartChatStream(ctx, plan, func(attemptCtx context.Context, planned routing.Attempt) (execution.Stream, *failure.Failure) {
		connector := r.registry.Get(planned.Route.ProviderID)
		if connector == nil {
			return nil, failure.New(
				failure.ProviderUnavailable,
				"No provider adapter is available.",
				true,
				failure.ProviderDetails{Provider: planned.Route.ProviderID},
				nil,
			)
		}
		request := prepareChatAttempt(req, planned.Route, true)
		request.Stream = true
		stream, streamErr := connector.ChatStream(attemptCtx, request)
		if streamErr != nil {
			return nil, connectors.NormalizeFailure(planned.Route.ProviderID, streamErr)
		}
		return &connectorEventStream{
			stream:   stream,
			provider: planned.Route.ProviderID,
			modelID:  planned.Route.ID(),
		}, nil
	})
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
	stream   connectors.ChatStream
	provider string
	modelID  string
	pending  []inference.StreamEvent
	terminal error
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

		chunk, err := s.stream.Recv()
		if err != nil && chunk == nil {
			if errors.Is(err, io.EOF) {
				return nil, io.EOF
			}
			return nil, connectors.NormalizeFailure(s.provider, err)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				s.terminal = io.EOF
			} else {
				s.terminal = connectors.NormalizeFailure(s.provider, err)
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
