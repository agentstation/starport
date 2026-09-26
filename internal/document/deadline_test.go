package document

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

// A context reports its deadline has passed by two separate means: the error it
// carries, which a timer sets, and the deadline itself, which is a time. The
// two disagree for as long as the platform's timer granularity, and Windows
// rounds to about fifteen milliseconds. A page reads faster than that.
//
// These tests run over the stub below rather than over a real timeout, because
// a real timeout only reproduces the gap on the platform that has it, and the
// bound belongs to the operator on every platform.

// lateTimerContext is a context whose deadline has passed and whose timer has
// not yet fired. It is what internal/document sees on a coarse-timer platform.
type lateTimerContext struct {
	context.Context
	deadline time.Time
}

func (c lateTimerContext) Deadline() (time.Time, bool) { return c.deadline, true }
func (c lateTimerContext) Err() error                  { return nil }

func TestAPassedDeadlineStopsTheReadBeforeItsTimerFires(t *testing.T) {
	t.Parallel()
	err := stopped(lateTimerContext{
		Context:  context.Background(),
		deadline: time.Now().Add(-time.Millisecond),
	})
	require.ErrorIs(t, err, context.DeadlineExceeded,
		"a time budget that already ran out let another page be read")
}

func TestADeadlineStillAheadReadsOn(t *testing.T) {
	t.Parallel()
	require.NoError(t, stopped(lateTimerContext{
		Context:  context.Background(),
		deadline: time.Now().Add(time.Hour),
	}))
}

func TestACallersOwnCancellationOutranksTheDeadline(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, stopped(ctx), context.Canceled,
		"a caller that went away was reported as an operator's bound")
}

func TestAReadWithNoBoundNeverStops(t *testing.T) {
	t.Parallel()
	require.NoError(t, stopped(context.Background()))
}

func TestTheTimeBudgetIsReportedAsItsOwnBound(t *testing.T) {
	for _, kind := range []string{"extractor", "caller_deadline", "caller_cancellation"} {
		t.Run(kind, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx := t.Context()
				want := ErrTimeBudgetExceeded
				switch kind {
				case "caller_deadline":
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, time.Second)
					defer cancel()
					want = context.DeadlineExceeded
				case "caller_cancellation":
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					defer cancel()
					go func() {
						time.Sleep(time.Second)
						cancel()
					}()
					want = context.Canceled
				}
				extractor := NewExtractor(Limits{MaxDuration: 2 * time.Second})
				started := time.Now()
				read := func(ctx context.Context, _ io.ReaderAt, _ int64, limits Limits) (Extraction, error) {
					if limits.MaxDuration != 2*time.Second {
						t.Fatal("reader did not receive the configured budget")
					}
					<-ctx.Done()
					return Extraction{}, ctx.Err()
				}
				result, err := extractor.extract(ctx, Input{Data: []byte("%PDF-"), Format: "pdf", Filename: "budget.pdf"}, read)
				if !errors.Is(err, want) || !strings.Contains(err.Error(), "budget.pdf") || len(result.Pages) != 0 {
					t.Fatalf("deadline result = %+v, error = %v, want %v", result, err, want)
				}
				elapsed := time.Second
				if kind == "extractor" {
					elapsed = 2 * time.Second
				} else if errors.Is(err, ErrTimeBudgetExceeded) {
					t.Fatal("caller cancellation became an extractor refusal")
				}
				if time.Since(started) != elapsed {
					t.Fatalf("deadline elapsed %s, want %s", time.Since(started), elapsed)
				}
			})
		})
	}
}
