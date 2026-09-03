package catalog

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRefreshTimeoutBoundsOnlyWhenSet proves the refresh cap is the deployment
// setting alone. A zero timeout hands the run a context with no deadline, so
// the transfer bounds decide when a transfer that does not progress ends. A
// positive timeout caps the run at that value.
func TestRefreshTimeoutBoundsOnlyWhenSet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		timeout      time.Duration
		wantDeadline bool
	}{
		{name: "zero adds no cap", timeout: 0},
		{name: "a negative value adds no cap", timeout: -time.Second},
		{
			name:         "a positive value caps the run",
			timeout:      time.Minute,
			wantDeadline: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			bounded, cancel := refreshContext(t.Context(), test.timeout)
			defer cancel()
			deadline, ok := bounded.Deadline()
			assert.Equal(t, test.wantDeadline, ok)
			if test.wantDeadline {
				assert.WithinDuration(
					t, time.Now().Add(test.timeout), deadline, time.Minute,
				)
			}
		})
	}
}

// TestOperationTimeoutBoundsOnlyWhenSet proves the operation registry adds a
// deadline only when the deployment sets one. Every run still receives a
// cancel, so the cancel route and Close end a run an operator no longer wants.
func TestOperationTimeoutBoundsOnlyWhenSet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		options      []OperationOption
		wantDeadline bool
	}{
		{name: "no timeout option"},
		{
			name:    "a zero timeout",
			options: []OperationOption{WithOperationTimeout(0)},
		},
		{
			name:         "a positive timeout",
			options:      []OperationOption{WithOperationTimeout(time.Minute)},
			wantDeadline: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			registry := NewOperations(test.options...)
			t.Cleanup(registry.Close)

			observed := make(chan bool, 1)
			operation, _ := registry.Submit(
				KindCatalogUpdate,
				func(ctx context.Context) (OperationResult, error) {
					_, ok := ctx.Deadline()
					observed <- ok
					return OperationResult{GenerationID: "gen-1"}, nil
				},
			)
			require.NotEmpty(t, operation.ID)
			select {
			case ok := <-observed:
				assert.Equal(t, test.wantDeadline, ok)
			case <-time.After(5 * time.Second):
				t.Fatal("the operation did not run")
			}
			waitForState(t, registry, operation.ID, OperationSucceeded)
		})
	}
}
