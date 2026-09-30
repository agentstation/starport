package catalog

import (
	"bytes"
	"context"
	"encoding/json/v2"

	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/storage"
)

// TopologyExpiringTarget retires exact original expiring controls through their native import owner.
// It never renews or creates a record. The owner checks native expiry, claim, cursor, and incarnation.
// The catalog compiler and target keep this operation separate from persistent CAS.
type TopologyExpiringTarget interface {
	ApplyExpiringCatalogTopology(context.Context, string, int, []storage.TransferRecord) error
}

func topologyStageDigest(stage topologyBatch) string {
	if len(stage.expiring) == 0 {
		return topologyMutationDigest(stage.mutations)
	}
	type boundRecord struct {
		Key             string
		ValuePresent    bool
		Value           []byte
		ExpiresAtMillis int64
	}
	records := make([]boundRecord, len(stage.expiring))
	for index, record := range stage.expiring {
		records[index] = boundRecord{record.Key, record.Value != nil, record.Value, record.ExpiresAtMillis}
	}
	raw, _ := json.Marshal(struct {
		Kind    string        `json:"kind"`
		Records []boundRecord `json:"records"`
	}{"expiring-record-retirement", records}, json.Deterministic(true))
	return payloadDigest(raw)
}

func cloneTopologyExpiring(original []storage.TransferRecord) []storage.TransferRecord {
	result := make([]storage.TransferRecord, len(original))
	for index, record := range original {
		record.Value = bytes.Clone(record.Value)
		result[index] = record
	}
	return result
}

func topologyExpiringBytes(records []storage.TransferRecord) int {
	result := 0
	for _, record := range records {
		result += len(record.Key) + len(record.Value) + 8
	}
	return result
}

func (c *CompiledTopology) appendExpiringRetirement(record storage.TransferRecord) error {
	if record.Validate() != nil || record.ExpiresAtMillis <= 0 {
		return recovery.ErrConflict
	}
	size := topologyExpiringBytes([]storage.TransferRecord{record})
	if size > topologyBatchMaxBytes {
		return storage.ErrValueTooLarge
	}
	if len(c.stages) == 0 || len(c.stages[len(c.stages)-1].expiring) == 0 || len(c.stages[len(c.stages)-1].expiring) >= topologyBatchMaxMutations || topologyExpiringBytes(c.stages[len(c.stages)-1].expiring) > topologyBatchMaxBytes-size {
		c.stages = append(c.stages, topologyBatch{})
	}
	record.Value = bytes.Clone(record.Value)
	last := &c.stages[len(c.stages)-1]
	last.expiring = append(last.expiring, record)
	if c.retiredExpiring == nil {
		c.retiredExpiring = map[string]bool{}
	}
	c.retiredExpiring[record.Key] = true
	c.expected[record.Key] = nil
	return nil
}
