package providerstate

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/availability"
	"github.com/agentstation/starport/internal/execution"
	"github.com/agentstation/starport/internal/failure"
	"github.com/agentstation/starport/internal/routing"
)

func TestProviderStateProjectionContract(t *testing.T) {
	catalog := embeddedCatalog(t)
	openAIOfferings, err := catalog.ProviderOfferings(catalogs.ProviderIDOpenAI)
	require.NoError(t, err)
	require.NotEmpty(t, openAIOfferings)
	modelID := openAIOfferings[0].ProviderModelID

	store := newWithClock(fixedClock{now: time.Unix(100, 0).UTC()})
	require.NoError(t, store.PublishCatalog("generation-1", catalog, catalogAdapterObservations(catalog,
		AdapterObservation{ProviderID: catalogs.ProviderIDOpenAI, State: AdapterReady},
		AdapterObservation{
			ProviderID: catalogs.ProviderIDAnthropic,
			State:      AdapterUnsupportedAuthentication,
			Reason:     ReasonAuthenticationUnsupported,
		},
	)))
	store.PublishCredentials(CredentialGeneration{
		CatalogGenerationID: "generation-1",
		Observations: []CredentialObservation{
			{
				ProviderID:      catalogs.ProviderIDOpenAI,
				State:           CredentialReady,
				Usable:          true,
				MaterialVersion: "opaque-v1",
			},
			{
				ProviderID: catalogs.ProviderIDAnthropic,
				State:      CredentialNotConfigured,
				Reason:     ReasonOperatorNotConfigured,
			},
		},
	})
	require.NoError(t, store.PublishAvailability(availability.Snapshot{
		Revision: 1,
		Records: []availability.Record{{
			Offering: availability.Offering{
				ProviderID:      string(catalogs.ProviderIDOpenAI),
				ProviderModelID: string(modelID),
			},
			State:       availability.StateOpen,
			FailureKind: failure.RateLimit,
		}},
	}))

	snapshot := store.Snapshot()
	require.Equal(t, "generation-1", snapshot.CatalogGenerationID)
	require.Greater(t, snapshot.Revision, uint64(0))
	openAI := requireProvider(t, snapshot, catalogs.ProviderIDOpenAI)
	require.Equal(t, AdapterReady, openAI.Adapter.State)
	require.Equal(t, CredentialReady, openAI.OperatorCredential.State)
	require.True(t, openAI.OperatorCredential.Usable)
	offering := requireOffering(t, openAI, modelID)
	require.Equal(t, availability.StateOpen, offering.State)
	require.Equal(t, ReasonRateLimited, offering.Reason)

	anthropic := requireProvider(t, snapshot, catalogs.ProviderIDAnthropic)
	require.Equal(t, AdapterUnsupportedAuthentication, anthropic.Adapter.State)
	require.Equal(t, ReasonAuthenticationUnsupported, anthropic.Adapter.Reason)
}

func TestProviderStateRejectsIncompleteAdapterProjection(t *testing.T) {
	catalog := embeddedCatalog(t)
	store := New()

	err := store.PublishCatalog("generation-1", catalog, []AdapterObservation{{
		ProviderID: catalogs.ProviderIDOpenAI,
		State:      AdapterReady,
	}})
	require.ErrorIs(t, err, ErrAdapterProjectionIncomplete)
	require.Empty(t, store.Snapshot().CatalogGenerationID)
}

func TestProviderStateRedactsCredentialMaterial(t *testing.T) {
	store, providerID := readyStore(t, "opaque-version-not-a-secret")
	payload, err := json.Marshal(store.Snapshot())
	require.NoError(t, err)
	require.NotContains(t, string(payload), "opaque-version-not-a-secret")
	require.NotContains(t, string(payload), "provider-secret-value")
	require.Contains(t, string(payload), string(providerID))
}

func TestFailureTransitionsRespectDocumentedScope(t *testing.T) {
	store, providerID := readyStore(t, "v1")
	route := routing.Route{
		CatalogGenerationID: "generation-1",
		ProviderID:          string(providerID),
		ProviderModelID:     firstOfferingID(t, embeddedCatalog(t), providerID),
	}

	store.PublishOutcome(execution.AttemptOutcome{
		Route: route,
		Credential: execution.CredentialEvidence{
			Owner: execution.CredentialOwnerTenant, MaterialVersion: "tenant-v1",
		},
		Failure: scopedFailure(failure.Authentication, failure.ScopeCredential),
	})
	require.Equal(t, CredentialReady, requireProvider(t, store.Snapshot(), providerID).OperatorCredential.State)

	store.PublishOutcome(execution.AttemptOutcome{
		Route: route,
		Credential: execution.CredentialEvidence{
			Owner: execution.CredentialOwnerOperator, MaterialVersion: "v1",
		},
		Failure: scopedFailure(failure.Permission, failure.ScopeNone),
	})
	require.Equal(t, CredentialReady, requireProvider(t, store.Snapshot(), providerID).OperatorCredential.State)

	credentialCases := []struct {
		version string
		kind    failure.Kind
		state   CredentialState
		reason  ReasonCode
	}{
		{"v2", failure.Authentication, CredentialInvalid, ReasonAuthenticationFailed},
		{"v3", failure.Permission, CredentialDenied, ReasonPermissionDenied},
		{"v4", failure.Quota, CredentialUnavailable, ReasonQuotaExceeded},
		{"v5", failure.Billing, CredentialUnavailable, ReasonBillingUnavailable},
	}
	for _, test := range credentialCases {
		store.PublishCredentials(CredentialGeneration{
			CatalogGenerationID: "generation-1",
			Observations: []CredentialObservation{{
				ProviderID: providerID, State: CredentialReady, Usable: true,
				MaterialVersion: test.version,
			}},
		})
		store.PublishOutcome(execution.AttemptOutcome{
			Route: route,
			Credential: execution.CredentialEvidence{
				Owner: execution.CredentialOwnerOperator, MaterialVersion: test.version,
			},
			Failure: scopedFailure(test.kind, failure.ScopeCredential),
		})
		status := requireProvider(t, store.Snapshot(), providerID).OperatorCredential
		require.Equal(t, test.state, status.State)
		require.Equal(t, test.reason, status.Reason)
	}

	tracker, err := availability.New(
		availability.Config{FailureThreshold: 1, OpenDuration: time.Minute}, nil, store,
	)
	require.NoError(t, err)
	for _, kind := range []failure.Kind{
		failure.RateLimit, failure.Quota, failure.Timeout, failure.ProviderUnavailable,
	} {
		require.NoError(t, tracker.Reset(availability.Offering{
			ProviderID: route.ProviderID, ProviderModelID: route.ProviderModelID,
		}))
		tracker.RecordFailure(route, scopedFailure(kind, failure.ScopeOffering), 0)
		require.Equal(
			t, availability.StateOpen,
			requireOffering(
				t,
				requireProvider(t, store.Snapshot(), providerID),
				catalogs.ProviderModelID(route.ProviderModelID),
			).State,
		)
	}
	require.NoError(t, tracker.Reset(availability.Offering{
		ProviderID: route.ProviderID, ProviderModelID: route.ProviderModelID,
	}))
	tracker.RecordFailure(route, scopedFailure(failure.NotFound, failure.ScopeOffering), 0)
	require.Equal(
		t, availability.StateUnavailable,
		requireOffering(
			t,
			requireProvider(t, store.Snapshot(), providerID),
			catalogs.ProviderModelID(route.ProviderModelID),
		).State,
	)

	require.NoError(t, tracker.Reset(availability.Offering{
		ProviderID: route.ProviderID, ProviderModelID: route.ProviderModelID,
	}))
	for _, kind := range []failure.Kind{failure.Canceled, failure.Internal, failure.Kind("unknown")} {
		tracker.RecordFailure(route, scopedFailure(kind, failure.ScopeNone), 0)
	}
	require.Equal(
		t, availability.StateHealthy,
		requireOffering(
			t,
			requireProvider(t, store.Snapshot(), providerID),
			catalogs.ProviderModelID(route.ProviderModelID),
		).State,
	)
}

func TestMaterialVersionRecovery(t *testing.T) {
	store, providerID := readyStore(t, "v1")
	route := routing.Route{ProviderID: string(providerID), ProviderModelID: "opaque-model"}
	operatorV1 := execution.CredentialEvidence{
		Owner: execution.CredentialOwnerOperator, MaterialVersion: "v1",
	}
	store.PublishOutcome(execution.AttemptOutcome{
		Route: route, Credential: operatorV1,
		Failure: scopedFailure(failure.Authentication, failure.ScopeCredential),
	})
	require.Equal(t, CredentialInvalid, requireProvider(t, store.Snapshot(), providerID).OperatorCredential.State)
	require.False(t, store.OperatorMaterialReady(string(providerID), "v1"))
	revision := store.Snapshot().Revision
	store.PublishCredentials(CredentialGeneration{
		CatalogGenerationID: "generation-1",
		Observations: []CredentialObservation{{
			ProviderID: providerID, State: CredentialReady, Usable: true,
			MaterialVersion: "v1",
		}},
	})
	require.Equal(t, revision, store.Snapshot().Revision)

	store.PublishCredentials(CredentialGeneration{
		CatalogGenerationID: "generation-1",
		Observations: []CredentialObservation{{
			ProviderID: providerID, State: CredentialRefreshing, Usable: true,
			Reason:          ReasonOperatorRefreshing,
			MaterialVersion: "v1",
		}},
	})
	require.Equal(t, CredentialInvalid, requireProvider(t, store.Snapshot(), providerID).OperatorCredential.State)

	store.PublishCredentials(CredentialGeneration{
		CatalogGenerationID: "generation-1",
		Observations: []CredentialObservation{{
			ProviderID: providerID, State: CredentialReady, Usable: true,
			MaterialVersion: "v1",
		}},
	})
	require.Equal(t, CredentialInvalid, requireProvider(t, store.Snapshot(), providerID).OperatorCredential.State)

	store.PublishCredentials(CredentialGeneration{
		CatalogGenerationID: "generation-1",
		Observations: []CredentialObservation{{
			ProviderID: providerID, State: CredentialReady, Usable: true,
			MaterialVersion: "v2",
		}},
	})
	require.Equal(t, CredentialReady, requireProvider(t, store.Snapshot(), providerID).OperatorCredential.State)
	require.True(t, store.OperatorMaterialReady(string(providerID), "v2"))

	store.PublishOutcome(execution.AttemptOutcome{
		Route: route, Credential: operatorV1,
		Failure: scopedFailure(failure.Authentication, failure.ScopeCredential),
	})
	require.Equal(t, CredentialReady, requireProvider(t, store.Snapshot(), providerID).OperatorCredential.State)
	require.True(t, store.OperatorMaterialReady(string(providerID), "v2"))

	operatorV2 := execution.CredentialEvidence{
		Owner: execution.CredentialOwnerOperator, MaterialVersion: "v2",
	}
	store.PublishOutcome(execution.AttemptOutcome{
		Route: route, Credential: operatorV2,
		Failure: scopedFailure(failure.Authentication, failure.ScopeCredential),
	})
	require.Equal(t, CredentialInvalid, requireProvider(t, store.Snapshot(), providerID).OperatorCredential.State)
	operatorV2.Accepted = true
	store.PublishOutcome(execution.AttemptOutcome{Route: route, Credential: operatorV2})
	require.Equal(t, CredentialReady, requireProvider(t, store.Snapshot(), providerID).OperatorCredential.State)
}

func readyStore(t *testing.T, version string) (*Store, catalogs.ProviderID) {
	t.Helper()
	catalog := embeddedCatalog(t)
	providerID := catalogs.ProviderIDOpenAI
	store := newWithClock(fixedClock{now: time.Unix(100, 0).UTC()})
	require.NoError(t, store.PublishCatalog("generation-1", catalog, catalogAdapterObservations(catalog,
		AdapterObservation{
			ProviderID: providerID, State: AdapterReady,
		})))
	store.PublishCredentials(CredentialGeneration{
		CatalogGenerationID: "generation-1",
		Observations: []CredentialObservation{{
			ProviderID: providerID, State: CredentialReady, Usable: true,
			MaterialVersion: version,
		}},
	})
	return store, providerID
}

func embeddedCatalog(t *testing.T) *catalogs.Catalog {
	t.Helper()
	source, err := catalogs.NewEmbedded()
	require.NoError(t, err)
	catalog, err := source.Build()
	require.NoError(t, err)
	return catalog
}

func catalogAdapterObservations(
	catalog *catalogs.Catalog,
	overrides ...AdapterObservation,
) []AdapterObservation {
	byProvider := make(map[catalogs.ProviderID]AdapterObservation, len(overrides))
	for _, observation := range overrides {
		byProvider[observation.ProviderID] = observation
	}
	observations := make([]AdapterObservation, 0, len(catalog.Providers().List()))
	for _, provider := range catalog.Providers().List() {
		observation, exists := byProvider[provider.ID]
		if !exists {
			observation = AdapterObservation{
				ProviderID: provider.ID, State: AdapterNoOfferings, Reason: ReasonNoOfferings,
			}
		}
		observations = append(observations, observation)
	}
	return observations
}

func firstOfferingID(t *testing.T, catalog *catalogs.Catalog, providerID catalogs.ProviderID) string {
	t.Helper()
	offerings, err := catalog.ProviderOfferings(providerID)
	require.NoError(t, err)
	require.NotEmpty(t, offerings)
	return string(offerings[0].ProviderModelID)
}

func requireProvider(t *testing.T, snapshot Snapshot, providerID catalogs.ProviderID) ProviderStatus {
	t.Helper()
	for _, provider := range snapshot.Providers {
		if provider.ProviderID == providerID {
			return provider
		}
	}
	t.Fatalf("provider %s was not projected", providerID)
	return ProviderStatus{}
}

func requireOffering(
	t *testing.T,
	provider ProviderStatus,
	modelID catalogs.ProviderModelID,
) OfferingStatus {
	t.Helper()
	for _, offering := range provider.Offerings {
		if offering.ProviderModelID == modelID {
			return offering
		}
	}
	t.Fatalf("offering %s was not projected", modelID)
	return OfferingStatus{}
}

func scopedFailure(kind failure.Kind, scope failure.StateScope) *failure.Failure {
	return failure.New(
		kind,
		"safe failure",
		false,
		failure.ProviderDetails{StateScope: scope},
		nil,
	)
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }
