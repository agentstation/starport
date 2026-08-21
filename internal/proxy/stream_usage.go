package proxy

import (
	"errors"
	"io"
	"strings"

	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/tokenize"
)

// usageNormalizingStream guarantees that every chat stream ends with one
// usage event. Provider-reported usage passes through untouched; when the
// stream reaches EOF without any usage, the wrapper synthesizes estimated
// counts from the request messages and the accumulated completion text.
// The synthesized event flows outward through the cache and usage-capture
// middlewares, so replays, accounting, and budgets all see the same frame
// the client does.
type usageNormalizingStream struct {
	stream    ChatCompletionStreamResponse
	estimator *tokenize.Estimator
	messages  []inference.Message

	// Frame identity latched from observed events so the synthesized
	// usage frame carries the same stream identity as its deltas.
	id          string
	createdUnix int64
	model       string
	modelUsed   string
	fingerprint string

	completion strings.Builder
	reasoning  strings.Builder
	sawUsage   bool
	pending    *inference.StreamEvent
	done       bool
}

// newUsageNormalizingStream wraps one routed stream. A nil estimator
// returns the stream unchanged.
func newUsageNormalizingStream(
	stream ChatCompletionStreamResponse,
	messages []inference.Message,
	estimator *tokenize.Estimator,
) ChatCompletionStreamResponse {
	if estimator == nil {
		return stream
	}
	return &usageNormalizingStream{stream: stream, estimator: estimator, messages: messages}
}

func (s *usageNormalizingStream) Read() (*inference.StreamEvent, error) {
	if s.pending != nil {
		event := s.pending
		s.pending = nil
		return event, nil
	}
	if s.done {
		return nil, io.EOF
	}
	event, err := s.stream.Read()
	if event != nil {
		s.observe(event)
	}
	if errors.Is(err, io.EOF) {
		s.done = true
		if !s.sawUsage {
			if synthesized := s.synthesize(); synthesized != nil {
				if event != nil {
					s.pending = synthesized
					return event, nil
				}
				return synthesized, nil
			}
		}
	}
	return event, err
}

func (s *usageNormalizingStream) Close() error {
	return s.stream.Close()
}

// Unwrap exposes the routed stream so cross-cutting middleware reaches
// route evidence.
func (s *usageNormalizingStream) Unwrap() ChatCompletionStreamResponse {
	return s.stream
}

// GetCacheStatus forwards the inner stream's cache status.
func (s *usageNormalizingStream) GetCacheStatus() string {
	if status, ok := s.stream.(CacheStatusProvider); ok {
		return status.GetCacheStatus()
	}
	return ""
}

// GetCacheAge forwards the inner stream's cache age.
func (s *usageNormalizingStream) GetCacheAge() int {
	if status, ok := s.stream.(CacheStatusProvider); ok {
		return status.GetCacheAge()
	}
	return 0
}

func (s *usageNormalizingStream) observe(event *inference.StreamEvent) {
	if event.ID != "" {
		s.id = event.ID
	}
	if event.CreatedUnix != 0 {
		s.createdUnix = event.CreatedUnix
	}
	if event.Model != "" {
		s.model = event.Model
	}
	if event.ModelUsed != "" {
		s.modelUsed = event.ModelUsed
	}
	if event.SystemFingerprint != "" {
		s.fingerprint = event.SystemFingerprint
	}
	if event.Usage != nil {
		s.sawUsage = true
	}
	for _, delta := range event.Deltas {
		s.completion.WriteString(delta.Text)
		s.reasoning.WriteString(delta.Reasoning)
	}
}

// synthesize builds one estimated usage event for a stream that ended
// without provider usage.
func (s *usageNormalizingStream) synthesize() *inference.StreamEvent {
	hint := s.tokenizerHint()
	input := s.estimator.CountMessages(hint, s.messages)
	output := s.estimator.CountText(hint, s.completion.String())
	reasoning := s.estimator.CountText(hint, s.reasoning.String())
	usage := inference.Usage{
		InputTokens:     input,
		OutputTokens:    output + reasoning,
		TotalTokens:     input + output + reasoning,
		ReasoningTokens: reasoning,
		Estimated:       true,
	}
	return &inference.StreamEvent{
		Kind:              inference.StreamUsage,
		ID:                s.id,
		CreatedUnix:       s.createdUnix,
		Model:             s.model,
		ModelUsed:         s.modelUsed,
		SystemFingerprint: s.fingerprint,
		Usage:             &usage,
	}
}

// tokenizerHint resolves the routed model's tokenizer family from the
// exact catalog snapshot that routed the stream.
func (s *usageNormalizingStream) tokenizerHint() tokenize.Hint {
	hint := tokenize.Hint{Model: s.modelUsed}
	evidence := findStreamEvidence(s.stream)
	if evidence == nil {
		return hint
	}
	snapshot := evidence.CatalogSnapshot()
	if snapshot == nil {
		return hint
	}
	route, ok := snapshot.ResolveRoute(s.modelUsed)
	if !ok {
		return hint
	}
	hint.Model = string(route.ProviderModelID)
	definition, err := snapshot.Definition(route.DefinitionID)
	if err == nil && definition.Weights.Architecture != nil {
		hint.Tokenizer = definition.Weights.Architecture.Tokenizer.String()
	}
	return hint
}
