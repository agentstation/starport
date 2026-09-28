package controllers

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/jobs/fileio"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/files"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestBatchAggregateRetryKeepsOneFileIdentity(t *testing.T) {
	records, err := files.OpenRepository(storage.NewMockStore())
	require.NoError(t, err)
	bytes, err := blob.NewFilesystem(t.TempDir())
	require.NoError(t, err)
	service, err := files.NewService(records, bytes)
	require.NoError(t, err)
	output := fileio.Store{Files: service, Account: "a"}
	sum := sha256.Sum256([]byte("{}\n"))
	digest := hex.EncodeToString(sum[:])
	first, err := output.StoreAggregate(t.Context(), jobs.Batch{ID: "batch", Account: "a"}, false, 3, digest, strings.NewReader("{}\n"))
	require.NoError(t, err)
	again, err := output.StoreAggregate(t.Context(), jobs.Batch{ID: "batch", Account: "a"}, false, 3, digest, strings.NewReader("{}\n"))
	require.NoError(t, err)
	require.Equal(t, first, again, "aggregate recovery must not allocate another file identity")
}
