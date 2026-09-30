package blob

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const (
	blobLayoutKey     = ".starport/layout"
	blobLayoutVersion = "starport-blob-sha256-v1\n"
	blobImportKey     = ".starport/import"
)

// ErrLayoutUnavailable reports an unverified blob storage format.
var ErrLayoutUnavailable = errors.New("blob: storage layout is unavailable")

// ErrLayoutMigrationRequired refuses an existing bucket without the current layout receipt.
var ErrLayoutMigrationRequired = errors.New("blob: existing objects require explicit layout migration")

// ErrImportRestricted retains the deployment recovery barrier on imported bytes.
var ErrImportRestricted = errors.New("blob: imported bytes require deployment recovery")

// objectControlKey reports native control objects. Snapshots exclude them.
// History objects are domain archive entries and are not control objects.
func objectControlKey(name string) bool {
	switch name {
	case blobLayoutKey, blobImportKey, blobActivationCurrent, blobReplayCurrent, blobClosureKey:
		return true
	}
	return false
}

func (o *ObjectStore) ensureLayout(ctx context.Context) error {
	return o.layout.ensure(ctx, o.probeLayout, ErrLayoutUnavailable)
}

func (o *ObjectStore) probeLayout(ctx context.Context) error {
	if err := checkActivationBarrier(ctx, objectActivation{store: o}); err != nil {
		return err
	}
	if err := o.readLayout(ctx); !isAbsent(err) {
		return err
	}
	// Creating a format receipt requires an empty prefix. Populated prefixes
	// enter through explicit migration while old writers remain fenced.
	list, err := o.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(o.bucket), Prefix: aws.String(o.listPrefix()), MaxKeys: aws.Int32(1)})
	if err != nil {
		return err
	}
	if len(list.Contents) != 0 || aws.ToBool(list.IsTruncated) {
		// A concurrent initializer can publish its receipt before this listing.
		err := o.readLayout(ctx)
		if isAbsent(err) {
			return ErrLayoutMigrationRequired
		}
		return err
	}
	_, err = o.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(o.bucket), Key: aws.String(o.objectKey(blobLayoutKey)), Body: strings.NewReader(blobLayoutVersion), IfNoneMatch: aws.String("*")})
	if err != nil && !isPublicationConflict(err) {
		return err
	}
	return o.readLayout(ctx)
}

func (o *ObjectStore) readLayout(ctx context.Context) error {
	result, err := o.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(o.bucket), Key: aws.String(o.objectKey(blobLayoutKey))})
	if err != nil {
		return err
	}
	data, readErr := io.ReadAll(io.LimitReader(result.Body, int64(len(blobLayoutVersion)+1)))
	if err := errors.Join(readErr, result.Body.Close()); err != nil {
		return err
	}
	if string(data) != blobLayoutVersion {
		return ErrLayoutMigrationRequired
	}
	return nil
}

func (o *ObjectStore) listPrefix() string {
	if o.prefix == "" {
		return ""
	}
	return o.prefix + "/"
}
