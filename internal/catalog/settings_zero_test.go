package catalog

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/agentstation/starmap"
	catalogstorage "github.com/agentstation/starmap/pkg/catalogs/storage"
	"github.com/agentstation/starmap/runtime"
	"github.com/stretchr/testify/require"
)

func TestZeroAcquisitionIntervalRunsOnlyAtStartup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		settings := identityTestSettings("", "", "")
		settings.SourcePollInterval = 0
		settings.AcquisitionEnabled = true
		settings.AcquisitionInterval = 0
		options, err := settings.starmapOptions()
		require.NoError(t, err)
		acquirer := new(countingAcquirer)
		options = append(options, runtime.WithAcquirer(acquirer), runtime.WithClientOptions(starmap.WithCatalogStore(catalogstorage.NewMemory())))
		connected, err := runtime.Open(t.Context(), options...)
		require.NoError(t, err)
		defer func() { require.NoError(t, connected.Close()) }()
		synctest.Wait()
		require.EqualValues(t, 1, acquirer.calls.Load())
		time.Sleep(5 * time.Hour)
		synctest.Wait()
		require.EqualValues(t, 1, acquirer.calls.Load(), "zero must not select the default repeat interval")
	})
}

type countingAcquirer struct{ calls atomic.Int64 }

func (a *countingAcquirer) AcquireProviders(context.Context, runtime.AcquisitionRequest) (runtime.AcquisitionResult, error) {
	a.calls.Add(1)
	return runtime.AcquisitionResult{}, nil
}
