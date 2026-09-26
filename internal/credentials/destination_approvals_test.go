package credentials

import (
	"testing"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"
)

func TestDestinationApprovalsSelectActualIdentity(t *testing.T) {
	identity, material, grant, request := destinationFixture(t)
	approvals, err := NewDestinationApprovals([]*DestinationGrant{grant})
	require.NoError(t, err)
	bound, err := approvals.Bind(identity.Provider, identity.Role, material, catalogs.ProviderOperationChatCompletions)
	require.NoError(t, err)
	_, err = bound.AuthorizeDestination(request)
	require.NoError(t, err)
	for _, change := range []string{"role", "provider", "operation", "profile"} {
		t.Run(change, func(t *testing.T) {
			provider, role, operation := identity.Provider, identity.Role, catalogs.ProviderOperationChatCompletions
			selected := material
			switch change {
			case "role":
				role = "other"
			case "provider":
				provider = "other"
			case "operation":
				operation = catalogs.ProviderOperationEmbeddings
			case "profile":
				profile := material.Profile()
				profile.Placements[0].Name = "Other"
				selected = NewMaterial(profile, nil, MaterialMetadata{Handle: material.Handle()})
			}
			result, err := approvals.Bind(provider, role, selected, operation)
			require.ErrorIs(t, err, ErrDestinationUnapproved)
			require.True(t, result.Empty())
		})
	}
	grant.Revoke()
	_, err = approvals.Bind(identity.Provider, identity.Role, material, catalogs.ProviderOperationChatCompletions)
	require.ErrorIs(t, err, ErrDestinationUnapproved)
}

func TestDestinationApprovalsRejectAmbiguityAndDefaultToDenial(t *testing.T) {
	identity, material, grant, _ := destinationFixture(t)
	_, err := NewDestinationApprovals([]*DestinationGrant{grant, grant})
	require.ErrorIs(t, err, ErrDestinationUnapproved)
	_, err = NewDestinationApprovals([]*DestinationGrant{nil})
	require.ErrorIs(t, err, ErrDestinationUnapproved)
	for _, empty := range []*DestinationApprovals{nil, {}} {
		_, err = empty.Bind(identity.Provider, identity.Role, material, catalogs.ProviderOperationChatCompletions)
		require.ErrorIs(t, err, ErrDestinationUnapproved)
	}
}

func TestDestinationApprovalSelectionHasNoAllocations(t *testing.T) {
	identity, material, grant, _ := destinationFixture(t)
	approvals, err := NewDestinationApprovals([]*DestinationGrant{grant})
	require.NoError(t, err)
	count := testing.AllocsPerRun(100, func() {
		selected, err := approvals.Bind(identity.Provider, identity.Role, material, catalogs.ProviderOperationChatCompletions)
		if err != nil || !selected.HasDestinationGrant() {
			panic("approval selection failed")
		}
	})
	require.Zero(t, count)
}

func TestDestinationPolicyBindsEachSelectedHandle(t *testing.T) {
	identity, material, _, request := destinationFixture(t)
	policy, err := NewDestinationPolicy(identity.Provider, identity.Role, material.Profile(), []Destination{{Operation: catalogs.ProviderOperationChatCompletions, Method: request.Method, URL: request.URL.String()}})
	require.NoError(t, err)
	approvals, err := NewDestinationApprovals(nil, policy)
	require.NoError(t, err)
	first, err := approvals.Bind(identity.Provider, identity.Role, material, catalogs.ProviderOperationChatCompletions)
	require.NoError(t, err)
	other := NewMaterial(material.Profile(), nil, MaterialMetadata{Handle: "other-account"})
	second, err := approvals.Bind(identity.Provider, identity.Role, other, catalogs.ProviderOperationChatCompletions)
	require.NoError(t, err)
	_, err = first.AuthorizeDestination(request)
	require.NoError(t, err)
	authorization, err := second.AuthorizeDestination(request)
	require.NoError(t, err)
	other.destination = first.destination
	other.destinationBound = true
	_, err = other.AuthorizeDestination(request)
	require.ErrorIs(t, err, ErrDestinationUnapproved)
	_, err = approvals.Bind(identity.Provider, "other-role", material, catalogs.ProviderOperationChatCompletions)
	require.ErrorIs(t, err, ErrDestinationUnapproved)
	policy.Revoke()
	require.ErrorIs(t, authorization.Check(request), ErrDestinationUnapproved)
	_, err = first.AuthorizeDestination(request)
	require.ErrorIs(t, err, ErrDestinationUnapproved)
	_, err = approvals.Bind(identity.Provider, identity.Role, material, catalogs.ProviderOperationChatCompletions)
	require.ErrorIs(t, err, ErrDestinationUnapproved)
}

func TestDestinationPolicyBindingHasNoAllocations(t *testing.T) {
	identity, material, _, request := destinationFixture(t)
	policy, err := NewDestinationPolicy(identity.Provider, identity.Role, material.Profile(), []Destination{{Operation: catalogs.ProviderOperationChatCompletions, Method: request.Method, URL: request.URL.String()}})
	require.NoError(t, err)
	approvals, err := NewDestinationApprovals(nil, policy)
	require.NoError(t, err)
	allocations := testing.AllocsPerRun(100, func() {
		bound, err := approvals.Bind(identity.Provider, identity.Role, material, catalogs.ProviderOperationChatCompletions)
		if err != nil {
			panic(err)
		}
		authorization, err := bound.AuthorizeDestination(request)
		if err != nil {
			panic(err)
		}
		if err := authorization.Check(request); err != nil {
			panic(err)
		}
	})
	require.Zero(t, allocations)
}

func TestDestinationPolicyDoesNotOverrideRevokedExactGrant(t *testing.T) {
	identity, material, grant, request := destinationFixture(t)
	policy, err := NewDestinationPolicy(identity.Provider, identity.Role, material.Profile(), []Destination{{Operation: catalogs.ProviderOperationChatCompletions, Method: request.Method, URL: request.URL.String()}})
	require.NoError(t, err)
	approvals, err := NewDestinationApprovals([]*DestinationGrant{grant}, policy)
	require.NoError(t, err)
	grant.Revoke()
	_, err = approvals.Bind(identity.Provider, identity.Role, material, catalogs.ProviderOperationChatCompletions)
	require.ErrorIs(t, err, ErrDestinationUnapproved)
	_, err = NewDestinationApprovals(nil, policy, policy)
	require.ErrorIs(t, err, ErrDestinationUnapproved)
	_, err = NewDestinationApprovals(nil, nil)
	require.ErrorIs(t, err, ErrDestinationUnapproved)
	_, err = NewDestinationApprovals([]*DestinationGrant{{}})
	require.ErrorIs(t, err, ErrDestinationUnapproved)
}
