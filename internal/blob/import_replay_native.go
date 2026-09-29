package blob

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func (t filesystemRestoreTarget) ReplayPublication(ctx context.Context, operation string, original Snapshot, step ImportPublicationStep, input io.Reader, scratch string) (string, error) {
	root, err := productfiles.ExistingDirectory(t.destination)
	if err != nil {
		return "", err
	}
	control, err := root.ExistingChild(".starport")
	if err != nil {
		return "", err
	}
	if err := control.RecoverPublications(ctx); err != nil {
		return "", err
	}
	return replayPublication(ctx, filesystemActivation{directory: control}, &Filesystem{root: t.destination}, operation, original, step, input, scratch)
}
func (t objectRestoreTarget) ReplayPublication(ctx context.Context, operation string, original Snapshot, step ImportPublicationStep, input io.Reader, scratch string) (string, error) {
	if t.store == nil {
		return "", ErrImportRestricted
	}
	return replayPublication(ctx, objectActivation(t), t.store, operation, original, step, input, scratch)
}

func (f *Filesystem) replayWrite(ctx context.Context, key string, expected, next PublicationState, input io.Reader) error {
	actual, err := f.InspectPublication(ctx, key)
	if err != nil || actual != expected {
		return errors.Join(ErrPublicationExists, err)
	}
	if err := os.MkdirAll(filepath.Join(f.root, stagingDir), dirPerm); err != nil {
		return err
	}
	if next.Kind == publicationRetired {
		input = strings.NewReader("")
	}
	_, err = f.writePublication(ctx, key, input, next.Kind == publicationRetired)
	return err
}
func (o *ObjectStore) replayWrite(ctx context.Context, key string, expected, next PublicationState, input io.Reader) error {
	actual, err := o.InspectPublication(ctx, key)
	if err != nil || actual != expected {
		return errors.Join(ErrPublicationExists, err)
	}
	address := o.objectKey(blobAddress(retainedDir, key))
	metadata := liveObjectMetadata
	header := liveEnvelope
	if next.Kind == publicationRetired {
		metadata = retiredObjectMetadata
		header = retiredEnvelope
		input = strings.NewReader("")
	}
	request := &s3.PutObjectInput{Bucket: aws.String(o.bucket), Key: aws.String(address), Body: io.MultiReader(strings.NewReader(header), input), Metadata: map[string]string{retainedObjectMetadataKey: metadata}}
	if expected.Kind == publicationAbsent {
		request.IfNoneMatch = aws.String("*")
	} else {
		current, err := o.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(o.bucket), Key: aws.String(address)})
		if err != nil || aws.ToString(current.ETag) == "" {
			return errors.Join(ErrPublicationExists, err)
		}
		request.IfMatch = current.ETag
	}
	// The existing uploader preserves conditional creation through multipart completion.
	if next.Kind == publicationLive {
		//nolint:staticcheck // The stable uploader retains the conditional multipart contract.
		_, err = o.uploader.Upload(ctx, request)
	} else {
		request.Body = strings.NewReader(retiredEnvelope)
		_, err = o.client.PutObject(ctx, request)
	}
	if isPublicationConflict(err) {
		return ErrPublicationExists
	}
	return err
}

func (t filesystemRestoreTarget) CheckImportPosition(ctx context.Context, operation string, original Snapshot, position ImportReplayPosition) error {
	if ctx == nil {
		return ErrImportRestricted
	}
	root, err := productfiles.ExistingDirectory(t.destination)
	if err != nil {
		return err
	}
	control, err := root.ExistingChild(".starport")
	if err != nil {
		return err
	}
	if err := control.CheckNoPendingPublications(ctx); err != nil {
		return err
	}
	records := filesystemActivation{directory: control}
	if err := checkBlobImport(ctx, records, operation, original); err != nil {
		return err
	}
	claim, err := makeBlobClaim(operation, original)
	if err != nil {
		return err
	}
	return checkReplayPosition(ctx, records, claim, position)
}
func (t objectRestoreTarget) CheckImportPosition(ctx context.Context, operation string, original Snapshot, position ImportReplayPosition) error {
	if t.store == nil || ctx == nil {
		return ErrImportRestricted
	}
	records := objectActivation(t)
	if err := checkBlobImport(ctx, records, operation, original); err != nil {
		return err
	}
	claim, err := makeBlobClaim(operation, original)
	if err != nil {
		return err
	}
	if err := t.store.readLayout(ctx); err != nil && !isAbsent(err) {
		return err
	}
	return checkReplayPosition(ctx, records, claim, position)
}

func (f *Filesystem) replayConfirm(ctx context.Context, key string) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	path := f.publicationPath(key)
	root, err := os.OpenRoot(f.root)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	relative, err := filepath.Rel(f.root, path)
	if err != nil {
		return err
	}
	file, err := root.OpenFile(relative, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	if err := errors.Join(file.Sync(), file.Close()); err != nil {
		return err
	}
	for directory := filepath.Dir(path); ; directory = filepath.Dir(directory) {
		if err := syncPublicationDirectory(directory); err != nil {
			return err
		}
		if directory == f.root {
			break
		}
	}
	return ctx.Err()
}
func (o *ObjectStore) replayConfirm(ctx context.Context, _ string) error { return ctx.Err() }
