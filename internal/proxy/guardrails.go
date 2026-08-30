package proxy

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/agentstation/starport/internal/guardrails"
	"github.com/agentstation/starport/internal/inference"
)

// guardrailRefusalClass names a guardrail refusal on the usage record and
// the HTTP error, so records match what the client saw.
const guardrailRefusalClass = "guardrail_refusal"

// guardrailStreamWindowBytes bounds how much streamed answer text is held
// back before the response checks read it. A bound keeps memory flat on a
// long answer at the price of checking each window alone.
const guardrailStreamWindowBytes = 8 << 10

// Guardrails is a proxy middleware that runs the account's guardrail
// pipeline over canonical text: the request messages before planning, and
// the answer before the caller reads it. Composition skips this
// middleware entirely when no check is configured, so an unconfigured
// deployment pays nothing here.
type Guardrails struct {
	policy guardrails.Policy
}

// NewGuardrails creates the middleware around one policy.
func NewGuardrails(policy guardrails.Policy) *Guardrails {
	return &Guardrails{policy: policy}
}

// Wrap implements Middleware.
func (g *Guardrails) Wrap(next Proxy) Proxy {
	return &guardrailService{Proxy: next, policy: g.policy}
}

type guardrailService struct {
	Proxy
	policy guardrails.Policy
}

// ProcessChatCompletion checks the request before planning and the answer
// before the caller reads it.
func (s *guardrailService) ProcessChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	pipeline := s.policy.PipelineFor(req.AccountID)
	if pipeline.Len() == 0 {
		return s.Proxy.ProcessChatCompletion(ctx, req)
	}
	requestVerdict, err := inspectMessages(ctx, pipeline, guardrails.DirectionRequest, req.Request.Messages)
	if err != nil {
		return nil, err
	}
	response, err := s.Proxy.ProcessChatCompletion(ctx, req)
	if err != nil || response == nil {
		return response, err
	}
	verdict := requestVerdict
	for i := range response.Response.Choices {
		message := []inference.Message{response.Response.Choices[i].Message}
		choiceVerdict, err := inspectMessages(ctx, pipeline, guardrails.DirectionResponse, message)
		if err != nil {
			return nil, err
		}
		response.Response.Choices[i].Message = message[0]
		verdict = strongerVerdict(verdict, choiceVerdict)
	}
	response.GuardrailVerdict = string(verdict)
	return response, nil
}

// ProcessChatCompletionStream checks the request before planning and
// wraps the stream so the answer is checked window by window.
func (s *guardrailService) ProcessChatCompletionStream(ctx context.Context, req *ChatCompletionRequest) (ChatCompletionStreamResponse, error) {
	pipeline := s.policy.PipelineFor(req.AccountID)
	if pipeline.Len() == 0 {
		return s.Proxy.ProcessChatCompletionStream(ctx, req)
	}
	requestVerdict, err := inspectMessages(ctx, pipeline, guardrails.DirectionRequest, req.Request.Messages)
	if err != nil {
		return nil, err
	}
	stream, err := s.Proxy.ProcessChatCompletionStream(ctx, req)
	if err != nil {
		return nil, err
	}
	return &guardrailStream{
		inner:    stream,
		ctx:      ctx,
		pipeline: pipeline,
		verdict:  requestVerdict,
	}, nil
}

// inspectMessages runs the pipeline over every text part, in place. A
// redaction rewrites the part, so planning, caching, and the provider all
// read the redacted text.
func inspectMessages(ctx context.Context, pipeline *guardrails.Pipeline, direction guardrails.Direction, messages []inference.Message) (guardrails.Verdict, error) {
	verdict := guardrails.VerdictAllow
	for i := range messages {
		content := messages[i].Content
		for j := range content {
			part := &content[j]
			if part.Kind != inference.ContentText || part.Text == "" {
				continue
			}
			text, partVerdict, err := pipeline.Inspect(ctx, direction, part.Text)
			if err != nil {
				return guardrails.VerdictRefuse, err
			}
			if partVerdict == guardrails.VerdictRedact {
				part.Text = text
			}
			verdict = strongerVerdict(verdict, partVerdict)
		}
	}
	return verdict, nil
}

// strongerVerdict keeps the strongest of two verdicts: refuse over redact
// over allow.
func strongerVerdict(a, b guardrails.Verdict) guardrails.Verdict {
	switch {
	case a == guardrails.VerdictRefuse || b == guardrails.VerdictRefuse:
		return guardrails.VerdictRefuse
	case a == guardrails.VerdictRedact || b == guardrails.VerdictRedact:
		return guardrails.VerdictRedact
	default:
		return guardrails.VerdictAllow
	}
}

// GuardrailVerdictProvider exposes the strongest verdict a stream saw, so
// the usage middleware outside this one can record it at stream end.
type GuardrailVerdictProvider interface {
	GuardrailVerdict() string
}

// guardrailStream holds streamed answer text back in a bounded window and
// runs the response checks over each window before releasing it. A
// refusal withholds the held events; the caller sees the refusal error
// instead of the text that drew it.
type guardrailStream struct {
	inner    ChatCompletionStreamResponse
	ctx      context.Context
	pipeline *guardrails.Pipeline
	verdict  guardrails.Verdict

	held    []*inference.StreamEvent
	window  strings.Builder
	pending []*inference.StreamEvent
	failed  error
	done    bool
}

func (s *guardrailStream) Read() (*inference.StreamEvent, error) {
	for {
		if len(s.pending) > 0 {
			event := s.pending[0]
			s.pending = s.pending[1:]
			return event, nil
		}
		if s.failed != nil {
			return nil, s.failed
		}
		if s.done {
			return nil, io.EOF
		}
		event, err := s.inner.Read()
		if event != nil {
			if text := eventText(event); text != "" || len(s.held) > 0 {
				// Once a window is open every event joins it, so order
				// survives the release.
				s.held = append(s.held, event)
				s.window.WriteString(text)
			} else if err == nil {
				return event, nil
			} else {
				s.pending = append(s.pending, event)
			}
		}
		if err != nil {
			if flushErr := s.flushWindow(); flushErr != nil {
				s.failed = flushErr
				s.pending = nil
				continue
			}
			if errors.Is(err, io.EOF) {
				s.done = true
				continue
			}
			s.failed = err
			continue
		}
		if s.window.Len() >= guardrailStreamWindowBytes {
			if flushErr := s.flushWindow(); flushErr != nil {
				s.failed = flushErr
				continue
			}
		}
	}
}

func (s *guardrailStream) Close() error {
	return s.inner.Close()
}

// Unwrap exposes the wrapped stream for cross-cutting middleware.
func (s *guardrailStream) Unwrap() ChatCompletionStreamResponse {
	return s.inner
}

// GuardrailVerdict implements GuardrailVerdictProvider.
func (s *guardrailStream) GuardrailVerdict() string {
	return string(s.verdict)
}

// flushWindow inspects the held window and releases it toward the caller.
// A refusal drops the held events and answers the refusal instead.
func (s *guardrailStream) flushWindow() error {
	if len(s.held) == 0 {
		return nil
	}
	held, text := s.held, s.window.String()
	s.held = nil
	s.window.Reset()
	inspected, verdict, err := s.pipeline.Inspect(s.ctx, guardrails.DirectionResponse, text)
	if err != nil {
		s.verdict = guardrails.VerdictRefuse
		return err
	}
	if verdict != guardrails.VerdictRedact {
		s.pending = append(s.pending, held...)
		return nil
	}
	s.verdict = strongerVerdict(s.verdict, guardrails.VerdictRedact)
	// A redaction rewrites the window as one string, so the original
	// delta boundaries cannot be kept. The text moves to one synthesized
	// delta, placed before the event that ends the choice, and every
	// other part of the held events survives in order.
	synthesized := synthesizedTextEvent(held[0], inspected)
	inserted := false
	for _, event := range held {
		if !inserted && eventEndsChoice(event) {
			s.pending = append(s.pending, synthesized)
			inserted = true
		}
		if stripped := withoutText(event); stripped != nil {
			s.pending = append(s.pending, stripped)
		}
	}
	if !inserted {
		s.pending = append(s.pending, synthesized)
	}
	return nil
}

// eventText concatenates the answer text one event carries.
func eventText(event *inference.StreamEvent) string {
	switch len(event.Deltas) {
	case 0:
		return ""
	case 1:
		return event.Deltas[0].Text
	}
	var builder strings.Builder
	for _, delta := range event.Deltas {
		builder.WriteString(delta.Text)
	}
	return builder.String()
}

// eventEndsChoice reports whether the event closes the answer, which is
// where a synthesized redaction has to land before.
func eventEndsChoice(event *inference.StreamEvent) bool {
	if event.Kind == inference.StreamEnd || event.Kind == inference.StreamUsage {
		return true
	}
	for _, delta := range event.Deltas {
		if delta.FinishReason != "" {
			return true
		}
	}
	return false
}

// withoutText copies the event with the answer text removed. It returns
// nil when nothing but that text remains.
func withoutText(event *inference.StreamEvent) *inference.StreamEvent {
	stripped := *event
	stripped.Deltas = make([]inference.ChoiceDelta, 0, len(event.Deltas))
	for _, delta := range event.Deltas {
		delta.Text = ""
		if delta.Role == "" && delta.Reasoning == "" && delta.Audio == nil &&
			len(delta.Media) == 0 && len(delta.ToolCalls) == 0 &&
			len(delta.LogProbs) == 0 && delta.FinishReason == "" {
			continue
		}
		stripped.Deltas = append(stripped.Deltas, delta)
	}
	if event.Kind == inference.StreamDelta && len(stripped.Deltas) == 0 && stripped.Usage == nil {
		return nil
	}
	return &stripped
}

// synthesizedTextEvent carries one redacted window as a single delta,
// stamped with the identity fields of the window's first event.
func synthesizedTextEvent(template *inference.StreamEvent, text string) *inference.StreamEvent {
	return &inference.StreamEvent{
		Kind:              inference.StreamDelta,
		ID:                template.ID,
		CreatedUnix:       template.CreatedUnix,
		Model:             template.Model,
		ModelUsed:         template.ModelUsed,
		SystemFingerprint: template.SystemFingerprint,
		Deltas:            []inference.ChoiceDelta{{Index: 0, Text: text}},
	}
}
