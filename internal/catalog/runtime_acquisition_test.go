package catalog

import (
	"context"
	"fmt"
	"sort"
	"testing"

	"github.com/agentstation/starmap/acquisition"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starmap/pkg/sources"
	pkgsync "github.com/agentstation/starmap/pkg/sync"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/storage"
)

func TestStarmapAcquisitionPublishesRefresh(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-acquisition-secret")
	var capturedSecret string
	runtime, err := OpenRuntime(
		context.Background(),
		storage.NewMockStore(),
		"",
		acquisition.WithProviderClientFactory(func(
			provider *catalogs.Provider,
		) (sources.ProviderClient, error) {
			credentialField, found := acquisitionCredentialField(
				provider.Credentials,
				"OPENAI_API_KEY",
			)
			if !found {
				return nil, fmt.Errorf("OPENAI_API_KEY credential field is required")
			}
			modelIDs := make([]string, 0, len(provider.Models))
			for modelID, model := range provider.Models {
				if model != nil && model.ModelRef != "" {
					modelIDs = append(modelIDs, modelID)
				}
			}
			sort.Strings(modelIDs)
			models := make([]catalogs.Model, 0, len(modelIDs))
			for _, modelID := range modelIDs {
				models = append(models, *provider.Models[modelID])
			}
			if len(models) > 0 {
				models[0].Name += " observed"
			}
			return staticProviderCatalogClient{
				models: models,
				capture: func(material sources.ProviderCredentialMaterial) error {
					value, exists := material.Value(credentialField.ID)
					if !exists {
						return fmt.Errorf("resolved %s credential is required", credentialField.ID)
					}
					capturedSecret = value
					return nil
				},
			}, nil
		}),
	)
	require.NoError(t, err)
	before := runtime.ControlPlane().Current().GenerationID()

	result, err := runtime.Refresh(
		context.Background(),
		pkgsync.WithSources(sources.ProvidersID),
		pkgsync.WithProvider(catalogs.ProviderIDOpenAI),
	)
	require.NoError(t, err)
	require.Equal(t, "sk-acquisition-secret", capturedSecret)
	require.NotEmpty(t, result.GenerationID)
	require.NotEqual(t, before, runtime.ControlPlane().Current().GenerationID())
	require.Equal(t, result.GenerationID, runtime.ControlPlane().Current().GenerationID())
}

type staticProviderCatalogClient struct {
	models  []catalogs.Model
	capture func(sources.ProviderCredentialMaterial) error
}

func (c staticProviderCatalogClient) ListModels(
	_ context.Context,
	material sources.ProviderCredentialMaterial,
) ([]catalogs.Model, error) {
	if c.capture != nil {
		if err := c.capture(material); err != nil {
			return nil, err
		}
	}
	return append([]catalogs.Model(nil), c.models...), nil
}

var _ sources.ProviderClient = staticProviderCatalogClient{}

func acquisitionCredentialField(
	credentials *catalogs.ProviderCredentials,
	environment string,
) (catalogs.ProviderCredentialField, bool) {
	if credentials == nil {
		return catalogs.ProviderCredentialField{}, false
	}
	for _, field := range credentials.Fields {
		for _, name := range field.Environment {
			if name == environment {
				return field, true
			}
		}
	}
	return catalogs.ProviderCredentialField{}, false
}
