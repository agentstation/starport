package guardrails

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// stubCheck answers one scripted result and records what it saw.
type stubCheck struct {
	name   string
	result Result
	err    error
	seen   []Content
}

func (c *stubCheck) Name() string { return c.name }

func (c *stubCheck) Inspect(_ context.Context, content Content) (Result, error) {
	c.seen = append(c.seen, content)
	return c.result, c.err
}

// TestPipelineAllowsCleanText holds the base case: allowing checks pass
// the text through unchanged.
func TestPipelineAllowsCleanText(t *testing.T) {
	check := &stubCheck{name: "pii", result: Result{Verdict: VerdictAllow}}
	pipeline := NewPipeline(check)

	text, verdict, err := pipeline.Inspect(context.Background(), DirectionRequest, "hello")
	require.NoError(t, err)
	require.Equal(t, VerdictAllow, verdict)
	require.Equal(t, "hello", text)
	require.Equal(t, []Content{{Direction: DirectionRequest, Text: "hello"}}, check.seen)
}

// TestPipelineComposesRedactions holds the ordering contract: the second
// check reads the text as the first one rewrote it.
func TestPipelineComposesRedactions(t *testing.T) {
	first := &stubCheck{name: "pii", result: Result{Verdict: VerdictRedact, Redacted: "call [REDACTED]"}}
	second := &stubCheck{name: "tone", result: Result{Verdict: VerdictAllow}}
	pipeline := NewPipeline(first, second)

	text, verdict, err := pipeline.Inspect(context.Background(), DirectionResponse, "call 555-0100")
	require.NoError(t, err)
	require.Equal(t, VerdictRedact, verdict)
	require.Equal(t, "call [REDACTED]", text)
	require.Equal(t, "call [REDACTED]", second.seen[0].Text,
		"the second check must read the redacted text")
}

// TestPipelineRefusesWithTheCheckName pins the refusal shape: the error
// names the check, the direction, and the reason, under ErrRefused.
func TestPipelineRefusesWithTheCheckName(t *testing.T) {
	refusing := &stubCheck{name: "policy", result: Result{Verdict: VerdictRefuse, Reason: "category above threshold"}}
	unreached := &stubCheck{name: "later"}
	pipeline := NewPipeline(refusing, unreached)

	_, verdict, err := pipeline.Inspect(context.Background(), DirectionRequest, "text")
	require.Equal(t, VerdictRefuse, verdict)
	require.ErrorIs(t, err, ErrRefused)

	var refusal *RefusalError
	require.ErrorAs(t, err, &refusal)
	require.Equal(t, "policy", refusal.Check)
	require.Equal(t, DirectionRequest, refusal.Direction)
	require.Equal(t, "category above threshold", refusal.Reason)
	require.Empty(t, unreached.seen, "a refusal must stop the pipeline")
}

// TestPipelineFailClosedOnAnErroringCheck holds the fail-closed
// invariant: a configured check that cannot evaluate refuses, never
// allows.
func TestPipelineFailClosedOnAnErroringCheck(t *testing.T) {
	broken := &stubCheck{name: "policy", err: errors.New("model unreachable")}
	unreached := &stubCheck{name: "later"}
	pipeline := NewPipeline(broken, unreached)

	_, verdict, err := pipeline.Inspect(context.Background(), DirectionResponse, "text")
	require.Equal(t, VerdictRefuse, verdict)
	require.ErrorIs(t, err, ErrRefused)

	var refusal *RefusalError
	require.ErrorAs(t, err, &refusal)
	require.Equal(t, "policy", refusal.Check)
	require.Contains(t, refusal.Reason, "model unreachable")
	require.Empty(t, unreached.seen, "a fail-closed refusal must stop the pipeline")
}

// TestPipelineFailClosedOnAnUnknownVerdict extends the same invariant to
// a check that answers a verdict this vocabulary does not hold.
func TestPipelineFailClosedOnAnUnknownVerdict(t *testing.T) {
	confused := &stubCheck{name: "custom", result: Result{Verdict: "maybe"}}
	pipeline := NewPipeline(confused)

	_, verdict, err := pipeline.Inspect(context.Background(), DirectionRequest, "text")
	require.Equal(t, VerdictRefuse, verdict)
	require.ErrorIs(t, err, ErrRefused)
}

// TestNilPipelineAllowsEverything holds the unconfigured contract: no
// pipeline means no checks and no cost.
func TestNilPipelineAllowsEverything(t *testing.T) {
	var pipeline *Pipeline
	require.Zero(t, pipeline.Len())

	text, verdict, err := pipeline.Inspect(context.Background(), DirectionRequest, "hello")
	require.NoError(t, err)
	require.Equal(t, VerdictAllow, verdict)
	require.Equal(t, "hello", text)
}

// TestBuildPipelineRefusesAnUnknownName holds the startup contract: a
// configured check no build ships is an error, not a silent skip.
func TestBuildPipelineRefusesAnUnknownName(t *testing.T) {
	_, err := BuildPipeline([]string{"no-such-check"})
	require.ErrorIs(t, err, ErrUnknownCheck)
	require.Contains(t, err.Error(), "no-such-check")
}

// TestStaticPolicyAnswersEveryAccount holds the policy seam: the static
// policy serves one pipeline regardless of account.
func TestStaticPolicyAnswersEveryAccount(t *testing.T) {
	pipeline := NewPipeline(&stubCheck{name: "pii", result: Result{Verdict: VerdictAllow}})
	policy := StaticPolicy{Pipeline: pipeline}
	require.Same(t, pipeline, policy.PipelineFor("acct_a"))
	require.Same(t, pipeline, policy.PipelineFor("acct_b"))
}
