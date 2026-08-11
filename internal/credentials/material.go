package credentials

import (
	"fmt"
	"net/url"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
)

// Material carries one selected inference profile and its resolved field
// values. The values are private so serializers and generic formatters cannot
// expose them.
type Material struct {
	profile  catalogs.ProviderCredentialProfile
	values   map[catalogs.ProviderCredentialFieldID]string
	metadata MaterialMetadata
}

// MaterialMetadata describes one resolved credential lifecycle. Version is
// opaque and contains no source path or credential value.
type MaterialMetadata struct {
	Version   string
	ExpiresAt time.Time
	Lease     *Lease
}

// Lease describes renewable credential material.
type Lease struct {
	Renewable    bool
	RefreshAfter time.Time
}

// NewMaterial creates caller-owned inference credential material.
func NewMaterial(
	profile catalogs.ProviderCredentialProfile,
	values map[catalogs.ProviderCredentialFieldID]string,
	metadata MaterialMetadata,
) Material {
	return Material{
		profile:  copyCredentialProfile(profile),
		values:   copyCredentialValues(values),
		metadata: copyMaterialMetadata(metadata),
	}
}

// Empty reports whether the material contains no selected profile.
func (m Material) Empty() bool { return m.profile.ID == "" }

// Profile returns a caller-owned copy of the selected profile.
func (m Material) Profile() catalogs.ProviderCredentialProfile {
	return copyCredentialProfile(m.profile)
}

// Value returns one exact credential or parameter value.
func (m Material) Value(fieldID catalogs.ProviderCredentialFieldID) (string, bool) {
	value, exists := m.values[fieldID]
	return value, exists
}

// Version returns the resolver-owned opaque material version.
func (m Material) Version() string { return m.metadata.Version }

// ExpiresAt returns the material expiry when a source supplied one.
func (m Material) ExpiresAt() (time.Time, bool) {
	if m.metadata.ExpiresAt.IsZero() {
		return time.Time{}, false
	}
	return m.metadata.ExpiresAt, true
}

// Lease returns caller-owned renewable-material metadata when present.
func (m Material) Lease() (Lease, bool) {
	if m.metadata.Lease == nil {
		return Lease{}, false
	}
	return *m.metadata.Lease, true
}

// EndpointBindings returns validated URL-template bindings for the profile.
func (m Material) EndpointBindings() map[string]string {
	bindings := make(map[string]string, len(m.profile.EndpointBindings))
	for _, binding := range m.profile.EndpointBindings {
		value, exists := m.values[binding.Field]
		if !exists || value == "" {
			continue
		}
		switch binding.Format {
		case catalogs.ProviderCredentialEndpointBindingURL:
			parsed, err := url.Parse(value)
			if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") ||
				parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
				continue
			}
			bindings[binding.Variable] = value
		case catalogs.ProviderCredentialEndpointBindingPathSegment:
			bindings[binding.Variable] = url.PathEscape(value)
		}
	}
	return bindings
}

// String returns a secret-free material summary.
func (m Material) String() string {
	return fmt.Sprintf("inference credential material (profile=%s, version=%t)", m.profile.ID, m.metadata.Version != "")
}

// GoString returns a secret-free Go-syntax material summary.
func (m Material) GoString() string { return m.String() }

func copyCredentialValues(
	values map[catalogs.ProviderCredentialFieldID]string,
) map[catalogs.ProviderCredentialFieldID]string {
	copied := make(map[catalogs.ProviderCredentialFieldID]string, len(values))
	for fieldID, value := range values {
		copied[fieldID] = value
	}
	return copied
}

func copyMaterialMetadata(metadata MaterialMetadata) MaterialMetadata {
	copied := metadata
	if metadata.Lease != nil {
		lease := *metadata.Lease
		copied.Lease = &lease
	}
	return copied
}

func copyCredentialProfile(
	profile catalogs.ProviderCredentialProfile,
) catalogs.ProviderCredentialProfile {
	copied := profile
	copied.Fields = append([]catalogs.ProviderCredentialFieldID(nil), profile.Fields...)
	copied.Placements = append([]catalogs.ProviderCredentialPlacement(nil), profile.Placements...)
	copied.Scopes = append([]string(nil), profile.Scopes...)
	copied.EndpointBindings = append(
		[]catalogs.ProviderCredentialEndpointBinding(nil),
		profile.EndpointBindings...,
	)
	if profile.ProtocolOptions.GoogleDefault != nil {
		options := *profile.ProtocolOptions.GoogleDefault
		copied.ProtocolOptions.GoogleDefault = &options
	}
	if profile.ProtocolOptions.AWSDefault != nil {
		options := *profile.ProtocolOptions.AWSDefault
		copied.ProtocolOptions.AWSDefault = &options
	}
	return copied
}
