package byok

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/agentstation/starmap/pkg/catalogs"
)

// CredentialValidator validates Starport-owned inference material without
// provider-specific membership code or network I/O.
type CredentialValidator interface {
	ValidateCredential(context.Context, string, map[string]string, map[string]any) error
}

// ProviderLookup returns one exact provider from the active immutable catalog.
type ProviderLookup func(catalogs.ProviderID) (catalogs.Provider, bool)

// NewCatalogCredentialValidator creates a validator from an active-catalog
// lookup.
func NewCatalogCredentialValidator(lookup ProviderLookup) (CredentialValidator, error) {
	if lookup == nil {
		return nil, errors.New("provider catalog lookup is required")
	}
	return &catalogCredentialValidator{lookup: lookup}, nil
}

type catalogCredentialValidator struct {
	lookup ProviderLookup
}

func (v *catalogCredentialValidator) ValidateCredential(
	ctx context.Context,
	providerID string,
	key map[string]string,
	config map[string]any,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	provider, found := v.lookup(catalogs.ProviderID(providerID))
	if !found || provider.Credentials == nil {
		return &ValidationError{Provider: providerID, Message: "provider has no inference credential contract"}
	}
	credentials := provider.Credentials
	fields := make(map[catalogs.ProviderCredentialFieldID]catalogs.ProviderCredentialField, len(credentials.Fields))
	for _, field := range credentials.Fields {
		fields[field.ID] = field
	}
	profiles := make(map[catalogs.ProviderCredentialProfileID]catalogs.ProviderCredentialProfile, len(credentials.Profiles))
	for _, profile := range credentials.Profiles {
		profiles[profile.ID] = profile
	}
	allowedSecrets := make(map[string]struct{})
	allowedParameters := make(map[string]struct{})
	for _, profileID := range credentials.Inference.Alternatives {
		profile, exists := profiles[profileID]
		if !exists {
			continue
		}
		for _, fieldID := range profile.Fields {
			field := fields[fieldID]
			if field.Kind == catalogs.ProviderCredentialFieldSecret {
				allowedSecrets[string(fieldID)] = struct{}{}
			} else {
				allowedParameters[string(fieldID)] = struct{}{}
			}
		}
	}
	for fieldID := range key {
		if _, exists := allowedSecrets[fieldID]; !exists {
			return &ValidationError{Provider: providerID, Field: fieldID, Message: "is not declared by an inference profile"}
		}
	}
	for fieldID := range config {
		if _, exists := allowedParameters[fieldID]; !exists {
			return &ValidationError{Provider: providerID, Field: fieldID, Message: "is not declared as an inference parameter"}
		}
	}

	var firstError error
	for _, profileID := range credentials.Inference.Alternatives {
		profile, exists := profiles[profileID]
		if !exists {
			continue
		}
		if err := validateProfileMaterial(providerID, profile, fields, key, config); err == nil {
			return nil
		} else if firstError == nil {
			firstError = err
		}
	}
	if firstError != nil {
		return firstError
	}
	return &ValidationError{Provider: providerID, Message: "provider has no usable inference credential profile"}
}

func validateProfileMaterial(
	providerID string,
	profile catalogs.ProviderCredentialProfile,
	fields map[catalogs.ProviderCredentialFieldID]catalogs.ProviderCredentialField,
	key map[string]string,
	config map[string]any,
) error {
	for _, fieldID := range profile.Fields {
		field, exists := fields[fieldID]
		if !exists {
			return &ValidationError{Provider: providerID, Field: string(fieldID), Message: "is missing from the provider field contract"}
		}
		value := ""
		switch field.Kind {
		case catalogs.ProviderCredentialFieldSecret:
			value = key[string(fieldID)]
		case catalogs.ProviderCredentialFieldParameter:
			if configured, exists := config[string(fieldID)]; exists {
				value, _ = configured.(string)
			}
			if value == "" {
				value = field.Default
			}
		}
		if field.Required && strings.TrimSpace(value) == "" {
			return &ValidationError{Provider: providerID, Field: string(fieldID), Message: "is required"}
		}
		if value == "" || field.Pattern == "" {
			continue
		}
		pattern, err := regexp.Compile(field.Pattern)
		if err != nil {
			return fmt.Errorf("provider %s field %s pattern is invalid", providerID, fieldID)
		}
		if !pattern.MatchString(value) {
			return &ValidationError{Provider: providerID, Field: string(fieldID), Message: "does not match the catalog pattern"}
		}
	}
	return nil
}
