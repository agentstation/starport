package providerauth

import (
	"errors"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/credentials"
)

// DefaultCloudChains returns compiled authentication primitives without a
// provider membership list.
func DefaultCloudChains() map[catalogs.ProviderAuthenticationPrimitive]credentials.CloudChain {
	return map[catalogs.ProviderAuthenticationPrimitive]credentials.CloudChain{
		catalogs.ProviderAuthenticationGoogleDefault: googleDefaultChain{},
		catalogs.ProviderAuthenticationAzureDefault:  azureDefaultChain{},
	}
}

func bearerField(
	profile catalogs.ProviderCredentialProfile,
	fields map[catalogs.ProviderCredentialFieldID]catalogs.ProviderCredentialField,
) (catalogs.ProviderCredentialFieldID, error) {
	var selected catalogs.ProviderCredentialFieldID
	for _, placement := range profile.Placements {
		if placement.Kind != catalogs.ProviderCredentialPlacementHeader ||
			placement.Scheme != catalogs.ProviderCredentialSchemeBearer ||
			fields[placement.Field].Kind != catalogs.ProviderCredentialFieldSecret {
			continue
		}
		if selected != "" {
			return "", errors.New("authentication profile has multiple bearer fields")
		}
		selected = placement.Field
	}
	if selected == "" {
		return "", errors.New("authentication profile has no bearer field")
	}
	return selected, nil
}

func renewableBearerMaterial(
	fieldID catalogs.ProviderCredentialFieldID,
	value string,
	expiresAt time.Time,
) credentials.SourceMaterial {
	refreshAfter := time.Time{}
	if !expiresAt.IsZero() {
		refreshAfter = expiresAt.Add(-defaultRefreshBefore)
	}
	return credentials.NewSourceMaterial(
		map[string]string{string(fieldID): value},
		string(fieldID)+"\x00"+value+"\x00"+expiresAt.UTC().Format(time.RFC3339Nano),
		expiresAt,
		&credentials.Lease{Renewable: true, RefreshAfter: refreshAfter},
	)
}
