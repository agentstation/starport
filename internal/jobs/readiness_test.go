package jobs_test

import (
	"context"
	"errors"
	"testing"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/stretchr/testify/require"
)

type unavailablePublication struct{ blob.Store }

var publicationUnavailable = errors.New("conditional publication unavailable")

func (unavailablePublication) EnsurePublicationReady(context.Context) error {
	return publicationUnavailable
}

func TestVideoRefusesUnqualifiedPublication(t *testing.T) {
	assets, err := blob.NewFilesystem(t.TempDir())
	require.NoError(t, err)
	service, _ := newService(t, jobs.WithAssetStore(unavailablePublication{assets}))
	_, err = service.Submit(t.Context(), open(nativeFixture()), submissionFor(accountA))
	require.ErrorIs(t, err, publicationUnavailable)
	provider := acceptedRunner()
	_, err = service.Submit(t.Context(), open(provider), submissionFor(accountA))
	require.ErrorIs(t, err, publicationUnavailable)
	require.Zero(t, provider.submits)
}
