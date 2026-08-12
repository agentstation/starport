package cloudchain

import (
	"errors"
	"sort"
	"strings"
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

func bearerSuppliedFields(
	profile catalogs.ProviderCredentialProfile,
	fields map[catalogs.ProviderCredentialFieldID]catalogs.ProviderCredentialField,
) ([]catalogs.ProviderCredentialFieldID, error) {
	fieldID, err := bearerField(profile, fields)
	if err != nil {
		return nil, err
	}
	return []catalogs.ProviderCredentialFieldID{fieldID}, nil
}

func renewableBearerMaterial(
	fieldID catalogs.ProviderCredentialFieldID,
	value string,
	expiresAt time.Time,
) credentials.SourceMaterial {
	return renewableCloudMaterial(map[string]string{string(fieldID): value}, expiresAt)
}

func renewableCloudMaterial(
	values map[string]string,
	expiresAt time.Time,
) credentials.SourceMaterial {
	refreshAfter := time.Time{}
	if !expiresAt.IsZero() {
		refreshAfter = expiresAt.Add(-defaultRefreshBefore)
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var version strings.Builder
	for _, key := range keys {
		version.WriteString(key)
		version.WriteByte(0)
		version.WriteString(values[key])
		version.WriteByte(0)
	}
	version.WriteString(expiresAt.UTC().Format(time.RFC3339Nano))
	return credentials.NewSourceMaterial(
		values,
		version.String(),
		expiresAt,
		&credentials.Lease{Renewable: true, RefreshAfter: refreshAfter},
	)
}
