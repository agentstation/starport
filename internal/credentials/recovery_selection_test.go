package credentials

import (
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"
)

func TestRecoveryMaterialSelectionTracksPrivateOwnerState(t *testing.T) {
	identity, material, grant, _ := destinationFixture(t)
	first, err := material.RecoverySelectionSHA256()
	require.NoError(t, err)
	changed := NewMaterial(material.Profile(), map[catalogs.ProviderCredentialFieldID]string{"api-key": "another-private-value"}, MaterialMetadata{Handle: material.Handle()})
	next, err := changed.RecoverySelectionSHA256()
	require.NoError(t, err)
	require.NotEqual(t, first, next)
	bound := material.WithDestinationGrant(grant, identity, catalogs.ProviderOperationChatCompletions)
	boundDigest, err := bound.RecoverySelectionSHA256()
	require.NoError(t, err)
	require.NotEqual(t, first, boundDigest)
	grant.Revoke()
	revoked, err := bound.RecoverySelectionSHA256()
	require.NoError(t, err)
	require.NotEqual(t, boundDigest, revoked)
	valid := NewMaterialValidity(time.Now().Add(time.Hour))
	selected := material.WithValidity(valid)
	before, err := selected.RecoverySelectionSHA256()
	require.NoError(t, err)
	valid.Revoke()
	after, err := selected.RecoverySelectionSHA256()
	require.NoError(t, err)
	require.NotEqual(t, before, after)
}

func TestRecoveryDestinationSelectionTracksAppliedPolicy(t *testing.T) {
	var defaults *DestinationApprovals
	defaultDigest, err := defaults.RecoverySelectionSHA256()
	require.NoError(t, err)
	empty, err := NewDestinationApprovals(nil)
	require.NoError(t, err)
	emptyDigest, err := empty.RecoverySelectionSHA256()
	require.NoError(t, err)
	require.NotEqual(t, defaultDigest, emptyDigest)
	_, material, grant, _ := destinationFixture(t)
	policy, err := NewDestinationPolicy("openai", "shared", material.Profile(), []Destination{{Operation: catalogs.ProviderOperationChatCompletions, Method: "POST", URL: "https://api.openai.com/v1/chat/completions"}})
	require.NoError(t, err)
	approvals, err := NewDestinationApprovals([]*DestinationGrant{grant}, policy)
	require.NoError(t, err)
	before, err := approvals.RecoverySelectionSHA256()
	require.NoError(t, err)
	again, err := approvals.RecoverySelectionSHA256()
	require.NoError(t, err)
	require.Equal(t, before, again)
	policy.Revoke()
	after, err := approvals.RecoverySelectionSHA256()
	require.NoError(t, err)
	require.NotEqual(t, before, after)
}

func TestRecoverySourceSelectionDoesNotResolveMaterial(t *testing.T) {
	provider := staticCredentialProvider()
	resolver := func() *Resolver {
		return NewResolver(WithEnvironmentLookup(func(string) (string, bool) { t.Fatal("recovery digest read the environment"); return "", false }))
	}
	first := referenceHandle(t, provider, resolver(), "env:EXPLICIT_OPENAI_KEY", false)
	second := referenceHandle(t, provider, resolver(), "env:EXPLICIT_OPENAI_KEY", false)
	a, err := RecoveryMaterialSourceSHA256(first.CachedSource())
	require.NoError(t, err)
	b, err := RecoveryMaterialSourceSHA256(second.CachedSource())
	require.NoError(t, err)
	require.Equal(t, a, b)
	handle, err := RecoveryMaterialSourceSHA256(first)
	require.NoError(t, err)
	require.NotEqual(t, a, handle)
	other := referenceHandle(t, provider, resolver(), "env:ANOTHER_OPENAI_KEY", false)
	c, err := RecoveryMaterialSourceSHA256(other.CachedSource())
	require.NoError(t, err)
	require.NotEqual(t, a, c)
	absent, err := RecoveryMaterialSourceSHA256(nil)
	require.NoError(t, err)
	require.NotEqual(t, a, absent)
}
