package recovery

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"path/filepath"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/sqlstore"
)

func checkRetainedActivationImages(ctx context.Context, runner *historyRunner) error {
	for _, step := range runner.accepted.state.history.manifest.Steps {
		var prepared historyPreparedStep
		body, err := runner.readJournalRecord(ctx, historyStepName(step.Ordinal, "prepared"), &prepared)
		if err != nil || body == nil {
			return errors.Join(ErrConflict, err)
		}
		path := runner.imagePath(prepared)
		directory, err := productfiles.ExistingDirectory(path)
		if err != nil {
			return err
		}
		if err := directory.CheckNoPendingPublications(ctx); err != nil {
			return err
		}
		payload := runner.accepted.state.history.payloads[step.Ordinal-1]
		switch historyNativeOwner(step.Kind) {
		case historyOwnerKV:
			if err := checkRetainedKVImage(ctx, runner, prepared, payload); err != nil {
				return err
			}
		case historyOwnerSQL:
			if err := checkRetainedSQLImage(ctx, runner, prepared, payload); err != nil {
				return err
			}
		case historyOwnerBlob:
			if err := checkRetainedBlobImage(ctx, runner, prepared, payload); err != nil {
				return err
			}
		default:
			return ErrConflict
		}
	}
	return ctx.Err()
}

func checkRetainedKVImage(ctx context.Context, runner *historyRunner, prepared historyPreparedStep, payload []byte) (resultErr error) {
	path := runner.imagePath(prepared)
	if err := checkRetainedSnapshotReceipt(ctx, filepath.Join(path, historyOwnerKV), prepared.KV); err != nil {
		return err
	}
	before, err := OpenKVSnapshot(ctx, KVSnapshotPath(filepath.Join(path, historyOwnerKV)), runner.request.ScratchDirectory, *prepared.KV)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, before.Close()) }()
	assets, err := blob.OpenSnapshot(ctx, filepath.Join(path, "blobs.tar"), runner.request.ScratchDirectory, *prepared.Blobs)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, assets.Close()) }()
	_, err = prepareHistoryKV(ctx, prepared.Kind, payload, before, assets, runner.run.ValidatedAt, runner.targets.Encryption, runner.run.Authority)
	return err
}

func checkRetainedSQLImage(ctx context.Context, runner *historyRunner, prepared historyPreparedStep, payload []byte) (resultErr error) {
	path := filepath.Join(runner.imagePath(prepared), historyOwnerSQL)
	if err := checkRetainedSnapshotReceipt(ctx, path, prepared.SQL); err != nil {
		return err
	}
	image, err := sqlstore.OpenRelationalSnapshot(ctx, filepath.Join(path, "starport.db"), *prepared.SQL, runner.request.ScratchDirectory)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, image.Close()) }()
	_, err = prepareHistorySQL(prepared.Kind, payload, runner.run.Authority)
	return err
}

func checkRetainedBlobImage(ctx context.Context, runner *historyRunner, prepared historyPreparedStep, payload []byte) (resultErr error) {
	var input historyBlobPayload
	if _, err := decodeHistoryPayload("blob_publication", payload, &input); err != nil {
		return err
	}
	if input.Version != 1 || !explicitHistoryMembers(payload, "expected", "next", "asset_id") || blob.ValidateKey(input.Key) != nil {
		return ErrConflict
	}
	image, err := blob.OpenSnapshot(ctx, filepath.Join(runner.imagePath(prepared), "blobs.tar"), runner.request.ScratchDirectory, *prepared.Blobs)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, image.Close()) }()
	reader, ok := image.(blob.RecoveryPublicationReader)
	if !ok {
		return ErrConflict
	}
	actual, err := reader.InspectPublication(ctx, input.Key)
	if err != nil || actual != input.Expected {
		return errors.Join(ErrConflict, err)
	}
	if input.Next.Kind == "live" {
		for _, asset := range runner.accepted.state.history.manifest.Assets {
			if asset.ID == input.AssetID && asset.Size == input.Next.Size && asset.SHA256 == input.Next.SHA256 {
				return ctx.Err()
			}
		}
		return ErrConflict
	}
	if input.AssetID != "" || input.Next.Kind != "retired" || input.Next.Size != 0 || input.Next.SHA256 != "" {
		return ErrConflict
	}
	return ctx.Err()
}

func checkRetainedSnapshotReceipt(ctx context.Context, path string, expected any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	directory, err := productfiles.ExistingDirectory(path)
	if err != nil {
		return err
	}
	if err := directory.CheckNoPendingPublications(ctx); err != nil {
		return err
	}
	body, err := directory.ReadFile("snapshot.json", historyManifestMaxBytes)
	if err != nil {
		return err
	}
	canonical, err := json.Marshal(expected)
	if err != nil || !bytes.Equal(body, canonical) {
		return ErrConflict
	}
	return nil
}
