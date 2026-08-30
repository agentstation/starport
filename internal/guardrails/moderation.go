package guardrails

import (
	"context"
	"errors"
	"fmt"
)

// The moderation check classifies text with a catalog moderation model
// and refuses when any category's score reaches its threshold. The model
// call rides the Moderator seam: the composition root implements it over
// the gateway's own moderation surface, so the call routes under the
// calling account's identity and this package never learns the gateway.

// moderationCheckName is the registered name of the moderation check.
const moderationCheckName = "moderation"

// DefaultModerationThreshold refuses when a category scores at or above
// it and no per-category threshold says otherwise.
const DefaultModerationThreshold = 0.5

// CategoryScore is one harm category's score from a moderation model.
// Scores sit in the unit interval; the provider names the category.
type CategoryScore struct {
	Category string
	Score    float64
}

// Moderator classifies one text. An error means the classification could
// not run, which the pipeline treats as a refusal.
type Moderator interface {
	Moderate(ctx context.Context, text string) ([]CategoryScore, error)
}

// errModerationUnconfigured refuses a pipeline that names the moderation
// check without a moderation model to call.
var errModerationUnconfigured = errors.New(
	"the moderation check needs a configured moderation model",
)

// moderationCheck refuses text a moderation model scores at or above the
// configured threshold in any category.
type moderationCheck struct {
	moderator Moderator
	threshold float64
	overrides map[string]float64
}

// newModerationCheck builds the check from settings. A zero threshold
// takes the default; a threshold outside the unit interval is a
// configuration error.
func newModerationCheck(settings Settings) (Check, error) {
	if settings.Moderator == nil {
		return nil, errModerationUnconfigured
	}
	threshold := settings.ModerationThreshold
	if threshold == 0 {
		threshold = DefaultModerationThreshold
	}
	if threshold < 0 || threshold > 1 {
		return nil, fmt.Errorf("moderation threshold %v falls outside zero through one", settings.ModerationThreshold)
	}
	for category, override := range settings.ModerationThresholds {
		if override < 0 || override > 1 {
			return nil, fmt.Errorf("moderation threshold %v for category %s falls outside zero through one", override, category)
		}
	}
	return &moderationCheck{
		moderator: settings.Moderator,
		threshold: threshold,
		overrides: settings.ModerationThresholds,
	}, nil
}

// Name implements Check.
func (c *moderationCheck) Name() string { return moderationCheckName }

// Inspect implements Check. A moderator error propagates, and the
// pipeline turns it into a refusal: a configured check that cannot
// classify must not wave the text through unread.
func (c *moderationCheck) Inspect(ctx context.Context, content Content) (Result, error) {
	scores, err := c.moderator.Moderate(ctx, content.Text)
	if err != nil {
		return Result{}, fmt.Errorf("moderate: %w", err)
	}
	for _, score := range scores {
		threshold := c.threshold
		if override, ok := c.overrides[score.Category]; ok {
			threshold = override
		}
		if score.Score >= threshold {
			return Result{
				Verdict: VerdictRefuse,
				Reason: fmt.Sprintf("moderation category %s scored %.2f at threshold %.2f",
					score.Category, score.Score, threshold),
			}, nil
		}
	}
	return Result{Verdict: VerdictAllow}, nil
}
