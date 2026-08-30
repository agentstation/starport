package inference

import (
	"errors"
	"fmt"
)

// Moderation classifies text against a fixed set of harm categories and
// answers with a score for each one. The provider names the categories, and
// no two providers name the same set, so the canonical shape carries each
// category by the name the provider gave it rather than pinning a list this
// gateway would have to grow.

// ErrModerationInputEmpty reports a moderation request with nothing to
// classify.
var ErrModerationInputEmpty = errors.New("a moderation request needs at least one input")

// ModerationRequest is the canonical moderation request.
type ModerationRequest struct {
	Model string
	// Inputs is the list of texts to classify, in the order the caller
	// supplied. A result answers the input at the same position, so the order
	// is part of the request's meaning rather than a presentation detail.
	Inputs []string
}

// NewModerationRequest builds a canonical moderation request and refuses the
// one request that cannot be answered. An empty input list classifies
// nothing, and it would reach a provider as a paid error, so it stops here.
func NewModerationRequest(model string, inputs []string) (ModerationRequest, error) {
	if len(inputs) == 0 {
		return ModerationRequest{}, ErrModerationInputEmpty
	}
	return ModerationRequest{
		Model:  model,
		Inputs: append([]string(nil), inputs...),
	}, nil
}

// Clone returns an independent moderation request copy.
func (r ModerationRequest) Clone() ModerationRequest {
	clone := r
	clone.Inputs = append([]string(nil), r.Inputs...)
	return clone
}

// ModerationCategory is one harm category's verdict on one input.
type ModerationCategory struct {
	// Name is the category name exactly as the provider states it.
	Name string
	// Flagged is the provider's own threshold decision for this category.
	Flagged bool
	// Score is how strongly the input matches the category. Providers
	// normalize it to the unit interval, and the gateway does not rescale it.
	Score float64
}

// ModerationResult is every category's verdict on one input.
type ModerationResult struct {
	// Flagged reports whether any category flagged the input.
	Flagged bool
	// Categories holds one verdict per category, in the order the provider
	// stated them.
	Categories []ModerationCategory
}

// ModerationResponse is the canonical moderation response.
type ModerationResponse struct {
	// ID is the provider's identifier for this classification, kept so the
	// wire answer a caller reads matches the record the provider holds.
	ID    string
	Model string
	// Results holds one result per request input, at the same position.
	Results []ModerationResult
	Usage   Usage
}

// Clone returns an independent moderation response copy.
func (r ModerationResponse) Clone() ModerationResponse {
	clone := r
	clone.Results = make([]ModerationResult, len(r.Results))
	for i, result := range r.Results {
		copied := result
		copied.Categories = append([]ModerationCategory(nil), result.Categories...)
		clone.Results[i] = copied
	}
	return clone
}

// ErrModerationResultCountMismatch reports a response whose result count
// disagrees with the request's input count. A caller reads each result by
// position, so a shorter or longer list silently answers the wrong input.
var ErrModerationResultCountMismatch = errors.New(
	"a moderation response must answer every request input exactly once",
)

// ErrModerationScoreOutOfRange reports a category score outside the unit
// interval. Every moderation provider publishes a normalized score, so a
// number outside it is a decoding fault rather than an unusual answer.
var ErrModerationScoreOutOfRange = errors.New(
	"a moderation category score falls outside zero through one",
)

// Validate refuses a response that cannot describe the request that produced
// it. A codec calls it before writing, because both faults produce an answer
// that reads as ordinary: a missing result shifts every later verdict onto
// the wrong input, and a score outside the unit interval reads as a
// confident number the schema says cannot exist.
func (r ModerationResponse) Validate(request ModerationRequest) error {
	if len(r.Results) != len(request.Inputs) {
		return fmt.Errorf(
			"%w: %d results for %d inputs",
			ErrModerationResultCountMismatch, len(r.Results), len(request.Inputs),
		)
	}
	for index, result := range r.Results {
		for _, category := range result.Categories {
			if category.Score < 0 || category.Score > 1 {
				return fmt.Errorf(
					"%w: input %d category %s scored %v",
					ErrModerationScoreOutOfRange, index, category.Name, category.Score,
				)
			}
		}
	}
	return nil
}
