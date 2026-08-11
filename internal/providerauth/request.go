package providerauth

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/credentials"
)

var (
	// ErrPrimitiveUnsupported reports an authentication primitive without a
	// compiled request applicator.
	ErrPrimitiveUnsupported = errors.New("provider authentication primitive is unsupported")
	// ErrMaterialRequired reports an authenticated request without resolved
	// credential material.
	ErrMaterialRequired = errors.New("provider credential material is required")
	// ErrPlacementValueRequired reports a placement whose selected material
	// does not contain the named field.
	ErrPlacementValueRequired = errors.New("provider credential placement value is required")
)

// Applicator applies one compiled authentication primitive to an HTTP request.
// Provider membership and credential values are outside this contract.
type Applicator func(credentials.Material, *http.Request) error

// Registry stores request applicators by authentication primitive.
type Registry struct {
	applicators map[catalogs.ProviderAuthenticationPrimitive]Applicator
}

// NewRegistry validates and stores request applicators by authentication
// primitive.
func NewRegistry(applicators map[catalogs.ProviderAuthenticationPrimitive]Applicator) (*Registry, error) {
	if len(applicators) == 0 {
		return nil, errors.New("provider authentication applicators are required")
	}
	registered := make(map[catalogs.ProviderAuthenticationPrimitive]Applicator, len(applicators))
	for primitive, applicator := range applicators {
		if strings.TrimSpace(string(primitive)) == "" || applicator == nil {
			return nil, errors.New("provider authentication applicator is invalid")
		}
		if _, exists := registered[primitive]; exists {
			return nil, errors.New("provider authentication primitive is duplicated")
		}
		registered[primitive] = applicator
	}
	return &Registry{applicators: registered}, nil
}

// ProductionRegistry returns Starport's compiled authentication primitives.
func ProductionRegistry() (*Registry, error) {
	return NewRegistry(map[catalogs.ProviderAuthenticationPrimitive]Applicator{
		catalogs.ProviderAuthenticationNone:          applyPlacements,
		catalogs.ProviderAuthenticationAPIKey:        applyPlacements,
		catalogs.ProviderAuthenticationBearerToken:   applyPlacements,
		catalogs.ProviderAuthenticationGoogleDefault: applyGoogleDefault,
		catalogs.ProviderAuthenticationAzureDefault:  applyPlacements,
	})
}

// Supports reports whether Starport compiled the authentication primitive.
func (r *Registry) Supports(primitive catalogs.ProviderAuthenticationPrimitive) bool {
	if r == nil {
		return false
	}
	_, exists := r.applicators[primitive]
	return exists
}

// Apply selects an applicator from the material's exact catalog profile.
func (r *Registry) Apply(material credentials.Material, request *http.Request) error {
	if request == nil {
		return errors.New("provider request is required")
	}
	if material.Empty() {
		return ErrMaterialRequired
	}
	profile := material.Profile()
	applicator, exists := r.applicators[profile.Primitive]
	if !exists {
		return fmt.Errorf("%s: %w", profile.Primitive, ErrPrimitiveUnsupported)
	}
	return applicator(material, request)
}

func applyGoogleDefault(material credentials.Material, request *http.Request) error {
	if err := applyPlacements(material, request); err != nil {
		return err
	}
	options := material.Profile().ProtocolOptions.GoogleDefault
	if options == nil || options.QuotaProjectField == "" {
		return nil
	}
	quotaProject, exists := material.Value(options.QuotaProjectField)
	if !exists || strings.TrimSpace(quotaProject) == "" {
		return nil
	}
	request.Header.Set("x-goog-user-project", quotaProject)
	return nil
}

func applyPlacements(material credentials.Material, request *http.Request) error {
	profile := material.Profile()
	if profile.Primitive != catalogs.ProviderAuthenticationNone && len(profile.Placements) == 0 {
		return errors.New("provider credential profile has no request placements")
	}
	applied := 0
	for _, placement := range profile.Placements {
		value, exists := material.Value(placement.Field)
		if !exists || strings.TrimSpace(value) == "" {
			continue
		}
		placed, err := applyScheme(placement.Scheme, value)
		if err != nil {
			return fmt.Errorf("field %s: %w", placement.Field, err)
		}
		switch placement.Kind {
		case catalogs.ProviderCredentialPlacementHeader:
			request.Header.Set(placement.Name, placed)
		case catalogs.ProviderCredentialPlacementQuery:
			if request.URL == nil || request.URL.Scheme != "https" {
				return errors.New("provider credential query placement requires an HTTPS request")
			}
			query := request.URL.Query()
			query.Set(placement.Name, placed)
			request.URL.RawQuery = query.Encode()
		default:
			return errors.New("provider credential placement kind is unsupported")
		}
		applied++
	}
	if profile.Primitive != catalogs.ProviderAuthenticationNone && applied == 0 {
		return ErrPlacementValueRequired
	}
	return nil
}

func applyScheme(scheme catalogs.ProviderCredentialScheme, value string) (string, error) {
	switch scheme {
	case catalogs.ProviderCredentialSchemeDirect:
		return value, nil
	case catalogs.ProviderCredentialSchemeBearer:
		return "Bearer " + value, nil
	case catalogs.ProviderCredentialSchemeBasic:
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(value)), nil
	default:
		return "", errors.New("provider credential placement scheme is unsupported")
	}
}
