package jobs

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRecoveryResumesUnprocessedPageAndReportsFailures(t *testing.T) {
	var state recoveryState[int]
	reads := 0
	read := func(_ context.Context, cursor string) (RecoveryPage[int], error) {
		reads++
		switch cursor {
		case "":
			return RecoveryPage[int]{Next: "empty"}, nil
		case "empty":
			return RecoveryPage[int]{Records: []int{1, 2, 3}, Scanned: 3, Next: "tail"}, nil
		case "tail":
			return RecoveryPage[int]{Records: []int{4}, Scanned: 2, Failed: 1}, errors.New("corrupt record")
		default:
			t.Fatalf("unexpected cursor %q", cursor)
			return RecoveryPage[int]{}, nil
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	var seen []int
	_, err := recoverPages(ctx, &state, read, func(_ context.Context, id int, _ *SweepResult) error {
		seen = append(seen, id)
		cancel()
		return nil
	})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, []int{1}, seen)
	require.Equal(t, 2, reads)
	result, err := recoverPages(t.Context(), &state, read, func(_ context.Context, id int, _ *SweepResult) error {
		seen = append(seen, id)
		return nil
	})
	require.EqualError(t, err, "corrupt record")
	require.Equal(t, 1, result.Failed)
	require.Equal(t, []int{1, 2, 3, 4}, seen)
	require.Equal(t, 3, reads)
	require.Empty(t, state.cursor)
	require.Empty(t, state.pending)
}

func TestRecoveryBusyDoesNotWait(t *testing.T) {
	var state recoveryState[int]
	state.mu.Lock()
	defer state.mu.Unlock()
	_, err := recoverPages[int](t.Context(), &state, nil, nil)
	require.ErrorIs(t, err, ErrRecoveryBusy)
}
