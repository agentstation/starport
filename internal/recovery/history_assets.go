package recovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/blob"
)

// Assets stream separately from the in-memory JSON metadata budget.
const historyAssetAggregateMaxBytes int64 = 16 << 30

type historyAsset struct {
	ID       string   `json:"id"`
	Path     string   `json:"path"`
	Size     int64    `json:"size"`
	SHA256   string   `json:"sha256"`
	Evidence []string `json:"evidence_source_ids"`
}

func (m historyManifest) validateAssets(evidence map[string]bool) error {
	if len(m.Assets) > historyMaxEntries {
		return ErrConflict
	}
	seen := make(map[string]bool, len(m.Assets))
	var total int64
	for i, asset := range m.Assets {
		if !historyReference(asset.ID, 128) || seen[asset.ID] || asset.Path != fmt.Sprintf("assets/%06d.bin", i+1) || asset.Size < 0 || asset.Size > blob.ImportPublicationMaxBytes || !historyDigest(asset.SHA256) || len(asset.Evidence) == 0 || len(asset.Evidence) > historyMaxEntries {
			return ErrConflict
		}
		if asset.Size > historyAssetAggregateMaxBytes-total {
			return errors.New("recovery history assets exceed their aggregate byte limit")
		}
		total += asset.Size
		seen[asset.ID] = true
		refs := make(map[string]bool, len(asset.Evidence))
		for _, id := range asset.Evidence {
			if !evidence[id] || refs[id] {
				return ErrConflict
			}
			refs[id] = true
		}
	}
	return nil
}

func verifyHistoryAssets(ctx context.Context, directory *productfiles.Directory, state *verifiedHistoryState) error {
	if len(state.manifest.Assets) == 0 {
		return nil
	}
	assets, err := directory.ExistingChild("assets")
	if err != nil {
		return err
	}
	state.assets = assets
	for _, asset := range state.manifest.Assets {
		if err := copyHistoryAsset(ctx, state, asset.ID, io.Discard); err != nil {
			return err
		}
	}
	return nil
}

// copyHistoryAsset rechecks private identity and bytes for every later use.
// The caller must discard partial output after any error.
func copyHistoryAsset(ctx context.Context, state *verifiedHistoryState, id string, output io.Writer) error {
	if ctx == nil || state == nil || state.assets == nil || output == nil {
		return ErrConflict
	}
	for _, asset := range state.manifest.Assets {
		if asset.ID != id {
			continue
		}
		hash := sha256.New()
		n, err := state.assets.CopyFile(ctx, filepath.Base(asset.Path), io.MultiWriter(output, hash), asset.Size)
		if err != nil {
			return err
		}
		if n != asset.Size || hex.EncodeToString(hash.Sum(nil)) != asset.SHA256 {
			return errors.New("recovery history asset differs from its declared bytes")
		}
		return nil
	}
	return ErrConflict
}
