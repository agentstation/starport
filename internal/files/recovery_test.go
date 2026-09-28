package files

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/blob"
	"github.com/stretchr/testify/require"
)

func TestRecoveryFileReferencesPreserveStateAndRequireCommittedBytes(t *testing.T) {
	for _, mode := range []string{"ready", "missing-ready", "wrong-size", "wrong-digest", "wrong-account", "expired", "pending", "deleting"} {
		t.Run(mode, func(t *testing.T) {
			at := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
			file := sampleFile("owner", "one")
			file.Bytes, file.CreatedAt, file.ExpiresAt = 5, at.Add(-time.Hour), at.Add(time.Hour)
			source, err := blob.NewFilesystem(filepath.Join(t.TempDir(), "blobs"))
			require.NoError(t, err)
			key := storageKey(file.Account, file.ID)
			switch mode {
			case "wrong-account":
				key = storageKey("another", file.ID)
			case "expired":
				file.ExpiresAt = at
			case "pending":
				file.State = FileStatePending
			case "deleting":
				file.State = FileStateDeleting
			case "wrong-digest":
				file.Purpose, file.outputIdentity = PurposeBatchOutput, "output-one"
				digest := sha256.Sum256([]byte("other"))
				file.outputDigest = hex.EncodeToString(digest[:])
			}
			if mode == "ready" || mode == "wrong-size" || mode == "wrong-digest" || mode == "wrong-account" {
				body := "bytes"
				if mode == "wrong-size" {
					body = "wrong size"
				}
				_, err = source.Publish(t.Context(), file.blobKey, strings.NewReader(body))
				require.NoError(t, err)
			}
			data, err := encodeFile(file)
			require.NoError(t, err)
			got, err := VerifyRecoveryRecord(t.Context(), key, data, source, at)
			switch mode {
			case "ready", "expired", "pending", "deleting":
				require.NoError(t, err)
				require.Equal(t, file, got)
			default:
				require.ErrorIs(t, err, ErrCorruptRecord)
			}
		})
	}
}
