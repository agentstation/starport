package guardrails

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// stubModerator answers scripted scores and records what it classified.
type stubModerator struct {
	scores []CategoryScore
	err    error
	seen   []string
}

func (m *stubModerator) Moderate(_ context.Context, text string) ([]CategoryScore, error) {
	m.seen = append(m.seen, text)
	return m.scores, m.err
}

func newTestModerationCheck(t *testing.T, settings Settings) Check {
	t.Helper()
	check, err := newModerationCheck(settings)
	require.NoError(t, err)
	return check
}

// TestModerationRefusesAtOrAboveThreshold holds the refusal contract: a
// category at the threshold refuses, and the reason names the category
// and both numbers.
func TestModerationRefusesAtOrAboveThreshold(t *testing.T) {
	moderator := &stubModerator{scores: []CategoryScore{
		{Category: "hate", Score: 0.1},
		{Category: "violence", Score: 0.5},
	}}
	check := newTestModerationCheck(t, Settings{Moderator: moderator})

	result, err := check.Inspect(context.Background(), Content{Direction: DirectionRequest, Text: "provocation"})
	require.NoError(t, err)
	require.Equal(t, VerdictRefuse, result.Verdict)
	require.Equal(t, "moderation category violence scored 0.50 at threshold 0.50", result.Reason)
	require.Equal(t, []string{"provocation"}, moderator.seen)
}

// TestModerationAllowsBelowThreshold holds the pass side: every category
// under its threshold lets the text through unchanged.
func TestModerationAllowsBelowThreshold(t *testing.T) {
	moderator := &stubModerator{scores: []CategoryScore{
		{Category: "hate", Score: 0.2},
		{Category: "violence", Score: 0.49},
	}}
	check := newTestModerationCheck(t, Settings{Moderator: moderator})

	result, err := check.Inspect(context.Background(), Content{Direction: DirectionResponse, Text: "mild"})
	require.NoError(t, err)
	require.Equal(t, VerdictAllow, result.Verdict)
}

// TestModerationHonorsPerCategoryThresholds holds the override contract:
// a named category reads its own threshold, every other one the default.
func TestModerationHonorsPerCategoryThresholds(t *testing.T) {
	moderator := &stubModerator{scores: []CategoryScore{{Category: "self-harm", Score: 0.3}}}
	check := newTestModerationCheck(t, Settings{
		Moderator:            moderator,
		ModerationThresholds: map[string]float64{"self-harm": 0.2},
	})

	result, err := check.Inspect(context.Background(), Content{Direction: DirectionResponse, Text: "worrying"})
	require.NoError(t, err)
	require.Equal(t, VerdictRefuse, result.Verdict)
	require.Equal(t, "moderation category self-harm scored 0.30 at threshold 0.20", result.Reason)
}

// TestModerationFailsClosedThroughThePipeline holds invariant six end to
// end: a moderator that cannot classify refuses the text instead of
// waving it through unread.
func TestModerationFailsClosedThroughThePipeline(t *testing.T) {
	moderator := &stubModerator{err: errors.New("moderation model unreachable")}
	pipeline := NewPipeline(newTestModerationCheck(t, Settings{Moderator: moderator}))

	_, verdict, err := pipeline.Inspect(context.Background(), DirectionRequest, "anything")
	require.Equal(t, VerdictRefuse, verdict)
	require.ErrorIs(t, err, ErrRefused)
	var refusal *RefusalError
	require.ErrorAs(t, err, &refusal)
	require.Equal(t, "moderation", refusal.Check)
	require.Contains(t, refusal.Reason, "moderation model unreachable")
}

// TestModerationCheckNeedsAModerator holds the startup contract: naming
// the check without a moderation model refuses to build.
func TestModerationCheckNeedsAModerator(t *testing.T) {
	_, err := BuildPipeline([]string{"moderation"}, Settings{})
	require.ErrorContains(t, err, "needs a configured moderation model")
}

// TestModerationRejectsAThresholdOutsideTheUnitInterval holds the
// configuration contract on both the default and a category override.
func TestModerationRejectsAThresholdOutsideTheUnitInterval(t *testing.T) {
	moderator := &stubModerator{}
	_, err := newModerationCheck(Settings{Moderator: moderator, ModerationThreshold: 1.5})
	require.ErrorContains(t, err, "falls outside zero through one")

	_, err = newModerationCheck(Settings{
		Moderator:            moderator,
		ModerationThresholds: map[string]float64{"hate": -0.1},
	})
	require.ErrorContains(t, err, "falls outside zero through one")
}

// TestBuildPipelineBuildsTheBuiltins holds the registry contract: the
// two shipped checks resolve by name, in the order written.
func TestBuildPipelineBuildsTheBuiltins(t *testing.T) {
	pipeline, err := BuildPipeline([]string{"pii", "moderation"}, Settings{Moderator: &stubModerator{}})
	require.NoError(t, err)
	require.Equal(t, 2, pipeline.Len())
}
