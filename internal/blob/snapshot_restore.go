package blob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type blobImportClaim struct {
	Version     int      `json:"version"`
	OperationID string   `json:"operation_id"`
	Snapshot    Snapshot `json:"snapshot"`
}

func makeBlobClaim(operation string, snapshot Snapshot) ([]byte, error) {
	if strings.TrimSpace(operation) == "" || len(operation) > 128 {
		return nil, errors.New("blob: import requires a bounded operation identifier")
	}
	if err := snapshot.validate(); err != nil {
		return nil, err
	}
	return json.Marshal(blobImportClaim{Version: 1, OperationID: operation, Snapshot: snapshot})
}

// RestoreResult reports a published directory separately from final durability.
// Published also identifies a verified directory retained from an exact retry.
// The import barrier remains until the complete deployment recovery procedure.
type RestoreResult struct{ Published bool }

// RestoreFilesystem verifies a complete image before publishing a new directory.
// Existing paths remain intact. Imported bytes retain a startup barrier.
func RestoreFilesystem(ctx context.Context, destination, source, operation string, expected Snapshot) (result RestoreResult, resultErr error) {
	return restoreFilesystem(ctx, destination, source, operation, expected, false)
}

func restoreFilesystem(ctx context.Context, destination, source, operation string, expected Snapshot, resume bool) (result RestoreResult, resultErr error) {
	claim, err := makeBlobClaim(operation, expected)
	if err != nil {
		return result, err
	}
	if !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return result, errors.New("blob: restore requires a clean absolute destination")
	}
	image, err := prepareBlobImage(ctx, filepath.Dir(destination), source, expected)
	if err != nil {
		return result, err
	}
	defer func() { resultErr = errors.Join(resultErr, image.close()) }()
	if _, err := image.parent.Lstat(filepath.Base(destination)); !errors.Is(err, os.ErrNotExist) {
		if err != nil || !resume {
			return result, errors.Join(os.ErrExist, err)
		}
		if err := verifyFilesystemImport(ctx, destination, image, claim, expected.Objects); err != nil {
			return result, err
		}
		result.Published = true
		return result, productfiles.SyncDirectory(image.parent)
	}
	control, err := image.directory.Child(".starport")
	if err != nil {
		return result, err
	}
	if err := control.CompareAndPublish(ctx, "import", nil, claim); err != nil {
		return result, err
	}
	// Windows requires the staging handle to close before directory publication.
	if err := image.closeRoot(); err != nil {
		return result, err
	}
	if _, err := image.directory.Identity(); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := productfiles.PublishDirectory(image.parent, image.name, image.parent, filepath.Base(destination)); err != nil {
		return result, err
	}
	result.Published = true
	return result, productfiles.SyncDirectory(image.parent)
}

// RestoreObjectStore imports into an empty prefix or resumes the same operation.
// It preserves existing objects and leaves its recovery barrier after every outcome.
// The coordinator must fence all target writers before this operation.
func RestoreObjectStore(ctx context.Context, target *ObjectStore, source, scratch, operation string, expected Snapshot) (resultErr error) {
	if target == nil {
		return errors.New("blob: restore requires object storage")
	}
	claim, err := makeBlobClaim(operation, expected)
	if err != nil {
		return err
	}
	image, err := prepareBlobImage(ctx, scratch, source, expected)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, image.close()) }()
	if err := target.claimImport(ctx, claim); err != nil {
		return err
	}
	// This private extracted image has already passed full archive validation.
	filesystem := &Filesystem{root: image.path}
	if err := filesystem.walkObjects(ctx, func(address string, size int64, input io.Reader) error {
		if err := target.verifyImportClaim(ctx, claim); err != nil {
			return err
		}
		return target.importObject(ctx, address, size, input)
	}); err != nil {
		return err
	}
	if err := target.verifyImportContents(ctx, image, expected.Objects); err != nil {
		return err
	}
	return target.verifyImportClaim(ctx, claim)
}

func (o *ObjectStore) claimImport(ctx context.Context, claim []byte) error {
	if err := o.verifyImportClaim(ctx, claim); err == nil {
		return nil
	} else if !isAbsent(err) {
		return err
	}
	paginator := s3.NewListObjectsV2Paginator(o.client, &s3.ListObjectsV2Input{Bucket: aws.String(o.bucket), Prefix: aws.String(o.listPrefix()), MaxKeys: aws.Int32(2)})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, object := range page.Contents {
			if aws.ToString(object.Key) != o.objectKey(blobLayoutKey) {
				return errors.New("blob: import target contains existing objects")
			}
		}
	}
	if err := o.readLayout(ctx); err != nil && !isAbsent(err) {
		return err
	}
	_, err := o.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(o.bucket), Key: aws.String(o.objectKey(blobImportKey)), Body: strings.NewReader(string(claim)), IfNoneMatch: aws.String("*")})
	if err != nil && !isPublicationConflict(err) {
		return err
	}
	return o.verifyImportClaim(ctx, claim)
}

func (o *ObjectStore) verifyImportClaim(ctx context.Context, claim []byte) error {
	_, err := o.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(o.bucket), Key: aws.String(o.objectKey(blobActivationCurrent))})
	if err == nil {
		return ErrActivationConflict
	}
	if !isAbsent(err) {
		return err
	}
	result, err := o.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(o.bucket), Key: aws.String(o.objectKey(blobImportKey))})
	if err != nil {
		return err
	}
	data, readErr := io.ReadAll(io.LimitReader(result.Body, int64(len(claim)+1)))
	if err := errors.Join(readErr, result.Body.Close()); err != nil {
		return err
	}
	if string(data) != string(claim) {
		return errors.New("blob: import operation conflicts with retained state")
	}
	return nil
}

func (o *ObjectStore) importObject(ctx context.Context, address string, size int64, input io.Reader) error {
	metadata := map[string]string{}
	reader, retired, err := inspectBlobEnvelope(address, size, input)
	if err != nil {
		return err
	}
	if strings.HasPrefix(address, retainedDir+"/") {
		metadata[retainedObjectMetadataKey] = liveObjectMetadata
		if retired {
			metadata[retainedObjectMetadataKey] = retiredObjectMetadata
		}
	}
	// Read the candidate once to bind conflict recovery to exact bytes.
	hash := sha256.New()
	count, err := io.Copy(hash, &contextReader{ctx: ctx, r: reader})
	if err != nil {
		return err
	}
	if count != size {
		return errors.New("blob: import image size changed")
	}
	file, ok := input.(io.Seeker)
	if !ok {
		return errors.New("blob: import image must support verified replay")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	//nolint:staticcheck // The stable uploader retains the conditional multipart contract.
	_, err = o.uploader.Upload(ctx, &s3.PutObjectInput{Bucket: aws.String(o.bucket), Key: aws.String(o.objectKey(address)), Body: &contextReader{ctx: ctx, r: input}, IfNoneMatch: aws.String("*"), Metadata: metadata})
	if err != nil && !isPublicationConflict(err) {
		return err
	}
	return o.verifyImportedObject(ctx, address, size, hex.EncodeToString(hash.Sum(nil)), metadata)
}

func (o *ObjectStore) verifyImportedObject(ctx context.Context, address string, size int64, digest string, metadata map[string]string) error {
	result, err := o.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(o.bucket), Key: aws.String(o.objectKey(address))})
	if err != nil {
		return err
	}
	if result.ContentLength == nil || *result.ContentLength != size || result.Metadata[retainedObjectMetadataKey] != metadata[retainedObjectMetadataKey] {
		return errors.Join(errors.New("blob: restored object metadata conflicts"), result.Body.Close())
	}
	hash := sha256.New()
	n, readErr := io.Copy(hash, &contextReader{ctx: ctx, r: io.LimitReader(result.Body, size+1)})
	if err := errors.Join(readErr, result.Body.Close()); err != nil {
		return err
	}
	if n != size || hex.EncodeToString(hash.Sum(nil)) != digest {
		return errors.New("blob: restored object content conflicts")
	}
	return nil
}

func (o *ObjectStore) verifyImportContents(ctx context.Context, image *blobImage, expected int64) error {
	paginator := s3.NewListObjectsV2Paginator(o.client, &s3.ListObjectsV2Input{Bucket: aws.String(o.bucket), Prefix: aws.String(o.listPrefix())})
	var count int64
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, object := range page.Contents {
			address, ok := strings.CutPrefix(aws.ToString(object.Key), o.listPrefix())
			if !ok {
				return errors.New("blob: import listing escaped the selected prefix")
			}
			if address == blobImportKey || address == blobLayoutKey || address == blobClosureKey {
				continue
			}
			if !validBlobAddress(address) {
				return errors.New("blob: import contains an unexpected address")
			}
			info, err := image.root.Lstat(address)
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() || object.Size == nil || *object.Size != info.Size() {
				return errors.New("blob: import contains an unexpected object")
			}
			count++
		}
	}
	if count != expected {
		return errors.New("blob: import object count does not match the snapshot")
	}
	return nil
}
