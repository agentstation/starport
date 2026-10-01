package catalog

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"reflect"
	"strings"

	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/storage"
)

// InspectPreparedCapturedCatalog checks original or final records against one sealed backup transfer.
// The final catalog can use the verified future identity while SQL admission remains closed.
// This read neither changes the original boundary nor grants admission permission.
func (c *CompiledTopology) InspectPreparedCapturedCatalog(ctx context.Context, view *recovery.KVSnapshotView, boundary recovery.Record) error {
	if ctx == nil || view == nil {
		return recovery.ErrConflict
	}
	if _, err := c.Record(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	marker, err := view.ReadCaptured(ctx, c.markerKey, topologyBatchMaxBytes)
	if errors.Is(err, storage.ErrNotFound) && boundary == c.inventory.Boundary {
		return InspectCapturedCatalog(ctx, view, boundary)
	}
	if err != nil {
		return err
	}
	if boundary != c.request.DestinationBoundary || marker.ExpiresAtMillis != 0 || !bytes.Equal(marker.Value, c.marker) {
		return recovery.ErrConflict
	}
	for key, value := range c.expected {
		if err := checkPreparedCatalogValue(ctx, view, key, value); err != nil {
			return err
		}
	}
	for _, mutation := range c.selection {
		if err := checkPreparedCatalogValue(ctx, view, mutation.Key, mutation.NewValue); err != nil {
			return err
		}
	}
	key := "catalog:topology:v1:" + payloadDigest([]byte(c.request.Operation.ID)) + ":selection"
	selected, err := view.ReadCaptured(ctx, key, fleetDescriptorMaxBytes)
	if err != nil {
		return err
	}
	var receipt struct {
		Version         int
		Topology        string
		Selection       TopologySelection
		Materialization string
	}
	if selected.ExpiresAtMillis != 0 || json.Unmarshal(selected.Value, &receipt, json.RejectUnknownMembers(true)) != nil || receipt.Version != 1 || receipt.Topology != c.digest || !reflect.DeepEqual(receipt.Selection, c.inventory.Selection) || !fleetChunkDigest(receipt.Materialization) {
		return recovery.ErrConflict
	}
	return c.inspectPreparedCatalogCensus(ctx, view)
}

type preparedCatalogRecord struct {
	digest  string
	size    int
	expires int64
}

func preparedCatalogNamespace(key string) bool {
	return strings.HasPrefix(key, "catalog_generation:") ||
		strings.HasPrefix(key, "catalog_candidate_generation:") ||
		strings.HasPrefix(key, "catalog:fleet:") ||
		strings.HasPrefix(key, topologyArchivePrefix)
}

// The original backup compiler already validates publication semantics and future identity.
// Retain a complete byte census so final snapshots cannot add, omit, expire, or substitute records.
func (c *CompiledTopology) capturePreparedCatalogCensus(ctx context.Context, source capturedCatalogRecords) error {
	census := map[string]preparedCatalogRecord{}
	if err := source.Enumerate(ctx, func(record storage.TransferRecord) error {
		if preparedCatalogNamespace(record.Key) {
			census[record.Key] = preparedCatalogRecord{payloadDigest(record.Value), len(record.Value), record.ExpiresAtMillis}
		}
		return nil
	}); err != nil {
		return err
	}
	put := func(key string, value []byte) {
		if !preparedCatalogNamespace(key) {
			return
		}
		if value == nil {
			delete(census, key)
		} else {
			census[key] = preparedCatalogRecord{digest: payloadDigest(value), size: len(value)}
		}
	}
	for key, value := range c.expected {
		put(key, value)
	}
	for _, mutation := range c.selection {
		put(mutation.Key, mutation.NewValue)
	}
	c.preparedCensus = census
	return nil
}

func (c *CompiledTopology) inspectPreparedCatalogCensus(ctx context.Context, source capturedCatalogRecords) error {
	if len(c.preparedCensus) == 0 {
		return recovery.ErrConflict
	}
	seen := 0
	if err := source.Enumerate(ctx, func(record storage.TransferRecord) error {
		if !preparedCatalogNamespace(record.Key) {
			return nil
		}
		expected, found := c.preparedCensus[record.Key]
		if !found || len(record.Value) != expected.size || record.ExpiresAtMillis != expected.expires || payloadDigest(record.Value) != expected.digest {
			return recovery.ErrConflict
		}
		seen++
		return nil
	}); err != nil {
		return err
	}
	if seen != len(c.preparedCensus) {
		return recovery.ErrConflict
	}
	return nil
}

func checkPreparedCatalogValue(ctx context.Context, view *recovery.KVSnapshotView, key string, value []byte) error {
	record, err := view.ReadCaptured(ctx, key, topologyBatchMaxBytes)
	if errors.Is(err, storage.ErrNotFound) && value == nil {
		return nil
	}
	if err != nil {
		return err
	}
	if value == nil || record.ExpiresAtMillis != 0 || !bytes.Equal(record.Value, value) {
		return recovery.ErrConflict
	}
	return nil
}
