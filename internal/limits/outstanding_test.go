package limits

import (
	"context"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func newTestJobMeter(t *testing.T) (*JobMeter, *atomicCounter) {
	t.Helper()
	counter := newAtomicCounter()
	meter, err := NewJobMeter(counter)
	require.NoError(t, err)
	return meter, counter
}

// TestConcurrentSubmissionsCannotBothPassABoundThatAdmitsOne is the property
// that matters here and nowhere else in this package.
//
// Two submissions arrive at once against an account with one slot left. A
// meter that read the total, decided, and then wrote would let both read a
// total that admits them and both start provider work, which is a spend
// commitment nothing has counted.
func TestConcurrentSubmissionsCannotBothPassABoundThatAdmitsOne(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	meter, _ := newTestJobMeter(t)

	var wg sync.WaitGroup
	results := make([]error, 2)
	for index := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[index] = meter.Reserve(ctx, "tenant_a", 1, 1)
		}()
	}
	wg.Wait()

	admitted := 0
	for _, err := range results {
		if err == nil {
			admitted++
			continue
		}
		require.ErrorIs(t, err, ErrTooManyOutstandingJobs)
	}
	require.Equal(t, 1, admitted)

	total, err := meter.Total(ctx, "tenant_a")
	require.NoError(t, err)
	require.Equal(t, int64(1), total, "the refused claim came back out")
}

// TestAFinishedJobLetsTheNextOneIn states what makes this a level and not a
// quota. An account at its bound is not shut out for the day: it is waiting for
// one of its own jobs to end.
func TestAFinishedJobLetsTheNextOneIn(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	meter, _ := newTestJobMeter(t)

	require.NoError(t, meter.Reserve(ctx, "tenant_a", 1, 1))
	require.ErrorIs(t, meter.Reserve(ctx, "tenant_a", 1, 1), ErrTooManyOutstandingJobs)
	require.NoError(t, meter.Release(ctx, "tenant_a", 1))
	require.NoError(t, meter.Reserve(ctx, "tenant_a", 1, 1))
}

// TestTheRefusalStatesTheBound is what the 429 body reads. An operator raising
// a limit needs the number that refused it, not the word "refused".
func TestTheRefusalStatesTheBound(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	meter, _ := newTestJobMeter(t)

	require.NoError(t, meter.Reserve(ctx, "tenant_a", 2, 2))
	err := meter.Reserve(ctx, "tenant_a", 1, 2)
	require.ErrorIs(t, err, ErrTooManyOutstandingJobs)
	require.Contains(t, err.Error(), strconv.Itoa(2))
	require.Contains(t, err.Error(), "outstanding job")
}

// TestEachAccountHoldsItsOwnSlots keeps one busy account from refusing another.
func TestEachAccountHoldsItsOwnSlots(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	meter, _ := newTestJobMeter(t)

	require.NoError(t, meter.Reserve(ctx, "tenant_a", 1, 1))
	require.NoError(t, meter.Reserve(ctx, "tenant_b", 1, 1))

	total, err := meter.Total(ctx, "tenant_b")
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
}

// TestTheTighterOfTheTwoBoundsRefusesFirst states how an account bound and a
// key bound resolve. Both read the same counter, so the smaller satisfies the
// larger and running both would be arithmetic for nothing.
func TestTheTighterOfTheTwoBoundsRefusesFirst(t *testing.T) {
	t.Parallel()

	accountBound := int64(8)
	keyBound := int64(2)

	rule, bounded := TightestOutstandingJobs(
		&Limits{OutstandingJobs: &accountBound},
		&Limits{OutstandingJobs: &keyBound},
	)
	require.True(t, bounded)
	require.Equal(t, keyBound, rule.Limit)
	require.Equal(t, ScopeKey, rule.Scope, "the refusal names the holder an operator has to change")

	rule, bounded = TightestOutstandingJobs(&Limits{OutstandingJobs: &accountBound}, nil)
	require.True(t, bounded)
	require.Equal(t, accountBound, rule.Limit)
	require.Equal(t, ScopeTenant, rule.Scope)

	_, bounded = TightestOutstandingJobs(nil, nil)
	require.False(t, bounded, "an account that states no bound is not bounded by this package")
}

// TestOutstandingJobsJoinsTheLimitVocabulary keeps the new dimension inside the
// three doors every other limit passes through.
func TestOutstandingJobsJoinsTheLimitVocabulary(t *testing.T) {
	t.Parallel()

	empty := int64(0)
	require.ErrorIs(t, (&Limits{OutstandingJobs: &empty}).Validate(), ErrInvalidOutstandingJobs)

	bound := int64(4)
	stated := &Limits{OutstandingJobs: &bound}
	require.NoError(t, stated.Validate())
	require.False(t, stated.IsZero())

	clone := stated.Clone()
	require.Equal(t, bound, *clone.OutstandingJobs)
	*clone.OutstandingJobs = 9
	require.Equal(t, int64(4), *stated.OutstandingJobs, "the clone shares no pointer")
}
