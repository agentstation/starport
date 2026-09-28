package jobslots

import (
	"encoding/json/v2"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/routing"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func attachmentJob(t *testing.T, id string, at time.Time) jobs.Job {
	t.Helper()
	job, err := jobs.New(id, "account", "provider", "model", routing.OperationVideosGenerations, at)
	require.NoError(t, err)
	job.SlotID = id
	job.SubmissionPending = true
	job.CatalogGeneration = "test-generation"
	return job
}

func TestAttachmentAndRecoveryResolveLostPublication(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(fmt.Sprint(after), func(t *testing.T) {
			repotest.Run(t, func(t *testing.T, backing storage.KVStore) {
				at := time.Now().UTC()
				meter, err := Open(backing)
				require.NoError(t, err)
				meter.now = func() time.Time { return at }
				job := attachmentJob(t, "job", at)
				require.NoError(t, meter.Reserve(t.Context(), job.Account, job.SlotID, job.ID, "video", 1))
				attachment, err := meter.Attachment(t.Context(), job.Account, job.SlotID, job.ID, "video")
				require.NoError(t, err)
				uncertain, err := jobs.OpenRepository(uncertainWrite{backing, after})
				require.NoError(t, err)
				require.Error(t, uncertain.CreateClaimed(t.Context(), job, attachment))
				reopened, err := Open(backing)
				require.NoError(t, err)
				reopened.now = func() time.Time { return at.Add(PendingGrace + time.Second) }
				result, err := reopened.RecoverPending(t.Context())
				require.NoError(t, err)
				require.Equal(t, map[bool]int{false: 1, true: 0}[after], result.Released)
				total, err := reopened.Total(t.Context(), job.Account)
				require.NoError(t, err)
				require.Equal(t, map[bool]int64{false: 0, true: 1}[after], total)
				kept, err := reopened.Get(t.Context(), job.Account, job.SlotID)
				require.NoError(t, err)
				require.Equal(t, after, kept.Attached)
				require.Equal(t, !after, kept.Released)
				records, err := jobs.OpenRepository(backing)
				require.NoError(t, err)
				_, err = records.Get(t.Context(), job.Account, job.ID)
				if after {
					require.NoError(t, err)
				} else {
					require.ErrorIs(t, err, jobs.ErrJobNotFound)
					require.ErrorIs(t, records.CreateClaimed(t.Context(), job, attachment), jobs.ErrClaimUnavailable)
				}
				again, err := reopened.RecoverPending(t.Context())
				require.NoError(t, err)
				require.Zero(t, again.Released)
			})
		})
	}
}

func TestAttachmentCompetesAtomicallyWithRecovery(t *testing.T) {
	repotest.Run(t, func(t *testing.T, backing storage.KVStore) {
		at := time.Now().UTC()
		meter, err := Open(backing)
		require.NoError(t, err)
		records, err := jobs.OpenRepository(backing)
		require.NoError(t, err)
		for i := range 12 {
			meter.now = func() time.Time { return at }
			job := attachmentJob(t, fmt.Sprintf("job-%d", i), at)
			require.NoError(t, meter.Reserve(t.Context(), job.Account, job.SlotID, job.ID, "video", 0))
			attachment, err := meter.Attachment(t.Context(), job.Account, job.SlotID, job.ID, "video")
			require.NoError(t, err)
			meter.now = func() time.Time { return at.Add(PendingGrace + time.Second) }
			var wg sync.WaitGroup
			var published, recovered error
			wg.Go(func() { published = records.CreateClaimed(t.Context(), job, attachment) })
			wg.Go(func() { _, recovered = meter.RecoverPending(t.Context()) })
			wg.Wait()
			require.NoError(t, recovered)
			claim, err := meter.Get(t.Context(), job.Account, job.SlotID)
			require.NoError(t, err)
			_, readErr := records.Get(t.Context(), job.Account, job.ID)
			if published == nil {
				require.NoError(t, readErr)
				require.True(t, claim.Attached)
				require.False(t, claim.Released)
				require.NoError(t, meter.Release(t.Context(), job.Account, job.SlotID))
			} else {
				require.ErrorIs(t, published, jobs.ErrClaimUnavailable)
				require.ErrorIs(t, readErr, jobs.ErrJobNotFound)
				require.False(t, claim.Attached)
				require.True(t, claim.Released)
			}
			total, err := meter.Total(t.Context(), job.Account)
			require.NoError(t, err)
			require.Zero(t, total)
		}
	})
}

func TestRecoveryRetainsRecentClaimsAndOriginalTimestamp(t *testing.T) {
	repotest.Run(t, func(t *testing.T, backing storage.KVStore) {
		at := time.Now().UTC()
		meter, err := Open(backing)
		require.NoError(t, err)
		meter.now = func() time.Time { return at }
		require.NoError(t, meter.Reserve(t.Context(), "account", "recent", "recent-job", "batch", 0))
		first, err := meter.RecoverPending(t.Context())
		require.NoError(t, err)
		require.Zero(t, first.Released)
		original, err := meter.Get(t.Context(), "account", "recent")
		require.NoError(t, err)
		meter.now = func() time.Time { return at.Add(PendingGrace / 2) }
		require.NoError(t, meter.Reserve(t.Context(), "account", "recent", "recent-job", "batch", 0))
		retried, err := meter.Get(t.Context(), "account", "recent")
		require.NoError(t, err)
		require.Equal(t, original.CreatedAt, retried.CreatedAt)
		_, err = meter.Attachment(t.Context(), "account", "recent", "different-job", "batch")
		require.ErrorIs(t, err, ErrClaimConflict)
	})
}

func TestPendingRecoverySurvivesLostAcknowledgement(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(fmt.Sprint(after), func(t *testing.T) {
			repotest.Run(t, func(t *testing.T, backing storage.KVStore) {
				at := time.Now().UTC()
				meter, err := Open(backing)
				require.NoError(t, err)
				meter.now = func() time.Time { return at }
				require.NoError(t, meter.Reserve(t.Context(), "account", "orphan", "orphan-job", "video", 0))
				job := attachmentJob(t, "active", at)
				require.NoError(t, meter.Reserve(t.Context(), job.Account, job.SlotID, job.ID, "video", 0))
				attachment, err := meter.Attachment(t.Context(), job.Account, job.SlotID, job.ID, "video")
				require.NoError(t, err)
				records, err := jobs.OpenRepository(backing)
				require.NoError(t, err)
				require.NoError(t, records.CreateClaimed(t.Context(), job, attachment))
				uncertain, err := Open(uncertainWrite{backing, after})
				require.NoError(t, err)
				uncertain.now = func() time.Time { return at.Add(PendingGrace + time.Second) }
				_, err = uncertain.RecoverPending(t.Context())
				require.Error(t, err)
				meter.now = uncertain.now
				_, err = meter.RecoverPending(t.Context())
				require.NoError(t, err)
				total, err := meter.Total(t.Context(), "account")
				require.NoError(t, err)
				require.Equal(t, int64(1), total)
				kept, err := meter.Get(t.Context(), "account", job.SlotID)
				require.NoError(t, err)
				require.True(t, kept.Attached)
				require.False(t, kept.Released)
			})
		})
	}
}

func TestOlderClaimFormatRequiresMigration(t *testing.T) {
	repotest.Run(t, func(t *testing.T, backing storage.KVStore) {
		meter, err := Open(backing)
		require.NoError(t, err)
		require.NoError(t, meter.Reserve(t.Context(), "account", "legacy", "legacy-job", "video", 1))
		claim, err := meter.Get(t.Context(), "account", "legacy")
		require.NoError(t, err)
		claim.Version = 2
		legacy, err := json.Marshal(claim)
		require.NoError(t, err)
		require.NoError(t, backing.Set(t.Context(), claimKey("account", "legacy"), legacy))
		_, err = meter.Attachment(t.Context(), "account", "legacy", "legacy-job", "video")
		require.ErrorIs(t, err, ErrInvalid)
		_, err = meter.RecoverPending(t.Context())
		require.ErrorIs(t, err, ErrInvalid)
		count, err := meter.Total(t.Context(), "account")
		require.NoError(t, err)
		require.Equal(t, int64(1), count)
		held, err := backing.Get(t.Context(), claimKey("account", "legacy"))
		require.NoError(t, err)
		require.Equal(t, legacy, held)
	})
}
