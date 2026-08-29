package jobs_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/routing"
	"github.com/agentstation/starport/internal/storage"
)

const (
	accountA = "account_a"
	accountB = "account_b"
)

func newRepository(t *testing.T) (jobs.Repository, storage.KVStore) {
	t.Helper()
	store := storage.NewMockStore()
	records, err := jobs.OpenRepository(store)
	require.NoError(t, err)
	return records, store
}

func storedJob(t *testing.T, id, account string, created time.Time) jobs.Job {
	t.Helper()
	job, err := jobs.New(
		id,
		account,
		"deepinfra",
		"black-forest-labs/FLUX-1-dev",
		routing.OperationImagesGenerations,
		created,
	)
	require.NoError(t, err)
	return job
}

func TestOpenRepositoryRefusesAnAbsentStore(t *testing.T) {
	t.Parallel()

	_, err := jobs.OpenRepository(nil)
	require.ErrorIs(t, err, jobs.ErrRepositoryRequired)
}

// TestAJobIsUnreadableByAnotherAccount holds the isolation the whole seam rests
// on. The answer is not found rather than forbidden, because a refusal would
// confirm that the identifier exists.
func TestAJobIsUnreadableByAnotherAccount(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	records, _ := newRepository(t)
	job := storedJob(t, "job_01", accountA, submitted)
	require.NoError(t, records.Create(ctx, job))

	read, err := records.Get(ctx, accountA, job.ID)
	require.NoError(t, err)
	require.Equal(t, job.ID, read.ID)

	_, err = records.Get(ctx, accountB, job.ID)
	require.ErrorIs(t, err, jobs.ErrJobNotFound)

	listed, err := records.List(ctx, accountB, 0)
	require.NoError(t, err)
	require.Empty(t, listed)

	// A delete by the wrong account reports success and removes nothing. The
	// owner still reads its job afterwards.
	require.NoError(t, records.Delete(ctx, accountB, job.ID))
	_, err = records.Get(ctx, accountA, job.ID)
	require.NoError(t, err)

	// A replace by the wrong account finds no record to replace.
	stolen := job
	stolen.Account = accountB
	require.ErrorIs(t, records.Replace(ctx, stolen), jobs.ErrJobNotFound)
}

func TestCreateRefusesAnIdentifierAlreadyInUse(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	records, _ := newRepository(t)
	job := storedJob(t, "job_01", accountA, submitted)
	require.NoError(t, records.Create(ctx, job))
	require.ErrorIs(t, records.Create(ctx, job), jobs.ErrJobExists)
}

// TestAListingKeepsItsOrderAcrossAReopen is the reason the repository sorts
// after decoding rather than trusting the scan. Storage answers in key order,
// and a key carries the identifier rather than the time.
func TestAListingKeepsItsOrderAcrossAReopen(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	records, store := newRepository(t)

	// The identifiers sort against the submission order on purpose, so a
	// listing that leaned on the key would answer backwards.
	submissions := []struct {
		id      string
		created time.Time
	}{
		{id: "job_zz", created: submitted},
		{id: "job_mm", created: submitted.Add(time.Minute)},
		{id: "job_aa", created: submitted.Add(2 * time.Minute)},
	}
	for _, submission := range submissions {
		require.NoError(t, records.Create(ctx, storedJob(t, submission.id, accountA, submission.created)))
	}

	newestFirst := []string{"job_aa", "job_mm", "job_zz"}

	listed, err := records.List(ctx, accountA, 0)
	require.NoError(t, err)
	require.Equal(t, newestFirst, identifiers(listed))

	// A second repository over the same store is what a restart looks like.
	reopened, err := jobs.OpenRepository(store)
	require.NoError(t, err)
	afterReopen, err := reopened.List(ctx, accountA, 0)
	require.NoError(t, err)
	require.Equal(t, newestFirst, identifiers(afterReopen))
}

// TestATieOnTheSubmissionTimeStillOrdersStably keeps two jobs from swapping
// places between two reads of the same unchanged data.
func TestATieOnTheSubmissionTimeStillOrdersStably(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	records, _ := newRepository(t)
	for _, id := range []string{"job_02", "job_01", "job_03"} {
		require.NoError(t, records.Create(ctx, storedJob(t, id, accountA, submitted)))
	}

	for range 3 {
		listed, err := records.List(ctx, accountA, 0)
		require.NoError(t, err)
		require.Equal(t, []string{"job_01", "job_02", "job_03"}, identifiers(listed))
	}
}

// TestAnIllegalStateWriteFailsAtTheRepository keeps the transition table in
// front of storage. A store that accepted the write would hold a record no
// state machine produced, and every later read would trust it.
func TestAnIllegalStateWriteFailsAtTheRepository(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	records, store := newRepository(t)
	job := storedJob(t, "job_01", accountA, submitted)
	require.NoError(t, records.Create(ctx, job))

	ended := submitted.Add(time.Minute)
	completed := job
	require.NoError(t, completed.Transition(jobs.JobStateCompleted, ended))
	require.NoError(t, records.Replace(ctx, completed))

	// Completed is terminal, so nothing moves out of it.
	revived := completed
	revived.State = jobs.JobStateRunning
	revived.TerminalAt = time.Time{}
	require.ErrorIs(t, records.Replace(ctx, revived), jobs.ErrIllegalTransition)

	// The record itself is valid, so the transition check is what refuses it.
	restated := completed
	restated.State = jobs.JobStateFailed
	restated.Reason = "the provider rejected the prompt"
	require.NoError(t, restated.Validate())
	require.ErrorIs(t, records.Replace(ctx, restated), jobs.ErrIllegalTransition)

	// The refused writes changed nothing.
	read, err := records.Get(ctx, accountA, job.ID)
	require.NoError(t, err)
	require.Equal(t, jobs.JobStateCompleted, read.State)
	require.Equal(t, ended, read.TerminalAt)

	// The record never reached the store, so no sweep has to undo it.
	keys, err := store.ScanWithPrefix(ctx, jobs.StoragePrefix, 100)
	require.NoError(t, err)
	require.Len(t, keys, 1)
}

// TestReplaceAcceptsAChangeThatKeepsTheState covers the poll that answers the
// same word twice. It is not a transition, and refusing it would stop a
// provider identifier from ever reaching the record.
func TestReplaceAcceptsAChangeThatKeepsTheState(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	records, _ := newRepository(t)
	job := storedJob(t, "job_01", accountA, submitted)
	require.NoError(t, records.Create(ctx, job))

	adopted := job
	require.NoError(t, adopted.AdoptProviderJob("video_4f19c0a7"))
	require.NoError(t, records.Replace(ctx, adopted))

	read, err := records.Get(ctx, accountA, job.ID)
	require.NoError(t, err)
	require.Equal(t, jobs.JobStateQueued, read.State)
	require.True(t, read.HasProviderJob(), "the provider job identifier did not survive the round trip")
}

func TestReplaceFindsNoRecordForAnUnknownJob(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	records, _ := newRepository(t)
	require.ErrorIs(t, records.Replace(ctx, storedJob(t, "job_01", accountA, submitted)), jobs.ErrJobNotFound)
}

func TestARepeatedDeleteIsSafe(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	records, _ := newRepository(t)
	job := storedJob(t, "job_01", accountA, submitted)
	require.NoError(t, records.Create(ctx, job))
	require.NoError(t, records.Delete(ctx, accountA, job.ID))
	require.NoError(t, records.Delete(ctx, accountA, job.ID))
	_, err := records.Get(ctx, accountA, job.ID)
	require.ErrorIs(t, err, jobs.ErrJobNotFound)
}

func TestGetRefusesAnEmptyAccountOrIdentifier(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	records, _ := newRepository(t)
	_, err := records.Get(ctx, "", "job_01")
	require.ErrorIs(t, err, jobs.ErrJobNotFound)
	_, err = records.Get(ctx, accountA, " ")
	require.ErrorIs(t, err, jobs.ErrJobNotFound)
}

// TestACorruptRecordReportsItselfRatherThanDecodingHalfway keeps a hand-edited
// or half-written value from becoming a job with an empty account.
func TestACorruptRecordReportsItselfRatherThanDecodingHalfway(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	records, store := newRepository(t)
	job := storedJob(t, "job_01", accountA, submitted)
	require.NoError(t, records.Create(ctx, job))

	keys, err := store.ScanWithPrefix(ctx, jobs.StoragePrefix, 100)
	require.NoError(t, err)
	require.Len(t, keys, 1)

	for _, corrupt := range [][]byte{
		[]byte("{"),
		[]byte(`{"schema_version":2,"id":"job_01","account":"account_a"}`),
		[]byte(`{"schema_version":1,"id":"job_01","account":"","state":"queued"}`),
	} {
		require.NoError(t, store.Set(ctx, keys[0], corrupt))
		_, err := records.Get(ctx, accountA, job.ID)
		require.ErrorIs(t, err, jobs.ErrCorruptRecord)
	}
}

func identifiers(records []jobs.Job) []string {
	ids := make([]string, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.ID)
	}
	return ids
}
