package limits

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// atomicCounter is an in-memory counter whose increment and decrement are
// atomic against concurrent callers, which is exactly what the meter's
// contract asks of a durable store.
//
// The lock is the point of the fake. A meter that read the total, decided, and
// then wrote would still pass a serial test, and would still admit two uploads
// that together pass the bound. Only a counter that is genuinely atomic can
// tell the two implementations apart.
type atomicCounter struct {
	mu     sync.Mutex
	values map[string]int64
	fail   error
}

func newAtomicCounter() *atomicCounter {
	return &atomicCounter{values: make(map[string]int64)}
}

func (c *atomicCounter) Increment(_ context.Context, key string, delta int64) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail != nil {
		return 0, c.fail
	}
	c.values[key] += delta
	return c.values[key], nil
}

func (c *atomicCounter) Decrement(ctx context.Context, key string, delta int64) (int64, error) {
	return c.Increment(ctx, key, -delta)
}

func newTestMeter(t *testing.T) (*StorageMeter, *atomicCounter) {
	t.Helper()
	counter := newAtomicCounter()
	meter, err := NewStorageMeter(counter)
	require.NoError(t, err)
	return meter, counter
}

// TestConcurrentReservationsCannotBothPassABoundThatAdmitsOne holds FIL-V17.
//
// Two uploads arrive at once and each one fits on its own. The bound admits a
// single one of them, and the pair must not both land. This is the property
// that forces the meter to claim before it checks: a read-then-write meter
// lets both callers read a total of zero, both decide they fit, and both
// write.
func TestConcurrentReservationsCannotBothPassABoundThatAdmitsOne(t *testing.T) {
	t.Parallel()
	meter, counter := newTestMeter(t)
	ctx := context.Background()

	const (
		size    = 600
		bound   = 1000
		callers = 8
	)

	var wg sync.WaitGroup
	results := make([]error, callers)
	start := make(chan struct{})
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i] = meter.Reserve(ctx, "account-a", size, bound)
		}()
	}
	close(start)
	wg.Wait()

	admitted := 0
	for _, err := range results {
		if err == nil {
			admitted++
			continue
		}
		require.ErrorIs(t, err, ErrStorageFull)
	}
	require.Equal(t, 1, admitted, "the bound admits one reservation of this size")

	// Every refusal gave its claim back, so the total counts only the bytes
	// that landed. A refusal that kept its claim would leave the holder
	// permanently short by the size of every upload it ever tried.
	total, err := meter.Total(ctx, "account-a")
	require.NoError(t, err)
	require.Equal(t, int64(size), total)
	require.Equal(t, int64(size), counter.values[StoredBytesPrefix+"account-a"])
}

// TestReleaseLowersTheTotalByTheFileSize states the other half of the pair. A
// delete has to give the bytes back, or an account that stayed under its bound
// would still run out of room after enough uploads and deletes.
func TestReleaseLowersTheTotalByTheFileSize(t *testing.T) {
	t.Parallel()
	meter, _ := newTestMeter(t)
	ctx := context.Background()

	require.NoError(t, meter.Reserve(ctx, "account-a", 400, 1000))
	require.NoError(t, meter.Reserve(ctx, "account-a", 500, 1000))
	total, err := meter.Total(ctx, "account-a")
	require.NoError(t, err)
	require.Equal(t, int64(900), total)

	require.NoError(t, meter.Release(ctx, "account-a", 400))
	total, err = meter.Total(ctx, "account-a")
	require.NoError(t, err)
	require.Equal(t, int64(500), total)

	// The room the delete gave back is usable room, not just a smaller number.
	require.NoError(t, meter.Reserve(ctx, "account-a", 400, 1000))
}

// TestEachHolderCountsOnlyItsOwnBytes states the isolation the bound depends
// on. One shared counter would let a busy account exhaust every other account.
func TestEachHolderCountsOnlyItsOwnBytes(t *testing.T) {
	t.Parallel()
	meter, _ := newTestMeter(t)
	ctx := context.Background()

	require.NoError(t, meter.Reserve(ctx, "account-a", 900, 1000))
	require.NoError(t, meter.Reserve(ctx, "account-b", 900, 1000))

	total, err := meter.Total(ctx, "account-b")
	require.NoError(t, err)
	require.Equal(t, int64(900), total)
	require.ErrorIs(t, meter.Reserve(ctx, "account-a", 200, 1000), ErrStorageFull)
	require.NoError(t, meter.Reserve(ctx, "account-b", 100, 1000))
}

// TestAnUnboundedHolderStillCountsItsBytes states why the meter runs even when
// nothing bounds the holder. An operator that sets a bound later would
// otherwise read a total of zero over storage that is already full.
func TestAnUnboundedHolderStillCountsItsBytes(t *testing.T) {
	t.Parallel()
	meter, _ := newTestMeter(t)
	ctx := context.Background()

	require.NoError(t, meter.Reserve(ctx, "account-a", 5000, 0))
	total, err := meter.Total(ctx, "account-a")
	require.NoError(t, err)
	require.Equal(t, int64(5000), total)
}

// TestReserveReportsACounterFailure states that an unreachable counter refuses
// the write. Admitting the upload would spend storage the meter cannot see,
// and the total would understate the holder from then on.
func TestReserveReportsACounterFailure(t *testing.T) {
	t.Parallel()
	meter, counter := newTestMeter(t)
	counter.fail = errors.New("counter unreachable")

	require.Error(t, meter.Reserve(context.Background(), "account-a", 100, 1000))
}

// TestTheMeterNamesItsHolder states the guard. A reservation under an empty
// holder would put every anonymous caller on one counter.
func TestTheMeterNamesItsHolder(t *testing.T) {
	t.Parallel()
	meter, _ := newTestMeter(t)
	ctx := context.Background()

	require.ErrorIs(t, meter.Reserve(ctx, "", 100, 1000), ErrInvalidHolder)
	require.ErrorIs(t, meter.Release(ctx, "", 100), ErrInvalidHolder)
	_, err := meter.Total(ctx, "")
	require.ErrorIs(t, err, ErrInvalidHolder)

	_, err = NewStorageMeter(nil)
	require.ErrorIs(t, err, ErrCounterRequired)
}

// TestStoredBytesJoinsTheLimitVocabulary states that the new bound behaves
// like every other one: it validates, it clones deeply, and it counts toward
// whether a holder carries limits at all.
func TestStoredBytesJoinsTheLimitVocabulary(t *testing.T) {
	t.Parallel()
	bound := int64(1 << 20)
	limits := &Limits{StoredBytes: &bound}

	require.NoError(t, limits.Validate())
	require.False(t, limits.IsZero())

	clone := limits.Clone()
	require.NotSame(t, limits.StoredBytes, clone.StoredBytes)
	require.Equal(t, bound, *clone.StoredBytes)

	zero := int64(0)
	require.ErrorIs(t, (&Limits{StoredBytes: &zero}).Validate(), ErrInvalidStoredBytes)
	negative := int64(-1)
	require.ErrorIs(t, (&Limits{StoredBytes: &negative}).Validate(), ErrInvalidStoredBytes)
}
