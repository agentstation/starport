package jobs_test

import (
	"testing"

	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits/jobslots"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestReleasedClaimCannotPublishJob(t *testing.T) {
	repotest.Run(t, func(t *testing.T, backing storage.KVStore) {
		meter, err := jobslots.Open(backing)
		require.NoError(t, err)
		records, err := jobs.OpenRepository(backing)
		require.NoError(t, err)
		job := storedJob(t, "delayed-job", accountA, submitted)
		job.SlotID = "delayed-claim"
		require.NoError(t, meter.Reserve(t.Context(), accountA, job.SlotID, job.ID, "video", 1))
		attachment, err := meter.Attachment(t.Context(), accountA, job.SlotID, job.ID, "video")
		require.NoError(t, err)
		require.NoError(t, meter.Release(t.Context(), accountA, job.SlotID))
		require.ErrorIs(t, records.Create(t.Context(), job), jobs.ErrClaimAttachmentRequired)
		require.ErrorIs(t, records.CreateClaimed(t.Context(), job, attachment), jobs.ErrClaimUnavailable)
		_, err = records.Get(t.Context(), accountA, job.ID)
		require.ErrorIs(t, err, jobs.ErrJobNotFound)
	})
}
