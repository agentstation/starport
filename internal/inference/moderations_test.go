package inference

import (
	"errors"
	"testing"
)

// TestModerationRequestRefusesWhatCannotBeClassified keeps a paid error out of
// the provider. An empty input list classifies nothing, and a provider bills
// for the round trip anyway.
func TestModerationRequestRefusesWhatCannotBeClassified(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name   string
		inputs []string
	}{
		{name: "nil inputs"},
		{name: "empty input slice", inputs: []string{}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewModerationRequest("openai/omni-moderation-latest", testCase.inputs)
			if !errors.Is(err, ErrModerationInputEmpty) {
				t.Fatalf("error = %v, want ErrModerationInputEmpty", err)
			}
		})
	}
}

// TestModerationRequestCloneSharesNothing holds the rule every canonical type
// in this package holds. A retried attempt clones the request it is about to
// send again, and a shared slice would let the second attempt see the first
// one's edits.
func TestModerationRequestCloneSharesNothing(t *testing.T) {
	t.Parallel()

	request, err := NewModerationRequest("openai/omni-moderation-latest", []string{"first", "second"})
	if err != nil {
		t.Fatalf("NewModerationRequest: %v", err)
	}
	clone := request.Clone()
	request.Inputs[0] = "changed"
	if clone.Inputs[0] != "first" {
		t.Fatalf("cloned input = %q", clone.Inputs[0])
	}

	response := ModerationResponse{
		Results: []ModerationResult{
			{Flagged: true, Categories: []ModerationCategory{{Name: "violence", Flagged: true, Score: 0.9}}},
		},
	}
	clonedResponse := response.Clone()
	response.Results[0].Categories[0].Score = 0.1
	if clonedResponse.Results[0].Categories[0].Score != 0.9 {
		t.Fatalf("cloned score = %v", clonedResponse.Results[0].Categories[0].Score)
	}
}

// TestModerationResponseValidateHoldsTheAnswerShape guards the two faults that
// read as ordinary answers. A result list that answers a different number of
// inputs shifts every later verdict onto the wrong input, and a score outside
// the unit interval reads as a confident number the schema says cannot exist.
func TestModerationResponseValidateHoldsTheAnswerShape(t *testing.T) {
	t.Parallel()

	request, err := NewModerationRequest("openai/omni-moderation-latest", []string{"one", "two"})
	if err != nil {
		t.Fatalf("NewModerationRequest: %v", err)
	}
	valid := ModerationResponse{
		Results: []ModerationResult{
			{Categories: []ModerationCategory{{Name: "violence", Score: 0}}},
			{Flagged: true, Categories: []ModerationCategory{{Name: "violence", Flagged: true, Score: 1}}},
		},
	}
	if err := valid.Validate(request); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	short := ModerationResponse{Results: valid.Results[:1]}
	if err := short.Validate(request); !errors.Is(err, ErrModerationResultCountMismatch) {
		t.Fatalf("count error = %v, want ErrModerationResultCountMismatch", err)
	}

	for _, score := range []float64{-0.01, 1.01} {
		out := valid.Clone()
		out.Results[1].Categories[0].Score = score
		if err := out.Validate(request); !errors.Is(err, ErrModerationScoreOutOfRange) {
			t.Fatalf("score %v error = %v, want ErrModerationScoreOutOfRange", score, err)
		}
	}
}
