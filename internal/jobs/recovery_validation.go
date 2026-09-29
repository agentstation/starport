package jobs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"time"

	"github.com/agentstation/starport/internal/blob"
)

// VerifyRecoveryRecord checks job identity and retained assets without advancing work.
// Missing pending assets or response receipts remain uncertain, never permission to repeat inference.
func VerifyRecoveryRecord(ctx context.Context, key string, data []byte, source blob.PublicationReader, capturedAt time.Time) (Job, error) {
	if err := ctx.Err(); err != nil {
		return Job{}, err
	}
	job, err := decodeJob(data)
	if err != nil || source == nil || capturedAt.IsZero() || key != storageKey(job.Account, job.ID) {
		return Job{}, ErrCorruptRecord
	}
	if job.AssetKey != "" {
		digest := job.assetDigest
		if job.Native {
			digest = job.nativeAssetDigest
		}
		err := blob.VerifyPublished(ctx, source, job.AssetKey, job.AssetBytes, digest)
		optional := job.assetPending || job.AssetExpired(capturedAt)
		if err != nil && (!optional || !errors.Is(err, blob.ErrNotFound)) {
			return Job{}, errors.Join(ErrCorruptRecord, err)
		}
	}
	if job.Native {
		if err := verifyRecoveryNativeAsset(ctx, source, job); err != nil {
			return Job{}, err
		}
		if err := verifyRecoveryNativeReceipt(ctx, source, job); err != nil && !errors.Is(err, blob.ErrNotFound) {
			return Job{}, errors.Join(ErrCorruptRecord, err)
		}
	}
	return job, nil
}

func verifyRecoveryNativeAsset(ctx context.Context, source blob.PublicationReader, job Job) error {
	if job.nativeAssetKey == "" || job.nativeAssetKey == job.AssetKey {
		return nil
	}
	info, err := source.StatPublished(ctx, job.nativeAssetKey)
	if errors.Is(err, blob.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Size <= 0 || info.Size > job.nativeAssetBound || job.nativeAssetDigest == "" {
		return ErrCorruptRecord
	}
	return blob.VerifyPublished(ctx, source, job.nativeAssetKey, info.Size, job.nativeAssetDigest)
}

func verifyRecoveryNativeReceipt(ctx context.Context, source blob.PublicationReader, job Job) (resultErr error) {
	if job.nativeReceiptKey == "" {
		return nil
	}
	reader, err := source.ReadPublished(ctx, job.nativeReceiptKey)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, reader.Close()) }()
	input := &recoveryReader{ctx: ctx, reader: reader}
	receipt, err := readNativeReceiptHeader(input, job)
	if err != nil {
		return err
	}
	hash := sha256.New()
	if _, err := io.CopyN(hash, input, receipt.AssetBytes); err != nil {
		return errors.Join(ErrCorruptRecord, err)
	}
	var extra [1]byte
	if n, err := io.ReadFull(input, extra[:]); n != 0 || !errors.Is(err, io.EOF) {
		return ErrCorruptRecord
	}
	if hex.EncodeToString(hash.Sum(nil)) != receipt.AssetDigest {
		return ErrCorruptRecord
	}
	return nil
}

type recoveryReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *recoveryReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
