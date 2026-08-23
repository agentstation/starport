package keyring

import (
	"context"
	"errors"
	"testing"

	"github.com/agentstation/starport/internal/credentials"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestValidateKeyUsesCatalogCredentialContracts(t *testing.T) {
	store := storage.NewMockStore()
	masterKey, err := credentials.GenerateMasterKey()
	require.NoError(t, err)
	manager, err := newTestProviderKeys(store, masterKey)
	require.NoError(t, err)

	tests := []struct {
		name     string
		provider string
		key      map[string]string
		config   map[string]any
		wantErr  bool
		field    string
	}{
		{
			name: "OpenAI exact catalog field", provider: "openai",
			key: map[string]string{"api-key": "operator-value"},
		},
		{
			name: "OpenAI optional parameter", provider: "openai",
			key:    map[string]string{"api-key": "operator-value"},
			config: map[string]any{"organization": "org-test"},
		},
		{
			name: "YAML-only OpenAI transport provider", provider: "alibaba",
			key: map[string]string{"api-key": "operator-value"},
		},
		{
			name: "Azure parameter", provider: "azure-openai",
			key:    map[string]string{"api-key": "operator-value"},
			config: map[string]any{"endpoint": "https://resource.example"},
		},
		{
			name: "missing required secret", provider: "openai",
			key: map[string]string{}, wantErr: true, field: "api-key",
		},
		{
			name: "undeclared compatibility alias", provider: "openai",
			key:     map[string]string{"api_key": "operator-value"},
			wantErr: true, field: "api_key",
		},
		{
			name: "parameter in secret material", provider: "openai",
			key:     map[string]string{"api-key": "operator-value", "organization": "org-test"},
			wantErr: true, field: "organization",
		},
		{
			name: "unknown provider", provider: "not-in-catalog",
			key: map[string]string{"api-key": "operator-value"}, wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := manager.ValidateKey(t.Context(), test.provider, test.key, test.config)
			if test.wantErr {
				require.Error(t, err)
				if test.field != "" {
					require.ErrorContains(t, err, test.field)
				}
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestValidateKeyDoesNotPerformNetworkIO(t *testing.T) {
	store := storage.NewMockStore()
	masterKey, err := credentials.GenerateMasterKey()
	require.NoError(t, err)
	manager, err := newTestProviderKeys(store, masterKey)
	require.NoError(t, err)

	require.NoError(t, manager.ValidateKey(
		t.Context(), "openai", map[string]string{"api-key": "local-value"}, nil,
	))
}

func TestValidateKeyHonorsContextCancellation(t *testing.T) {
	store := storage.NewMockStore()
	masterKey, err := credentials.GenerateMasterKey()
	require.NoError(t, err)
	manager, err := newTestProviderKeys(store, masterKey)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = manager.ValidateKey(ctx, "openai", map[string]string{"api-key": "local-value"}, nil)
	require.ErrorIs(t, err, context.Canceled)
}

func TestNewProviderKeysRequiresCredentialValidator(t *testing.T) {
	repository, err := credentials.Open(storage.NewMockStore())
	require.NoError(t, err)
	masterKey, err := credentials.GenerateMasterKey()
	require.NoError(t, err)
	_, err = NewProviderKeys(repository, masterKey, nil)
	require.True(t, errors.Is(err, ErrCredentialValidatorRequired))
}
