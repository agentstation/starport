package proxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/guardrails"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/usage"
)

// replaceCheck redacts every occurrence of one substring.
type replaceCheck struct {
	name string
	from string
	to   string
}

func (c replaceCheck) Name() string { return c.name }

func (c replaceCheck) Inspect(_ context.Context, content guardrails.Content) (guardrails.Result, error) {
	if !strings.Contains(content.Text, c.from) {
		return guardrails.Result{Verdict: guardrails.VerdictAllow}, nil
	}
	return guardrails.Result{
		Verdict:  guardrails.VerdictRedact,
		Redacted: strings.ReplaceAll(content.Text, c.from, c.to),
	}, nil
}

// matchRefuseCheck refuses every text that holds one substring.
type matchRefuseCheck struct {
	name string
	on   string
}

func (c matchRefuseCheck) Name() string { return c.name }

func (c matchRefuseCheck) Inspect(_ context.Context, content guardrails.Content) (guardrails.Result, error) {
	if strings.Contains(content.Text, c.on) {
		return guardrails.Result{Verdict: guardrails.VerdictRefuse, Reason: "matched " + c.on}, nil
	}
	return guardrails.Result{Verdict: guardrails.VerdictAllow}, nil
}

// brokenCheck cannot evaluate anything.
type brokenCheck struct{}

func (brokenCheck) Name() string { return "broken" }

func (brokenCheck) Inspect(context.Context, guardrails.Content) (guardrails.Result, error) {
	return guardrails.Result{}, errors.New("evaluator offline")
}

// guardrailCoreStub is the wrapped core: it records what reached it and
// answers a scripted response or stream.
type guardrailCoreStub struct {
	Proxy
	chatCalls    int
	streamCalls  int
	lastMessages []inference.Message
	response     *ChatCompletionResponse
	events       []*inference.StreamEvent
	stream       *scriptedStream
}

func (s *guardrailCoreStub) ProcessChatCompletion(_ context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	s.chatCalls++
	s.lastMessages = req.Request.Messages
	return s.response, nil
}

func (s *guardrailCoreStub) ProcessChatCompletionStream(_ context.Context, req *ChatCompletionRequest) (ChatCompletionStreamResponse, error) {
	s.streamCalls++
	s.lastMessages = req.Request.Messages
	s.stream = &scriptedStream{events: s.events}
	return s.stream, nil
}

// scriptedStream replays a fixed event sequence and then io.EOF.
type scriptedStream struct {
	events []*inference.StreamEvent
	closed bool
}

func (s *scriptedStream) Read() (*inference.StreamEvent, error) {
	if len(s.events) == 0 {
		return nil, io.EOF
	}
	event := s.events[0]
	s.events = s.events[1:]
	return event, nil
}

func (s *scriptedStream) Close() error {
	s.closed = true
	return nil
}

func textMessage(role inference.Role, text string) inference.Message {
	return inference.Message{
		Role:    role,
		Content: []inference.ContentPart{{Kind: inference.ContentText, Text: text}},
	}
}

func guardrailChatRequest(text string) *ChatCompletionRequest {
	return &ChatCompletionRequest{
		AccountID: "acct",
		Request: inference.ChatRequest{
			Model:    "author/model",
			Messages: []inference.Message{textMessage(inference.RoleUser, text)},
		},
	}
}

func guardrailChatResponse(text string) *ChatCompletionResponse {
	return &ChatCompletionResponse{
		Response: inference.ChatResponse{
			Choices: []inference.Choice{{Message: textMessage(inference.RoleAssistant, text)}},
		},
	}
}

func wrapGuardrails(core Proxy, checks ...guardrails.Check) Proxy {
	policy := guardrails.StaticPolicy{Pipeline: guardrails.NewPipeline(checks...)}
	return NewGuardrails(policy).Wrap(core)
}

func textDelta(text string) *inference.StreamEvent {
	return &inference.StreamEvent{
		Kind:   inference.StreamDelta,
		ID:     "evt",
		Deltas: []inference.ChoiceDelta{{Text: text}},
	}
}

func streamText(events []*inference.StreamEvent) string {
	var builder strings.Builder
	for _, event := range events {
		for _, delta := range event.Deltas {
			builder.WriteString(delta.Text)
		}
	}
	return builder.String()
}

// TestGuardrailsRedactTheRequestBeforeTheCore holds the pre-request seam:
// planning, caching, and the provider all read the redacted text.
func TestGuardrailsRedactTheRequestBeforeTheCore(t *testing.T) {
	core := &guardrailCoreStub{response: guardrailChatResponse("fine")}
	service := wrapGuardrails(core, replaceCheck{name: "pii", from: "555-0100", to: "[REDACTED]"})

	response, err := service.ProcessChatCompletion(context.Background(), guardrailChatRequest("call 555-0100"))
	require.NoError(t, err)
	require.Equal(t, 1, core.chatCalls)
	require.Equal(t, "call [REDACTED]", core.lastMessages[0].Content[0].Text,
		"the core must read the redacted request")
	require.Equal(t, string(guardrails.VerdictRedact), response.GuardrailVerdict)
}

// TestGuardrailsRefuseTheRequestBeforePlanning holds the refusal seam:
// the core, and so the planner, never sees a refused request.
func TestGuardrailsRefuseTheRequestBeforePlanning(t *testing.T) {
	core := &guardrailCoreStub{response: guardrailChatResponse("fine")}
	service := wrapGuardrails(core, matchRefuseCheck{name: "moderation", on: "forbidden"})

	response, err := service.ProcessChatCompletion(context.Background(), guardrailChatRequest("a forbidden ask"))
	require.Nil(t, response)
	require.ErrorIs(t, err, guardrails.ErrRefused)
	require.Zero(t, core.chatCalls, "a refused request must never reach planning")

	var refusal *guardrails.RefusalError
	require.ErrorAs(t, err, &refusal)
	require.Equal(t, "moderation", refusal.Check)
	require.Equal(t, guardrails.DirectionRequest, refusal.Direction)
}

// TestGuardrailsInspectTheAnswer holds the post-response seam: the caller
// reads the answer as the checks rewrote it.
func TestGuardrailsInspectTheAnswer(t *testing.T) {
	core := &guardrailCoreStub{response: guardrailChatResponse("the number is 555-0100")}
	service := wrapGuardrails(core, replaceCheck{name: "pii", from: "555-0100", to: "[REDACTED]"})

	response, err := service.ProcessChatCompletion(context.Background(), guardrailChatRequest("clean"))
	require.NoError(t, err)
	require.Equal(t, "the number is [REDACTED]", response.Response.Choices[0].Message.Content[0].Text)
	require.Equal(t, string(guardrails.VerdictRedact), response.GuardrailVerdict)
}

// TestGuardrailsRefuseTheAnswer withholds a response the checks refuse.
func TestGuardrailsRefuseTheAnswer(t *testing.T) {
	core := &guardrailCoreStub{response: guardrailChatResponse("a forbidden answer")}
	service := wrapGuardrails(core, matchRefuseCheck{name: "moderation", on: "forbidden"})

	response, err := service.ProcessChatCompletion(context.Background(), guardrailChatRequest("clean"))
	require.Nil(t, response, "a refused answer must never reach the caller")
	require.ErrorIs(t, err, guardrails.ErrRefused)

	var refusal *guardrails.RefusalError
	require.ErrorAs(t, err, &refusal)
	require.Equal(t, guardrails.DirectionResponse, refusal.Direction)
}

// TestGuardrailsFailClosedOnAnErroringCheck holds invariant 6 at this
// seam: a configured check that cannot evaluate refuses the request.
func TestGuardrailsFailClosedOnAnErroringCheck(t *testing.T) {
	core := &guardrailCoreStub{response: guardrailChatResponse("fine")}
	service := wrapGuardrails(core, brokenCheck{})

	_, err := service.ProcessChatCompletion(context.Background(), guardrailChatRequest("anything"))
	require.ErrorIs(t, err, guardrails.ErrRefused)
	require.Zero(t, core.chatCalls, "fail closed means the request never runs")
}

// TestGuardrailsSkipAnEmptyPipeline holds the unconfigured contract: no
// checks, no wrapper work, and the stream passes through untouched.
func TestGuardrailsSkipAnEmptyPipeline(t *testing.T) {
	core := &guardrailCoreStub{
		response: guardrailChatResponse("fine"),
		events:   []*inference.StreamEvent{textDelta("fine")},
	}
	service := wrapGuardrails(core)

	response, err := service.ProcessChatCompletion(context.Background(), guardrailChatRequest("clean"))
	require.NoError(t, err)
	require.Empty(t, response.GuardrailVerdict, "no pipeline means no verdict")

	stream, err := service.ProcessChatCompletionStream(context.Background(), guardrailChatRequest("clean"))
	require.NoError(t, err)
	require.Same(t, ChatCompletionStreamResponse(core.stream), stream,
		"an empty pipeline must not wrap the stream")
}

// TestGuardrailStreamAllowsCleanEvents passes an unflagged stream through
// in order.
func TestGuardrailStreamAllowsCleanEvents(t *testing.T) {
	core := &guardrailCoreStub{events: []*inference.StreamEvent{
		{Kind: inference.StreamStart, ID: "evt"},
		textDelta("hello "),
		textDelta("world"),
		{Kind: inference.StreamEnd, ID: "evt"},
	}}
	service := wrapGuardrails(core, matchRefuseCheck{name: "moderation", on: "forbidden"})

	stream, err := service.ProcessChatCompletionStream(context.Background(), guardrailChatRequest("clean"))
	require.NoError(t, err)
	events := drainStream(t, stream)
	require.Len(t, events, 4)
	require.Equal(t, "hello world", streamText(events))
}

// TestGuardrailStreamRefusesAndWithholdsTheWindow holds the streamed
// refusal: the held text never reaches the caller, only the refusal does.
func TestGuardrailStreamRefusesAndWithholdsTheWindow(t *testing.T) {
	core := &guardrailCoreStub{events: []*inference.StreamEvent{
		{Kind: inference.StreamStart, ID: "evt"},
		textDelta("a forbidden "),
		textDelta("answer"),
		{Kind: inference.StreamEnd, ID: "evt"},
	}}
	service := wrapGuardrails(core, matchRefuseCheck{name: "moderation", on: "forbidden"})

	stream, err := service.ProcessChatCompletionStream(context.Background(), guardrailChatRequest("clean"))
	require.NoError(t, err)

	first, err := stream.Read()
	require.NoError(t, err, "events before any answer text pass through")
	require.Equal(t, inference.StreamStart, first.Kind)

	_, err = stream.Read()
	require.ErrorIs(t, err, guardrails.ErrRefused)
	_, err = stream.Read()
	require.ErrorIs(t, err, guardrails.ErrRefused, "the refusal is terminal")
}

// TestGuardrailStreamRedactsTheWindow holds the streamed redaction: the
// caller reads one synthesized delta with the rewritten window, before
// the event that closes the answer.
func TestGuardrailStreamRedactsTheWindow(t *testing.T) {
	core := &guardrailCoreStub{events: []*inference.StreamEvent{
		{Kind: inference.StreamStart, ID: "evt"},
		textDelta("the number is "),
		textDelta("555-0100"),
		{Kind: inference.StreamDelta, ID: "evt", Deltas: []inference.ChoiceDelta{{FinishReason: "stop"}}},
		{Kind: inference.StreamEnd, ID: "evt"},
	}}
	service := wrapGuardrails(core, replaceCheck{name: "pii", from: "555-0100", to: "[REDACTED]"})

	stream, err := service.ProcessChatCompletionStream(context.Background(), guardrailChatRequest("clean"))
	require.NoError(t, err)
	events := drainStream(t, stream)
	require.Equal(t, "the number is [REDACTED]", streamText(events))

	finishIndex, textIndex := -1, -1
	for i, event := range events {
		for _, delta := range event.Deltas {
			if delta.FinishReason != "" {
				finishIndex = i
			}
			if strings.Contains(delta.Text, "[REDACTED]") {
				textIndex = i
			}
		}
	}
	require.Greater(t, finishIndex, textIndex, "the redacted text lands before the finish reason")

	provider, ok := stream.(GuardrailVerdictProvider)
	require.True(t, ok)
	require.Equal(t, string(guardrails.VerdictRedact), provider.GuardrailVerdict())
}

// TestGuardrailStreamBoundsTheWindow proves a long answer is inspected in
// bounded windows rather than held whole.
func TestGuardrailStreamBoundsTheWindow(t *testing.T) {
	chunk := strings.Repeat("a", guardrailStreamWindowBytes)
	core := &guardrailCoreStub{events: []*inference.StreamEvent{
		textDelta(chunk),
		textDelta("tail"),
	}}
	service := wrapGuardrails(core, matchRefuseCheck{name: "moderation", on: "forbidden"})

	stream, err := service.ProcessChatCompletionStream(context.Background(), guardrailChatRequest("clean"))
	require.NoError(t, err)

	first, err := stream.Read()
	require.NoError(t, err)
	require.Equal(t, chunk, first.Deltas[0].Text,
		"a full window releases before the stream ends")
	rest := drainStream(t, stream)
	require.Equal(t, "tail", streamText(rest))
}

// TestApplyOutcomeRecordsARefusal holds the usage contract: a refusal
// lands on the record with its class, status, verdict, and check.
func TestApplyOutcomeRecordsARefusal(t *testing.T) {
	record := usage.Record{Timestamp: time.Now()}
	applyOutcome(&record, &guardrails.RefusalError{
		Check:     "moderation",
		Direction: guardrails.DirectionRequest,
		Reason:    "matched",
	})
	require.Equal(t, usage.StatusError, record.Status)
	require.Equal(t, http.StatusBadRequest, record.StatusCode)
	require.Equal(t, guardrailRefusalClass, record.ErrorClass)
	require.Equal(t, string(guardrails.VerdictRefuse), record.GuardrailVerdict)
	require.Equal(t, "moderation", record.GuardrailCheck)
}
