package credentials

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/agentstation/starmap/pkg/catalogs"
)

const destinationHTTPS = "https"

// ErrDestinationUnapproved reports a request outside its credential grant.
var ErrDestinationUnapproved = errors.New("inference credential destination is not approved")

// DestinationIdentity binds a grant to one provider, credential role, and handle.
// Keyring supplies the role. Handle must identify the credential without its value.
type DestinationIdentity struct {
	Provider catalogs.ProviderID
	Role     string
	Handle   string
}

// Destination names one approved operation and exact HTTP request target.
// URL contains no credential value. Streaming targets require their own entries.
type Destination struct {
	Operation catalogs.ProviderOperation
	Method    string
	URL       string
}

// DestinationGrant retains an approved profile and exact request targets.
// Catalog refresh cannot extend this immutable grant.
type DestinationGrant struct {
	identity DestinationIdentity
	profile  catalogs.ProviderCredentialProfile
	targets  []destinationTarget
	revoked  atomic.Bool
}

type destinationTarget struct {
	operation catalogs.ProviderOperation
	method    string
	scheme    string
	host      string
	port      string
	path      string
	query     string
}

// NewDestinationGrant validates an explicit approval without network access.
// The caller must receive approval before it constructs or replaces a grant.
func NewDestinationGrant(identity DestinationIdentity, profile catalogs.ProviderCredentialProfile, destinations []Destination) (*DestinationGrant, error) {
	if identity.Provider == "" || identity.Role == "" || identity.Handle == "" || profile.ID == "" || len(destinations) == 0 {
		return nil, ErrDestinationUnapproved
	}
	grant := &DestinationGrant{identity: identity, profile: copyCredentialProfile(profile)}
	for _, destination := range destinations {
		parsed, err := url.Parse(destination.URL)
		if err != nil || !validDestinationURL(parsed) || destination.Operation == "" || !validDestinationMethod(destination.Method) {
			return nil, ErrDestinationUnapproved
		}
		grant.targets = append(grant.targets, destinationTarget{
			operation: destination.Operation, method: destination.Method,
			scheme: parsed.Scheme, host: parsed.Hostname(), port: destinationPort(parsed),
			path: parsed.EscapedPath(), query: parsed.RawQuery,
		})
	}
	return grant, nil
}

// Revoke prevents new authorizations and invalidates existing request handles.
func (g *DestinationGrant) Revoke() {
	if g != nil {
		g.revoked.Store(true)
	}
}

// Authorize binds material to an approved operation and request target.
// It compares the complete profile before credential placement can occur.
func (g *DestinationGrant) Authorize(identity DestinationIdentity, material Material, operation catalogs.ProviderOperation, request *http.Request) (DestinationAuthorization, error) {
	if g == nil || g.revoked.Load() || identity != g.identity || material.Handle() != identity.Handle || request == nil || !validDestinationURL(request.URL) ||
		!sameDestinationProfile(g.profile, material.profile) {
		return DestinationAuthorization{}, ErrDestinationUnapproved
	}
	for index := range g.targets {
		target := &g.targets[index]
		if target.operation == operation && target.matches(request) {
			return DestinationAuthorization{grant: g, target: *target}, nil
		}
	}
	return DestinationAuthorization{}, ErrDestinationUnapproved
}

// DestinationAuthorization checks a request target before credential placement.
// Revocation invalidates all authorizations from the grant.
type DestinationAuthorization struct {
	grant  *DestinationGrant
	target destinationTarget
}

// Check refuses revocation or a changed HTTP request target.
func (a *DestinationAuthorization) Check(request *http.Request) error {
	if a == nil || a.grant == nil || a.grant.revoked.Load() ||
		request == nil || !validDestinationURL(request.URL) || !a.target.matches(request) {
		return ErrDestinationUnapproved
	}
	return nil
}

// AfterPlacement binds the final query after approved credential placement.
// Other target fields must remain unchanged.
func (a DestinationAuthorization) AfterPlacement(request *http.Request) (DestinationAuthorization, error) {
	if a.grant == nil || a.grant.revoked.Load() || request == nil || !validDestinationURL(request.URL) {
		return DestinationAuthorization{}, ErrDestinationUnapproved
	}
	final := a
	final.target.query = request.URL.RawQuery
	if !final.target.matches(request) || !approvedQueryChange(a.target.query, final.target.query, a.grant.profile.Placements) {
		return DestinationAuthorization{}, ErrDestinationUnapproved
	}
	return final, nil
}

func approvedQueryChange(before, after string, placements []catalogs.ProviderCredentialPlacement) bool {
	if before == after {
		return true
	}
	original, err := url.ParseQuery(before)
	if err != nil {
		return false
	}
	final, err := url.ParseQuery(after)
	if err != nil {
		return false
	}
	for _, placement := range placements {
		if placement.Kind == catalogs.ProviderCredentialPlacementQuery {
			delete(original, placement.Name)
			delete(final, placement.Name)
		}
	}
	if len(original) != len(final) {
		return false
	}
	for name, values := range original {
		if !slices.Equal(values, final[name]) {
			return false
		}
	}
	return true
}

// WithDestinationGrant binds material to its selected approval and operation.
// A nil grant remains a bound refusal rather than unrestricted material.
func (m Material) WithDestinationGrant(grant *DestinationGrant, identity DestinationIdentity, operation catalogs.ProviderOperation) Material {
	m.destination = materialDestination{grant: grant, identity: identity, operation: operation}
	m.destinationBound = true
	return m
}

type materialDestination struct {
	grant     *DestinationGrant
	identity  DestinationIdentity
	operation catalogs.ProviderOperation
}

// HasDestinationGrant reports whether selection supplied a grant binding.
func (m Material) HasDestinationGrant() bool { return m.destinationBound }

// AuthorizeDestination refuses absent or mismatched destination approval.
func (m Material) AuthorizeDestination(request *http.Request) (DestinationAuthorization, error) {
	if !m.destinationBound {
		return DestinationAuthorization{}, ErrDestinationUnapproved
	}
	return m.destination.grant.Authorize(m.destination.identity, m, m.destination.operation, request)
}

func (t *destinationTarget) matches(request *http.Request) bool {
	u := request.URL
	return request.Method == t.method && u.Scheme == t.scheme && strings.EqualFold(u.Hostname(), t.host) &&
		destinationPort(u) == t.port && u.EscapedPath() == t.path && u.RawQuery == t.query &&
		(request.Host == "" || strings.EqualFold(request.Host, u.Host))
}

func validDestinationURL(u *url.URL) bool {
	return u != nil && (u.Scheme == destinationHTTPS || u.Scheme == "http") && u.Hostname() != "" &&
		u.User == nil && u.Opaque == "" && u.Fragment == "" && u.RawFragment == "" && !u.OmitHost
}

func destinationPort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	if u.Scheme == destinationHTTPS {
		return "443"
	}
	return "80"
}

func validDestinationMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

func sameDestinationProfile(a, b catalogs.ProviderCredentialProfile) bool {
	return a.ID == b.ID && a.Primitive == b.Primitive && slices.Equal(a.Fields, b.Fields) &&
		slices.Equal(a.Placements, b.Placements) && slices.Equal(a.Scopes, b.Scopes) &&
		slices.Equal(a.EndpointBindings, b.EndpointBindings) &&
		sameDestinationOptions(a.ProtocolOptions.GoogleDefault, b.ProtocolOptions.GoogleDefault) &&
		sameDestinationOptions(a.ProtocolOptions.AWSDefault, b.ProtocolOptions.AWSDefault)
}

func sameDestinationOptions[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
