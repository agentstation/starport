package catalog

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"
)

type discoveryPolicy struct {
	definition catalogs.ModelDefinitionID
	provider   catalogs.ProviderID
}

func (p discoveryPolicy) AllowsDefinition(id catalogs.ModelDefinitionID) bool {
	return id == p.definition
}
func (p discoveryPolicy) AllowsOffering(key catalogs.OfferingKey) bool {
	return key.ProviderID == p.provider
}

func TestDiscoveryPreservesMembershipWithoutAdapter(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := Open(client)
	require.NoError(t, err)
	provider, offering := firstOffering(t, client.Catalog())
	definition := offering.DefinitionID
	require.NotEmpty(t, definition)
	policy := discoveryPolicy{definition: definition, provider: provider}
	before, err := plane.Current().Discover(policy)
	require.NoError(t, err)
	require.Len(t, before.Models, 1)
	require.NotEmpty(t, before.Models[0].Offerings)
	require.Empty(t, plane.Current().Definitions(), "compatibility membership remains routable")
	require.NoError(t, plane.SetAdapter(testAdapterAvailability(provider, offering, true)))
	active, err := plane.Current().Discover(policy)
	require.NoError(t, err)
	require.Equal(t, before, active, "adapter activation does not change accepted discovery facts")
	require.NoError(t, plane.RemoveAdapter(provider))
	after, err := plane.Current().Discover(policy)
	require.NoError(t, err)
	require.Equal(t, before, after)
	after.Models[0].Definition.Name = "caller mutation"
	after.Models[0].Offerings[0].ProviderModelID = "caller mutation"
	again, err := plane.Current().Discover(policy)
	require.NoError(t, err)
	require.Equal(t, before, again)
}

func TestDiscoveryRequiresExplicitDisclosurePolicy(t *testing.T) {
	client, err := starmap.New()
	require.NoError(t, err)
	plane, err := Open(client)
	require.NoError(t, err)
	_, err = plane.Current().Discover(nil)
	require.ErrorIs(t, err, ErrDisclosurePolicyRequired)
	denied, err := plane.Current().Discover(discoveryPolicy{})
	require.NoError(t, err)
	require.Empty(t, denied.Models)
}

func TestDiscoveryRefusesColdInternalAuthority(t *testing.T) {
	source := authorityAcceptanceSource(t)
	r := openAuthorityAcceptance(t, authoritySnapshotBadger(t, t.TempDir()), filepath.Join(t.TempDir(), "runtime"), source, "enterprise", "production")
	snapshot := r.ControlPlane().Current()
	require.False(t, snapshot.AllowsNewAttempt())
	_, err := snapshot.Discover(discoveryPolicy{})
	require.Error(t, err)
}

type discoveryPolicyWithVisit struct {
	discoveryPolicy
	visit func()
}

func (p discoveryPolicyWithVisit) AllowsDefinition(id catalogs.ModelDefinitionID) bool {
	p.visit()
	return p.discoveryPolicy.AllowsDefinition(id)
}

func TestDiscoveryRechecksAuthorityBeforeReturningFacts(t *testing.T) {
	source := authorityAcceptanceSource(t)
	r := openAuthorityAcceptance(t, authoritySnapshotBadger(t, t.TempDir()), filepath.Join(t.TempDir(), "runtime"), source, "enterprise", "production")
	acceptAuthorityGeneration(t, r)
	snapshot := r.ControlPlane().Current()
	policy := discoveryPolicy{definition: "enterprise-fixture/allowed"}
	accepted, err := snapshot.Discover(policy)
	require.NoError(t, err)
	require.Len(t, accepted.Models, 1, "internal discovery excludes embedded membership")
	require.Empty(t, accepted.Models[0].Offerings, "offering disclosure needs its own grant")
	interrupted, err := snapshot.Discover(discoveryPolicyWithVisit{
		discoveryPolicy: policy,
		visit:           func() { require.NoError(t, r.Close(t.Context())) },
	})
	require.Error(t, err)
	require.Equal(t, Discovery{}, interrupted, "revocation returns no partial catalog")
}

func TestDiscoveryRejectsMissingSnapshot(t *testing.T) {
	var snapshot *RoutableSnapshot
	result, err := snapshot.Discover(discoveryPolicy{})
	require.Error(t, err)
	require.Equal(t, Discovery{}, result)
}

func TestDiscoveryRejectsKnownAuthorityWithdrawal(t *testing.T) {
	source := authorityAcceptanceSource(t)
	r := openAuthorityAcceptance(t, authoritySnapshotBadger(t, t.TempDir()), filepath.Join(t.TempDir(), "runtime"), source, "enterprise", "production")
	acceptAuthorityGeneration(t, r)
	snapshot := r.ControlPlane().Current()
	policy := discoveryPolicy{definition: "enterprise-fixture/allowed"}
	before, err := snapshot.Discover(policy)
	require.NoError(t, err)
	require.Len(t, before.Models, 1)
	source.mu.Lock()
	source.receipt.Head.Sequence++
	source.receipt.Head.GenerationID = "withdrawn-next"
	source.receipt.Head.RequiredPermissionRevision = "sha256:" + strings.Repeat("c", 64)
	source.mu.Unlock()
	require.NoError(t, r.runtime.RefreshPermission(t.Context()))
	after, err := snapshot.Discover(policy)
	require.Error(t, err)
	require.Equal(t, Discovery{}, after)
	require.Equal(t, before.GenerationID, snapshot.GenerationID(), "withdrawal does not require metadata activation")
}
