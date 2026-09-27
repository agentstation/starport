package router

import (
	"context"
	"errors"
	"github.com/agentstation/starmap/pkg/catalogs"
	"io"
	"sync"

	"github.com/agentstation/starport/internal/limits/admission"
	"github.com/agentstation/starport/internal/providers/connectors"
)

// budgetChatStream retains capacity until a complete provider stream supplies
// usage. Close and transport failure cannot turn partial usage into a refund.
type budgetChatStream struct {
	stream  connectors.ChatStream
	ctx     context.Context
	ticket  admission.Ticket
	billing *catalogs.TextChatBilling
	mu      sync.Mutex
	usage   *connectors.Usage
	done    bool
	err     error
}

func (s *budgetChatStream) Recv() (*connectors.ChatStreamChunk, error) {
	chunk, err := s.stream.Recv()
	s.mu.Lock()
	defer s.mu.Unlock()
	if chunk != nil && chunk.Usage != nil && !s.done {
		retained := chunk.Usage.Copy()
		s.usage = &retained
	}
	if err != nil {
		var usage *connectors.Usage
		completion, reportsCompletion := s.stream.(connectors.StreamCompletion)
		if errors.Is(err, io.EOF) && reportsCompletion && completion.CompletionObserved() {
			usage = s.usage
		}
		if settlementErr := s.finish(usage); settlementErr != nil {
			return chunk, budgetFailure(settlementErr)
		}
	}
	return chunk, err
}

func (s *budgetChatStream) Close() error {
	err := s.stream.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	return errors.Join(err, s.finish(nil))
}

// finish requires mu. A concurrent Close can establish uncertainty before EOF.
// A later read cannot replace that conservative result with partial evidence.
func (s *budgetChatStream) finish(usage *connectors.Usage) error {
	if !s.done {
		s.done = true
		s.err = finishChatBudget(s.ctx, s.ticket, usage, s.billing)
	}
	return s.err
}
