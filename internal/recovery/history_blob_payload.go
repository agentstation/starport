package recovery

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/agentstation/starport/internal/blob"
)

type historyBlobPayload struct {
	Version  int                   `json:"version"`
	Key      string                `json:"key"`
	Expected blob.PublicationState `json:"expected"`
	Next     blob.PublicationState `json:"next"`
	AssetID  string                `json:"asset_id"`
}

func (r *historyRunner) applyBlob(ctx context.Context, p historyPreparedStep, payload []byte, evidence string) (receipt string, resultErr error) {
	var input historyBlobPayload
	if _, err := decodeHistoryPayload("blob_publication", payload, &input); err != nil {
		return "", err
	}
	if input.Version != 1 || !explicitHistoryMembers(payload, "expected", "next", "asset_id") || blob.ValidateKey(input.Key) != nil {
		return "", ErrConflict
	}
	before, err := blob.OpenSnapshot(ctx, filepath.Join(r.imagePath(p), "blobs.tar"), r.request.ScratchDirectory, *p.Blobs)
	if err != nil {
		return "", err
	}
	defer func() { resultErr = errors.Join(resultErr, before.Close()) }()
	inspector, ok := before.(blob.RecoveryPublicationReader)
	if !ok {
		return "", ErrConflict
	}
	actual, err := inspector.InspectPublication(ctx, input.Key)
	if err != nil {
		return "", err
	}
	if actual != input.Expected {
		return "", ErrConflict
	}
	var stream io.Reader
	var staged *os.File
	if input.Next.Kind == "live" {
		found := false
		for _, asset := range r.accepted.state.history.manifest.Assets {
			if asset.ID == input.AssetID && asset.Size == input.Next.Size && asset.SHA256 == input.Next.SHA256 {
				found = true
				break
			}
		}
		if !found {
			return "", ErrConflict
		}
		staged, err = os.CreateTemp(r.request.ScratchDirectory, "history-asset-*")
		if err != nil {
			return "", err
		}
		defer func() { resultErr = errors.Join(resultErr, staged.Close(), os.Remove(staged.Name())) }()
		if err := copyHistoryAsset(ctx, r.accepted.state.history, input.AssetID, staged); err != nil {
			return "", err
		}
		if err := staged.Sync(); err != nil {
			return "", err
		}
		if _, err := staged.Seek(0, io.SeekStart); err != nil {
			return "", err
		}
		stream = staged
	} else if input.AssetID != "" {
		return "", ErrConflict
	}
	step := blob.ImportPublicationStep{Sequence: p.Before.Blobs.Sequence + 1, PreviousSHA256: p.Before.Blobs.ReceiptSHA256, EvidenceSHA256: evidence, Key: input.Key, Expected: input.Expected, Next: input.Next}
	return r.targets.Blobs.ReplayPublication(ctx, r.identity.ComponentOperation, r.identity.BlobOriginal, step, stream, r.request.ScratchDirectory)
}
