package catalog

import (
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap"
	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starmap/pkg/catalogs/permission"
	starmaperrors "github.com/agentstation/starmap/pkg/errors"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

type snapshotAuthorityReader interface {
	AuthorityHead() catalogs.CatalogAuthorityHead
}

func authoritySnapshotGeneration(t *testing.T) catalogs.Generation {
	t.Helper()
	input := runtimeTestGeneration(t, "authority-input", testEmptyCatalog(t, "provider"), time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC))
	generation, err := permission.PrepareGeneration(input, permission.GenerationConfig{
		AuthorityID: "enterprise", PolicyID: "production", Sequence: 1,
	})
	require.NoError(t, err)
	return generation
}

func authoritySnapshotBadger(t *testing.T, path string) *storage.BadgerStore {
	t.Helper()
	store, err := storage.OpenBadger(storage.BadgerConfig{
		Path: path, SyncWrites: true, NumVersions: 1, NumLevelZero: 5, MemTableSize: 64 << 20,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	return store
}

func TestAuthoritySnapshotRetainsHeadAcrossAvailabilityAndRestart(t *testing.T) {
	path := t.TempDir()
	kv := authoritySnapshotBadger(t, path)
	store, err := NewGenerationStore(kv)
	require.NoError(t, err)
	generation := authoritySnapshotGeneration(t)
	require.NoError(t, store.Commit(t.Context(), generation, ""))
	for _, phase := range []string{"accepted", "reopened"} {
		client, err := starmap.New(starmap.WithCatalogStore(store))
		require.NoError(t, err)
		plane, err := Open(client)
		require.NoError(t, err)
		retained := plane.Current()
		reader, ok := any(retained).(snapshotAuthorityReader)
		require.True(t, ok, "routing snapshot cannot return its authority head")
		require.Equal(t, generation.Manifest.AuthorityHead, reader.AuthorityHead(), phase)
		require.NoError(t, plane.ReplaceAdapters(nil))
		require.Equal(t, generation.Manifest.AuthorityHead, any(plane.Current()).(snapshotAuthorityReader).AuthorityHead(), phase)
		require.Zero(t, testing.AllocsPerRun(100, func() {
			if reader.AuthorityHead() != generation.Manifest.AuthorityHead {
				t.Fatal("retained snapshot changed its authority head")
			}
		}))
		ordinary := runtimeTestState(t, runtimeTestGeneration(t, "ordinary", testEmptyCatalog(t, "ordinary"), generation.Manifest.GeneratedAt.Add(time.Minute)))
		require.NoError(t, plane.Activate(ordinary))
		require.Equal(t, catalogs.CatalogAuthorityHead{}, any(plane.Current()).(snapshotAuthorityReader).AuthorityHead())
		require.Equal(t, generation.Manifest.AuthorityHead, reader.AuthorityHead())
		if phase == "accepted" {
			require.NoError(t, kv.Close())
			kv = authoritySnapshotBadger(t, path)
			store, err = NewGenerationStore(kv)
			require.NoError(t, err)
		}
	}
	require.Equal(t, catalogs.CatalogAuthorityHead{}, any((*RoutableSnapshot)(nil)).(snapshotAuthorityReader).AuthorityHead())
}

func TestAuthorityCandidateRequiresExactStoredHead(t *testing.T) {
	generation := authoritySnapshotGeneration(t)
	changes := map[string]func(*catalogs.CatalogAuthorityHead){
		"missing":            func(h *catalogs.CatalogAuthorityHead) { *h = catalogs.CatalogAuthorityHead{} },
		"foreign policy":     func(h *catalogs.CatalogAuthorityHead) { h.PolicyID = "other" },
		"different sequence": func(h *catalogs.CatalogAuthorityHead) { h.Sequence++ },
		"different revision": func(h *catalogs.CatalogAuthorityHead) {
			h.RequiredPermissionRevision = "sha256:" + strings.Repeat("a", 64)
		},
	}
	for name, change := range changes {
		for _, alreadyAccepted := range []bool{false, true} {
			t.Run(name+"/"+map[bool]string{false: "new", true: "repeat"}[alreadyAccepted], func(t *testing.T) {
				kv := authoritySnapshotBadger(t, t.TempDir())
				candidates, err := newCandidateGenerationStore(kv)
				require.NoError(t, err)
				accepted, err := NewGenerationStore(kv)
				require.NoError(t, err)
				require.NoError(t, candidates.Commit(t.Context(), generation, ""))
				if alreadyAccepted {
					require.NoError(t, accepted.Commit(t.Context(), generation, ""))
				}
				r := &Runtime{candidates: candidates, accepted: accepted}
				state := runtimeTestState(t, generation)
				state.AuthorityHead = generation.Manifest.AuthorityHead
				change(&state.AuthorityHead)
				require.Error(t, r.Accept(t.Context(), Candidate{State: state}))
				current, err := accepted.Current(t.Context())
				if alreadyAccepted {
					require.NoError(t, err)
					require.Equal(t, generation.Manifest.AuthorityHead, current.Manifest.AuthorityHead)
				} else {
					require.ErrorIs(t, err, starmaperrors.ErrNotFound)
				}
				state.AuthorityHead = generation.Manifest.AuthorityHead
				require.NoError(t, r.Accept(t.Context(), Candidate{State: state}))
			})
		}
	}
}

func TestAuthorityProjectionRejectsMismatchedHead(t *testing.T) {
	generation := authoritySnapshotGeneration(t)
	for _, field := range []string{"generation", "checksum", "shape"} {
		t.Run(field, func(t *testing.T) {
			state := runtimeTestState(t, generation)
			state.AuthorityHead = generation.Manifest.AuthorityHead
			switch field {
			case "generation":
				state.AuthorityHead.GenerationID = "other"
			case "checksum":
				state.AuthorityHead.PayloadChecksum = "sha256:" + strings.Repeat("a", 64)
			case "shape":
				state.AuthorityHead.PolicyID = ""
			}
			plane := &ControlPlane{}
			require.Error(t, plane.Activate(state))
			require.Nil(t, plane.Current())
		})
	}
}
