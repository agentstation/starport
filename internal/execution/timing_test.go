package execution

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/routing"
)

// TestStreamingCarriesNoElapsedDeadlineAfterFirstByte proves route timing is
// route-specific.
//
// The elapsed budget bounds route selection. It ends a stream that delivers no
// first byte, and it releases as soon as one arrives, because a caller that
// reads a stream must not have it cut in half. An ordinary JSON route keeps the
// same budget from end to end.
func TestStreamingCarriesNoElapsedDeadlineAfterFirstByte(t *testing.T) {
	tests := []struct {
		name        string
		events      int
		advance     time.Duration
		wantEvents  int
		wantDelivry bool
	}{
		{
			name:        "one event arrives inside the budget",
			events:      1,
			advance:     0,
			wantEvents:  1,
			wantDelivry: true,
		},
		{
			name:        "the stream outlives the budget it committed inside",
			events:      4,
			advance:     time.Hour,
			wantEvents:  4,
			wantDelivry: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clock := newFakeClock()
			config := testConfig()
			config.MaxElapsed = 5 * time.Second
			executor := newTestExecutor(t, clock, config)

			var attemptCtx context.Context
			events := make([]*inference.StreamEvent, 0, test.events)
			for range test.events {
				events = append(events, &inference.StreamEvent{Kind: inference.StreamDelta})
			}
			stream, err := executor.StartChatStream(
				context.Background(),
				testPlan(t, "provider-a/model"),
				func(ctx context.Context, _ routing.Attempt) (Stream, *failure.Failure, AttemptAction) {
					attemptCtx = ctx
					return &scriptedStream{events: events}, nil, AttemptActionDefault
				},
			)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, stream.Close()) })

			// The provider stream never carries the elapsed deadline. The
			// budget lives in the selection watch, which the first byte ends.
			_, hasDeadline := attemptCtx.Deadline()
			require.False(t, hasDeadline)

			first, err := stream.Read()
			require.NoError(t, err)
			require.NotNil(t, first)
			require.True(t, stream.Committed())
			require.NoError(t, attemptCtx.Err())

			// Time now passes the whole budget. A committed stream keeps
			// delivering, because nothing bounds it after the first byte.
			clock.Advance(test.advance)
			delivered := 1
			for {
				event, readErr := stream.Read()
				if event == nil {
					require.ErrorIs(t, readErr, io.EOF)
					break
				}
				require.NoError(t, readErr)
				delivered++
			}
			require.Equal(t, test.wantEvents, delivered)
			require.Equal(t, test.wantDelivry, delivered > 0)
		})
	}
}

// TestJSONRouteKeepsTheElapsedBudget proves the ordinary route keeps its bound.
// Only streaming releases the budget, and only after it committed.
func TestJSONRouteKeepsTheElapsedBudget(t *testing.T) {
	clock := newFakeClock()
	config := testConfig()
	config.MaxElapsed = 5 * time.Second
	executor := newTestExecutor(t, clock, config)

	_, err := executor.ExecuteChat(
		context.Background(),
		testPlan(t, "provider-a/model"),
		func(context.Context, routing.Attempt) (*inference.ChatResponse, *failure.Failure, AttemptAction) {
			clock.Advance(6 * time.Second)
			return &inference.ChatResponse{ID: "late"}, nil, AttemptActionDefault
		},
	)
	var executionError *Error
	require.ErrorAs(t, err, &executionError)
	require.Equal(t, failure.Timeout, executionError.Failure.Kind())
}
