package catalog

import (
	"context"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/acquisition"
	"github.com/agentstation/starmap/pkg/catalogs"
	starmaperrors "github.com/agentstation/starmap/pkg/errors"
	"github.com/agentstation/starmap/pkg/sources"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/storage"
)

// acquisitionSecret is the catalog-acquisition credential the deployment
// lookup supplies. No inference plane holds it, so a provider observation that
// carries it proves the acquisition plane is the one acquisition read.
const acquisitionSecret = "sk-acquisition-secret"

// observedMarker is what the observation adds to one model name. A published
// generation that carries it proves the refresh served the observed layer.
const observedMarker = " observed"

// TestStarmapAcquisitionPublishesRefresh proves the catalog-acquisition plane
// stands alone and reaches the refresh.
//
// The deployment lookup is the only credential source the resolver reads, and
// the observation records what it resolved. A refresh then publishes the
// observed provider layer as one effective generation, so what acquisition
// observed is what the runtime serves as its candidate.
func TestStarmapAcquisitionPublishesRefresh(t *testing.T) {
	observer := &recordingProviderObserver{provider: catalogs.ProviderIDOpenAI}
	acquirer, err := acquisition.NewAcquirer(
		acquisition.WithProviderObserver(observer),
		acquisition.WithAcquirerCoalesceWindow(time.Millisecond),
	)
	require.NoError(t, err)

	runtime, err := openRuntime(
		t.Context(),
		storage.NewMockStore(),
		Settings{
			Source:              string(starmap.SourceEmbedded),
			SourceStartupPolicy: string(starmap.StartupPreferLocal),
			SourcePollInterval:  time.Hour,
			SourceMaxHops:       8,
			AcquisitionEnabled:  true,
			TransferIdleTimeout: time.Minute,
			TransferMaxDuration: time.Minute,
		},
		acquirer,
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, runtime.Close(context.Background()))
	})

	before := runtime.Status().GenerationID

	report, err := runtime.Refresh(t.Context())
	require.NoError(t, err)
	assert.NotEmpty(t, report.RunID, "a refresh names the run it served")

	assert.Equal(
		t, acquisitionSecret, observer.resolved(),
		"acquisition must read the deployment lookup alone",
	)

	after, err := runtime.CurrentCandidate(t.Context())
	require.NoError(t, err)
	require.NotEmpty(t, after.State.GenerationID)
	assert.NotEqual(
		t, before, after.State.GenerationID,
		"the observed provider layer must publish a new effective generation",
	)
	assert.Equal(
		t, after.State.GenerationID, runtime.Status().GenerationID,
		"the status must report the generation the candidate carries",
	)

	// The published generation carries what the observation changed, so the
	// refresh served the acquisition layer and not the embedded bootstrap.
	provider, found := after.State.Catalog.Providers().Get(catalogs.ProviderIDOpenAI)
	require.True(t, found)
	assert.True(
		t, observedMarkerPresent(provider),
		"the published generation must carry the observed provider layer",
	)
}

// recordingProviderObserver observes one provider through the deployment
// credential plane and records the value it resolved. Every other provider
// answers with no layer, so the run publishes exactly one observation.
type recordingProviderObserver struct {
	provider catalogs.ProviderID

	mu     sync.Mutex
	secret string
}

// ObserveProvider implements acquisition.ProviderObserver.
func (o *recordingProviderObserver) ObserveProvider(
	ctx context.Context,
	current *catalogs.Catalog,
	id catalogs.ProviderID,
) (acquisition.ProviderObservation, error) {
	if id != o.provider {
		return acquisition.ProviderObservation{Attempt: sources.ProviderAttempt{
			ProviderID: id,
			Outcome:    sources.ProviderOutcomeSkippedNotConfigured,
			Reason:     sources.ProviderReasonCredentialUnavailable,
		}}, nil
	}
	provider, found := current.Providers().Get(id)
	if !found {
		return acquisition.ProviderObservation{}, &starmaperrors.NotFoundError{
			Resource: "provider", ID: string(id),
		}
	}

	// The resolver holds the deployment lookup alone. Reading it here proves
	// the acquisition plane supplied the credential the observation used.
	resolver := NewAcquisitionResolver(func(name string) (string, bool) {
		if name == "OPENAI_API_KEY" {
			return acquisitionSecret, true
		}
		return "", false
	})
	material, err := resolver.ResolveCatalog(ctx, provider)
	if err != nil {
		return acquisition.ProviderObservation{}, err
	}
	value, _ := material.Value("api-key")
	o.mu.Lock()
	o.secret = value
	o.mu.Unlock()

	observed, err := observedProviderCatalog(*provider)
	if err != nil {
		return acquisition.ProviderObservation{}, err
	}
	payload, err := catalogs.EncodeCatalogPayload(observed)
	if err != nil {
		return acquisition.ProviderObservation{}, err
	}
	return acquisition.ProviderObservation{
		Layer: starmap.ProviderLayer{
			ProviderID: id,
			Payload:    payload,
			Digest:     catalogs.DescribeCatalogPayload(payload).Checksum,
			ObservedAt: time.Now().UTC(),
		},
		Attempt: sources.ProviderAttempt{
			ProviderID: id,
			Outcome:    sources.ProviderOutcomeSucceeded,
			Requested:  true,
			Records:    len(provider.Models),
		},
	}, nil
}

// resolved returns the credential value the observation read.
func (o *recordingProviderObserver) resolved() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.secret
}

// observedProviderCatalog returns a catalog that holds one provider whose
// first model carries an observation marker. The marker makes the observed
// payload differ from the current one, so the refresh must move the
// generation forward.
func observedProviderCatalog(provider catalogs.Provider) (catalogs.Reader, error) {
	modelIDs := make([]string, 0, len(provider.Models))
	for modelID := range provider.Models {
		modelIDs = append(modelIDs, modelID)
	}
	sort.Strings(modelIDs)
	if len(modelIDs) > 0 {
		observed := *provider.Models[modelIDs[0]]
		observed.Name += observedMarker
		models := make(map[string]*catalogs.Model, len(provider.Models))
		for modelID, model := range provider.Models {
			models[modelID] = model
		}
		models[modelIDs[0]] = &observed
		provider.Models = models
	}
	builder := catalogs.NewEmpty()
	if err := builder.SetProvider(provider); err != nil {
		return nil, err
	}
	return builder, nil
}

// observedMarkerPresent reports whether one model of the provider carries the
// marker the observation added.
func observedMarkerPresent(provider *catalogs.Provider) bool {
	for _, model := range provider.Models {
		if model != nil && strings.HasSuffix(model.Name, observedMarker) {
			return true
		}
	}
	return false
}

var _ acquisition.ProviderObserver = (*recordingProviderObserver)(nil)
