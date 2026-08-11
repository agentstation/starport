package execution

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/routing"
)

// StartChatStream starts an execution-owned stream. It can retry or fall back
// only before it returns the first canonical event to its caller.
func (e *Executor) StartChatStream(
	ctx context.Context,
	plan *routing.Plan,
	attempt StreamAttempt,
) (ManagedStream, error) {
	if plan == nil {
		return nil, ErrPlanRequired
	}
	if attempt == nil {
		return nil, ErrAttemptRequired
	}
	streamCtx, cancel := context.WithTimeout(ctx, e.config.MaxElapsed)
	managed := &managedStream{
		ctx:           streamCtx,
		cancel:        cancel,
		session:       newSession(e, plan),
		start:         attempt,
		evidenceIndex: -1,
	}
	if err := managed.startNext(); err != nil {
		cancel()
		return nil, err
	}
	return managed, nil
}

type managedStream struct {
	readMu sync.Mutex
	mu     sync.Mutex

	ctx    context.Context
	cancel context.CancelFunc

	session       *session
	start         StreamAttempt
	current       Stream
	currentRoute  routing.Attempt
	evidenceIndex int
	committed     bool
	terminal      bool
	pendingError  error
}

func (s *managedStream) Read() (*inference.StreamEvent, error) {
	s.readMu.Lock()
	defer s.readMu.Unlock()

	for {
		s.mu.Lock()
		if s.terminal {
			s.mu.Unlock()
			return nil, io.EOF
		}
		current := s.current
		pendingError := s.pendingError
		s.pendingError = nil
		s.mu.Unlock()

		var event *inference.StreamEvent
		var err error
		if pendingError != nil {
			err = pendingError
		} else {
			event, err = current.Read()
		}
		if event != nil {
			s.mu.Lock()
			if s.terminal {
				s.mu.Unlock()
				return nil, io.EOF
			}
			s.committed = true
			if err != nil {
				s.pendingError = err
			}
			s.mu.Unlock()
			return event, nil
		}
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			s.mu.Lock()
			if s.terminal {
				s.mu.Unlock()
				return nil, io.EOF
			}
			s.session.succeed(s.evidenceIndex)
			s.terminal = true
			s.cancel()
			s.mu.Unlock()
			_ = current.Close()
			return nil, io.EOF
		}

		providerFailure, action := failureFromError(s.currentRoute.Route.ProviderID, err)
		s.mu.Lock()
		if s.terminal {
			s.mu.Unlock()
			return nil, io.EOF
		}
		decision := s.session.fail(s.evidenceIndex, providerFailure, action)
		if s.committed || decision == decisionStop {
			s.terminal = true
			s.cancel()
			terminalError := s.session.terminalError(ErrAllAttemptsFailed)
			s.mu.Unlock()
			_ = current.Close()
			return nil, terminalError
		}
		s.mu.Unlock()
		_ = current.Close()
		if err := s.session.wait(s.ctx, decision); err != nil {
			s.mu.Lock()
			s.terminal = true
			s.cancel()
			terminalError := s.session.cancelError(err)
			s.mu.Unlock()
			return nil, terminalError
		}
		if err := s.startNext(); err != nil {
			s.mu.Lock()
			s.terminal = true
			s.cancel()
			s.mu.Unlock()
			return nil, err
		}
	}
}

func (s *managedStream) Close() error {
	s.mu.Lock()
	if s.terminal {
		s.mu.Unlock()
		return nil
	}
	s.terminal = true
	s.cancel()
	if s.evidenceIndex >= 0 && s.evidenceIndex < len(s.session.evidence) {
		evidence := &s.session.evidence[s.evidenceIndex]
		if evidence.State == StateRunning {
			s.session.fail(s.evidenceIndex, contextFailure(context.Canceled), AttemptActionDefault)
		}
	}
	current := s.current
	s.mu.Unlock()
	if current == nil {
		return nil
	}
	return current.Close()
}

func (s *managedStream) Attempts() []AttemptEvidence {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.session.evidenceCopy()
}

func (s *managedStream) Committed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.committed
}

func (s *managedStream) ModelUsed() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentRoute.Route.ID()
}

func (s *managedStream) startNext() error {
	for {
		s.mu.Lock()
		if s.terminal {
			err := s.ctx.Err()
			if err == nil {
				err = context.Canceled
			}
			terminalError := s.session.cancelError(err)
			s.mu.Unlock()
			return terminalError
		}
		planned, evidenceIndex, err := s.session.begin(s.ctx)
		if err != nil {
			s.mu.Unlock()
			return err
		}
		s.mu.Unlock()

		stream, providerFailure, action := s.start(s.ctx, planned)

		s.mu.Lock()
		providerFailure = s.session.normalizeOutcome(s.ctx, stream != nil, providerFailure)
		if providerFailure == nil {
			s.current = stream
			s.currentRoute = planned
			s.evidenceIndex = evidenceIndex
			s.mu.Unlock()
			return nil
		}
		decision := s.session.fail(evidenceIndex, providerFailure, action)
		if decision == decisionStop {
			terminalError := s.session.terminalError(ErrAllAttemptsFailed)
			s.mu.Unlock()
			if stream != nil {
				_ = stream.Close()
			}
			return terminalError
		}
		s.mu.Unlock()
		if stream != nil {
			_ = stream.Close()
		}
		if err := s.session.wait(s.ctx, decision); err != nil {
			s.mu.Lock()
			terminalError := s.session.cancelError(err)
			s.mu.Unlock()
			return terminalError
		}
	}
}

func failureFromError(provider string, err error) (*failure.Failure, AttemptAction) {
	action := AttemptActionDefault
	var actionError *attemptActionError
	if errors.As(err, &actionError) {
		action = actionError.action
	}
	var providerFailure *failure.Failure
	if errors.As(err, &providerFailure) {
		return providerFailure, action
	}
	if errors.Is(err, context.Canceled) {
		return contextFailure(context.Canceled), AttemptActionDefault
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return contextFailure(context.DeadlineExceeded), AttemptActionDefault
	}
	return failure.New(
		failure.ProviderUnavailable,
		"The provider stream failed.",
		true,
		failure.ProviderDetails{Provider: provider},
		err,
	), action
}
