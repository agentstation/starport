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
