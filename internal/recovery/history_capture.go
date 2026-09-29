package recovery

import (
	"context"
	"crypto/rand"
	"encoding/json/v2"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/agentstation/starmap/pkg/productfiles"
)

func (r *historyRunner) imagePath(p historyPreparedStep) string {
	return filepath.Join(r.accepted.state.directory, "images", p.ImageDirectory)
}
func (r *historyRunner) prepare(ctx context.Context, step historyStep, positions HistoryReplayPositions, previous string) (*historyPreparedStep, error) {
	images, err := r.directory.Child("images")
	if err != nil {
		return nil, err
	}
	name := fmt.Sprintf("%06d-%s", step.Ordinal, rand.Text())
	directory, err := images.CreateChild(name)
	if err != nil {
		return nil, err
	}
	p := historyPreparedStep{Version: 1, RunSHA256: historySHA256(r.runBytes), Ordinal: step.Ordinal, Kind: step.Kind, PayloadSHA256: step.SHA256, PreviousSHA256: previous, Before: positions, ImageDirectory: name}
	path := r.imagePath(p)
	switch historyNativeOwner(step.Kind) {
	case historyOwnerKV:
		value, err := SnapshotKV(ctx, importedKVSource{r.targets.KV, r.identity.KVClaim, positions.KV}, filepath.Join(path, historyOwnerKV))
		if err != nil {
			return nil, err
		}
		p.KV = &value
	case historyOwnerSQL:
		value, err := r.witness.db.SnapshotRelationalImport(ctx, filepath.Join(path, historyOwnerSQL), r.identity.SQLOriginal, r.identity.SQL, positions.SQL)
		if err != nil {
			return nil, err
		}
		p.SQL = &value.Snapshot
	}
	if historyNativeOwner(step.Kind) != historyOwnerSQL {
		value, err := r.targets.Blobs.SnapshotImportAt(ctx, filepath.Join(path, "blobs.tar"), r.identity.ComponentOperation, r.identity.BlobOriginal, positions.Blobs)
		if err != nil {
			return nil, err
		}
		p.Blobs = &value
	}
	root, err := directory.Open()
	if err != nil {
		return nil, err
	}
	err = errors.Join(productfiles.SyncDirectory(root), root.Close())
	if err != nil {
		return nil, err
	}
	if err := r.guard(ctx, positions, ""); err != nil {
		return nil, err
	}
	body, err := json.Marshal(p, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	if err := r.directory.CompareAndPublish(ctx, historyStepName(step.Ordinal, "prepared"), nil, body); err != nil {
		return nil, err
	}
	return &p, nil
}
