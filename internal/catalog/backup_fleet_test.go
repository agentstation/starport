package catalog

import (
	"context"
	"encoding/json/v2"
	"maps"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap/runtime"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

// capturedCatalogFault changes only the named bytes in an immutable native snapshot.
type capturedCatalogFault struct {
	capturedCatalogRecords
	changed map[string]*storage.TransferRecord
}

func (f capturedCatalogFault) ReadCaptured(ctx context.Context, key string, limit int) (storage.TransferRecord, error) {
	if record, found := f.changed[key]; found {
		if record == nil {
			return storage.TransferRecord{}, storage.ErrNotFound
		}
		if len(record.Value) > limit {
			return storage.TransferRecord{}, storage.ErrValueTooLarge
		}
		return *record, nil
	}
	return f.capturedCatalogRecords.ReadCaptured(ctx, key, limit)
}
func (f capturedCatalogFault) Enumerate(ctx context.Context, visit func(storage.TransferRecord) error) error {
	pending := maps.Clone(f.changed)
	if err := f.capturedCatalogRecords.Enumerate(ctx, func(record storage.TransferRecord) error {
		if replacement, found := pending[record.Key]; found {
			delete(pending, record.Key)
			if replacement == nil {
				return nil
			}
			record = *replacement
		}
		return visit(record)
	}); err != nil {
		return err
	}
	for _, record := range pending {
		if record != nil {
			if err := visit(*record); err != nil {
				return err
			}
		}
	}
	return nil
}

func TestCapturedFleetCatalogNativeReferences(t *testing.T) {
	fleet, kv, witness := fleetTestStore(t)
	ctx := t.Context()
	settings := identityTestSettings(filepath.Join(t.TempDir(), "state"), "", "127.0.0.1:0")
	settings.DeploymentID = fleet.identity.DeploymentID
	connected, err := openRuntime(ctx, kv, settings, runtimeCollectors{fleet: fleet})
	require.NoError(t, err)
	_, err = connected.runtime.RefreshSource(ctx)
	require.NoError(t, err)
	candidate, err := connected.CurrentCandidate(ctx)
	require.NoError(t, err)
	require.NoError(t, connected.Accept(ctx, candidate))
	require.NoError(t, connected.Close(ctx))
	original, err := fleet.CurrentPublication(ctx)
	require.NoError(t, err)
	// Leave a real interrupted upload, including its partial native chunks.
	grant, err := fleet.AcquireLease(ctx, "backup-fixture", time.Minute)
	require.NoError(t, err)
	next := original.Publication
	next.Grant, next.Expected = grant, original.Head
	native := fleet.store
	fleet.store = &lostChunkReply{IncarnationStore: native}
	_, err = fleet.CommitPublication(ctx, next)
	require.ErrorContains(t, err, "lost native response")
	fleet.store = native
	require.NoError(t, fleet.Release(ctx, grant))
	closed, err := witness.Close(ctx, fleet.approval)
	require.NoError(t, err)
	view := capturedCatalogView(t, kv)
	before := capturedCatalogBytes(t, view)
	require.NoError(t, InspectCapturedCatalog(ctx, view, closed))
	require.Equal(t, before, capturedCatalogBytes(t, view))
	var inventory fleetInventory
	data, err := view.GetBounded(ctx, fleet.prefix+"inventory", fleetRetentionRecordBytes)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &inventory))
	require.NotNil(t, inventory.Pending)
	require.NotEmpty(t, inventory.Entries)
	selected := inventory.Entries[0]
	for _, mode := range []string{"missing-inventory", "missing-marker", "lost-selection", "missing-head", "missing-chunk", "corrupt-chunk", "wrong-receipt", "foreign-deployment", "expired-descriptor", "wrong-history", "bad-pending-chunk", "unknown-record", "lost-epoch", "stale-epoch", "missing-adoption"} {
		t.Run(mode, func(t *testing.T) {
			changed := map[string]*storage.TransferRecord{}
			replace := func(key string, value []byte) { changed[key] = &storage.TransferRecord{Key: key, Value: value} }
			switch mode {
			case "lost-selection":
				changed[fleet.prefix+"head"] = nil
				changed[fleet.prefix+"head-initialized"] = nil
				changed[fleet.prefix+"accepted"] = nil
			case "missing-inventory":
				changed[fleet.prefix+"inventory"] = nil
			case "missing-marker":
				changed[fleet.prefix+"head-initialized"] = nil
			case "missing-head":
				changed[fleet.prefix+"head"] = nil
			case "missing-chunk":
				changed[fleet.prefix+"blob:"+selected.ID+":"+selected.Record.Chunks[0]] = nil
			case "corrupt-chunk":
				replace(fleet.prefix+"blob:"+selected.ID+":"+selected.Record.Chunks[0], []byte("corrupt"))
			case "wrong-receipt":
				replace(fleet.publicationKey(selected.Head), []byte(`{}`))
			case "foreign-deployment":
				replace("catalog:fleet:{foreign}:v1:epoch", []byte("1"))
			case "expired-descriptor":
				record, err := view.ReadCaptured(ctx, fleet.publicationKey(selected.Head), fleetDescriptorMaxBytes)
				require.NoError(t, err)
				record.ExpiresAtMillis = time.Now().Add(time.Hour).UnixMilli()
				changed[record.Key] = &record
			case "wrong-history":
				var accepted fleetAcceptance
				data, err := view.GetBounded(ctx, fleet.prefix+"accepted", fleetDescriptorMaxBytes)
				require.NoError(t, err)
				require.NoError(t, json.Unmarshal(data, &accepted))
				accepted.History[0].PayloadChecksum = strings.Repeat("0", 64)
				data, err = json.Marshal(accepted)
				require.NoError(t, err)
				replace(fleet.prefix+"accepted", data)
			case "bad-pending-chunk":
				replace(fleet.prefix+"blob:"+inventory.Pending.ID+":"+inventory.Pending.Record.Chunks[0], []byte("bad partial data"))
			case "unknown-record":
				replace(fleet.prefix+"unknown", []byte("unknown"))
			case "lost-epoch":
				changed[fleet.prefix+"epoch"] = nil
			case "stale-epoch":
				replace(fleet.prefix+"epoch", []byte("0"))
			case "missing-adoption":
				copy := inventory
				copy.Entries = append([]fleetBlob(nil), inventory.Entries...)
				copy.Entries[0].Adoption = &runtime.FleetAdoption{Previous: selected.Head, Receipt: strings.Repeat("0", 64)}
				data, err := json.Marshal(copy)
				require.NoError(t, err)
				replace(fleet.prefix+"inventory", data)
			}
			err := inspectCapturedCatalog(t.Context(), capturedCatalogFault{view, changed}, closed)
			require.Error(t, err, mode)
		})
	}
	current, err := witness.Current(ctx, closed.DeploymentID)
	require.NoError(t, err)
	require.Equal(t, closed, current)
	// Owner recovery resolves the interrupted upload and creates a real adoption receipt.
	options, err := settings.starmapOptions()
	require.NoError(t, err)
	approved, err := adoptFleet(ctx, kv.(storage.IncarnationProvider), witness, FleetAdoptionRequest{SourceApproval: fleet.approval, Closed: closed, Head: original.Head, BackendID: fleet.identity.BackendID, OperationID: "backup-adoption", Evidence: "native-test-fenced"}, options)
	require.NoError(t, err)
	closed, err = witness.Close(ctx, approved)
	require.NoError(t, err)
	adopted := capturedCatalogView(t, kv)
	require.NoError(t, InspectCapturedCatalog(ctx, adopted, closed))
	for _, record := range capturedCatalogBytes(t, adopted) {
		if strings.HasPrefix(record.Key, fleet.prefix+"adoption-receipt:") {
			require.Error(t, inspectCapturedCatalog(ctx, capturedCatalogFault{adopted, map[string]*storage.TransferRecord{record.Key: nil}}, closed))
		}
	}
}
