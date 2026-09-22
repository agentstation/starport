package catalog

import (
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starmap/pkg/catalogs/permission"
	"github.com/stretchr/testify/require"
)

func TestAuthorityAcceptanceUsesPublicationSequence(t *testing.T) {
	now := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	generation := func(sequence uint64, at time.Time, provider catalogs.ProviderID, authority, policy string) catalogs.Generation {
		input := runtimeTestGeneration(t, "input", testEmptyCatalog(t, provider), at)
		result, err := permission.PrepareGeneration(input, permission.GenerationConfig{
			AuthorityID: authority, PolicyID: policy, Sequence: sequence,
		})
		require.NoError(t, err)
		return result
	}
	current := generation(2, now, "current", "enterprise", "production")
	ordinary := runtimeTestGeneration(t, "ordinary", testEmptyCatalog(t, "ordinary"), now.Add(time.Minute))
	for _, test := range []struct {
		name      string
		current   catalogs.Generation
		next      catalogs.Generation
		wantError bool
	}{
		{"newer sequence with older time", current, generation(3, now.Add(-time.Minute), "next", "enterprise", "production"), false},
		{"newer sequence with equal time", current, generation(3, now, "next", "enterprise", "production"), false},
		{"older sequence with newer time", current, generation(1, now.Add(time.Minute), "next", "enterprise", "production"), true},
		{"same sequence with changed content", current, generation(2, now.Add(time.Minute), "next", "enterprise", "production"), true},
		{"different authority", current, generation(3, now.Add(time.Minute), "next", "other", "production"), true},
		{"different policy", current, generation(3, now.Add(time.Minute), "next", "enterprise", "other"), true},
		{"ordinary replacement", current, ordinary, true},
		{"unapproved authority transition", ordinary, generation(3, now.Add(2*time.Minute), "next", "enterprise", "production"), true},
		{"identical repeat", current, current, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := t.TempDir()
			kv := authoritySnapshotBadger(t, path)
			candidates, err := newCandidateGenerationStore(kv)
			require.NoError(t, err)
			accepted, err := NewGenerationStore(kv)
			require.NoError(t, err)
			require.NoError(t, accepted.Commit(t.Context(), test.current, ""))
			require.NoError(t, candidates.Commit(t.Context(), test.next, ""))
			r := &Runtime{candidates: candidates, accepted: accepted}
			state := runtimeTestState(t, test.next)
			state.AuthorityHead = test.next.Manifest.AuthorityHead
			for range 2 {
				err := r.Accept(t.Context(), Candidate{State: state})
				if test.wantError {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
			}
			require.NoError(t, kv.Close())
			kv = authoritySnapshotBadger(t, path)
			accepted, err = NewGenerationStore(kv)
			require.NoError(t, err)
			retained, err := accepted.Current(t.Context())
			require.NoError(t, err)
			want := test.next
			if test.wantError {
				want = test.current
			}
			require.Equal(t, want.Manifest, retained.Manifest)
			require.Equal(t, want.Payload, retained.Payload)
		})
	}
}
