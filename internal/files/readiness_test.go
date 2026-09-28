package files

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/repotest"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type unavailablePublication struct{ blob.Store }

var publicationUnavailable = errors.New("conditional publication unavailable")

func (unavailablePublication) EnsurePublicationReady(context.Context) error {
	return publicationUnavailable
}

func TestFilesRefuseUnqualifiedPublication(t *testing.T) {
	repotest.Run(t, func(t *testing.T, kv storage.KVStore) {
		records, err := OpenRepository(kv)
		require.NoError(t, err)
		data, err := blob.NewFilesystem(t.TempDir())
		require.NoError(t, err)
		service, err := NewService(records, unavailablePublication{data})
		require.NoError(t, err)
		_, err = service.PrepareOutput(t.Context(), "a", "line", "result", 1)
		require.ErrorIs(t, err, publicationUnavailable)
		_, err = service.Upload(t.Context(), UploadRequest{Account: "a", Filename: "input", Purpose: PurposeBatch, Size: 1}, strings.NewReader("x"))
		require.ErrorIs(t, err, publicationUnavailable)
	})
}
