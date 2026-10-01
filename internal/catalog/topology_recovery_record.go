package catalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"

	"github.com/agentstation/starport/internal/recovery"
)

const recoveryTopologyRecordMaxBytes = 64 << 10

type recoveryTopologyRecord struct {
	Version               int                     `json:"version"`
	BackupManifestSHA256  string                  `json:"backup_manifest_sha256"`
	BackupInventorySHA256 string                  `json:"backup_inventory_sha256"`
	Dispositions          map[string]int          `json:"dispositions"`
	CensusSHA256          string                  `json:"census_sha256"`
	Boundary              recovery.Record         `json:"boundary"`
	Request               TopologyTransferRequest `json:"request"`
	TopologySHA256        string                  `json:"topology_sha256"`
	StageCount            int                     `json:"stage_count"`
	StagesSHA256          string                  `json:"stages_sha256"`
	SelectionSHA256       string                  `json:"selection_sha256"`
}

// CompileRecoveryTopology derives all inputs from one verified immutable backup.
// It opens no current target and grants no publication or admission permission.
func CompileRecoveryTopology(ctx context.Context, source *recovery.RestoreSource, request TopologyTransferRequest) (compiled *CompiledTopology, resultErr error) {
	if ctx == nil || source == nil || !fleetChunkDigest(source.ManifestDigest()) {
		return nil, recovery.ErrConflict
	}
	view, err := source.OpenCapturedKV(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		resultErr = errors.Join(resultErr, view.Close())
		if resultErr != nil {
			compiled = nil
		}
	}()
	census, err := readTopologyBackupCensus(ctx, source)
	if err != nil {
		return nil, fmt.Errorf("catalog backup file census: %w", err)
	}
	inventory, err := census.inventory(ctx, view, source.CapturedBoundary())
	if err != nil {
		return nil, fmt.Errorf("catalog backup reconstruction census: %w", err)
	}
	compiled, err = CompileTopologyTransfer(ctx, inventory, request, view)
	if err != nil {
		return nil, err
	}
	if err := compiled.capturePreparedCatalogCensus(ctx, view); err != nil {
		return nil, err
	}
	censusSHA, err := census.digest()
	if err != nil {
		return nil, err
	}
	record := recoveryTopologyRecord{Dispositions: census.dispositions(), Version: 1, BackupManifestSHA256: source.ManifestDigest(), BackupInventorySHA256: census.inventorySHA, CensusSHA256: censusSHA, Boundary: source.CapturedBoundary(), Request: request, TopologySHA256: compiled.TopologyDigest(), StageCount: compiled.StageCount(), StagesSHA256: compiled.recoveryStagesDigest(), SelectionSHA256: topologyMutationDigest(compiled.selection)}
	data, err := json.Marshal(record, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	if len(data) > recoveryTopologyRecordMaxBytes {
		return nil, errors.New("catalog recovery topology metadata exceeds its record limit")
	}
	compiled.recoveryRecord = bytes.Clone(data)
	return compiled, nil
}

// Record returns the small original backup binding without private capsules or target mutations.
// Low-level compiler output has no verified backup provenance and cannot produce this record.
func (c *CompiledTopology) Record() ([]byte, error) {
	if c == nil || len(c.recoveryRecord) == 0 || len(c.recoveryRecord) > recoveryTopologyRecordMaxBytes {
		return nil, recovery.ErrConflict
	}
	return bytes.Clone(c.recoveryRecord), nil
}

// Digest identifies the sealed backup record. TopologyDigest identifies the separate native lane.
func (c *CompiledTopology) Digest() string {
	if c == nil || len(c.recoveryRecord) == 0 {
		return ""
	}
	return payloadDigest(c.recoveryRecord)
}

// InspectRecoveryTopology recompiles original verified backup bytes and checks the exact seal.
// It does not recapture current targets, renew time evidence, or write catalog state.
func InspectRecoveryTopology(ctx context.Context, source *recovery.RestoreSource, request TopologyTransferRequest, record []byte, sealedSHA256 string) (*CompiledTopology, error) {
	if ctx == nil || !fleetChunkDigest(sealedSHA256) || len(record) == 0 || len(record) > recoveryTopologyRecordMaxBytes || payloadDigest(record) != sealedSHA256 {
		return nil, recovery.ErrConflict
	}
	var retained recoveryTopologyRecord
	if err := json.Unmarshal(record, &retained, json.RejectUnknownMembers(true)); err != nil {
		return nil, recovery.ErrConflict
	}
	canonical, err := json.Marshal(retained, json.Deterministic(true))
	if err != nil || !bytes.Equal(record, canonical) || retained.Version != 1 || source == nil || retained.BackupManifestSHA256 != source.ManifestDigest() || retained.Request != request || retained.Boundary != source.CapturedBoundary() {
		return nil, recovery.ErrConflict
	}
	compiled, err := CompileRecoveryTopology(ctx, source, request)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(record, compiled.recoveryRecord) {
		return nil, recovery.ErrConflict
	}
	return compiled, nil
}

// DeriveRecoveryTopologyDirection reads the original catalog ownership from the verified backup.
// The destination storage owner supplies its fleet selection. This read grants no target permission.
func DeriveRecoveryTopologyDirection(ctx context.Context, source *recovery.RestoreSource, destinationFleet bool) (direction TopologyDirection, resultErr error) {
	if source == nil || ctx == nil {
		return "", recovery.ErrConflict
	}
	view, err := source.OpenCapturedKV(ctx)
	if err != nil {
		return "", err
	}
	defer func() {
		resultErr = errors.Join(resultErr, view.Close())
		if resultErr != nil {
			direction = ""
		}
	}()
	if err := InspectCapturedCatalog(ctx, view, source.CapturedBoundary()); err != nil {
		return "", err
	}
	archive, err := captureFleetHistoricalArchive(ctx, view, source.CapturedBoundary())
	if err != nil {
		return "", err
	}
	if destinationFleet {
		if archive == nil {
			return TopologyLocalToFleet, nil
		}
		return TopologyFleetRestore, nil
	}
	if archive == nil {
		return TopologyLocalRestore, nil
	}
	return TopologyFleetToLocal, nil
}

func (c *CompiledTopology) recoveryStagesDigest() string {
	digest := sha256.New()
	_, _ = digest.Write([]byte("catalog-recovery-stages/1"))
	var ordinal [8]byte
	binary.BigEndian.PutUint64(ordinal[:], uint64(len(c.stages)))
	_, _ = digest.Write(ordinal[:])
	for index, stage := range c.stages {
		binary.BigEndian.PutUint64(ordinal[:], uint64(index))
		_, _ = digest.Write(ordinal[:])
		_, _ = digest.Write([]byte(topologyStageDigest(stage)))
	}
	return hex.EncodeToString(digest.Sum(nil))
}
