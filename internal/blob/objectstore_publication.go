package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// Publish requires conditional creation for both single-part and multipart
// completion. The configured S3 service must enforce If-None-Match: *.
func (o *ObjectStore) Publish(ctx context.Context, key string, r io.Reader) (Info, error) {
	if err := ValidateKey(key); err != nil {
		return Info{}, err
	}
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	//nolint:staticcheck // The stable uploader copies the condition to multipart completion.
	_, err := o.uploader.Upload(ctx, &s3.PutObjectInput{
		Bucket: aws.String(o.bucket), Key: aws.String(o.objectKey(retainedDir + "/" + key)),
		Body:        &contextReader{ctx: ctx, r: io.MultiReader(strings.NewReader(liveEnvelope), r)},
		IfNoneMatch: aws.String("*"), Metadata: map[string]string{retainedObjectMetadataKey: liveObjectMetadata},
	})
	if err != nil {
		if isPublicationConflict(err) {
			return Info{}, ErrPublicationExists
		}
		return Info{}, uploadError(err)
	}
	return o.StatPublished(ctx, key)
}

// Retire writes a real object, never a delete marker. S3 permits conditional
// creation over a delete marker, which would let an old upload reappear.
func (o *ObjectStore) Retire(ctx context.Context, key string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := o.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(o.bucket), Key: aws.String(o.objectKey(retainedDir + "/" + key)),
		Body: strings.NewReader(retiredEnvelope), Metadata: map[string]string{retainedObjectMetadataKey: "retired-v1"},
	})
	if err != nil {
		return fmt.Errorf("blob: retire publication: %w", err)
	}
	return nil
}

// ReadPublished checks the same envelope as the filesystem backend.
func (o *ObjectStore) ReadPublished(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ValidateKey(key); err != nil {
		return nil, err
	}
	output, err := o.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(o.bucket), Key: aws.String(o.objectKey(retainedDir + "/" + key)),
	})
	if isAbsent(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := readEnvelope(output.Body); err != nil {
		_ = output.Body.Close()
		return nil, err
	}
	return output.Body, nil
}

// StatPublished checks publication metadata from the atomic object write.
func (o *ObjectStore) StatPublished(ctx context.Context, key string) (Info, error) {
	if err := ValidateKey(key); err != nil {
		return Info{}, err
	}
	output, err := o.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(o.bucket), Key: aws.String(o.objectKey(retainedDir + "/" + key)),
	})
	if isAbsent(err) {
		return Info{}, ErrNotFound
	}
	if err != nil {
		return Info{}, err
	}
	switch output.Metadata[retainedObjectMetadataKey] {
	case "retired-v1":
		return Info{}, ErrNotFound
	case liveObjectMetadata:
		if aws.ToInt64(output.ContentLength) >= int64(len(liveEnvelope)) {
			return Info{Key: key, Size: aws.ToInt64(output.ContentLength) - int64(len(liveEnvelope))}, nil
		}
	}
	return Info{}, ErrCorruptPublication
}

func isPublicationConflict(err error) bool {
	var response *smithyhttp.ResponseError
	return errors.As(err, &response) && response.HTTPStatusCode() == http.StatusPreconditionFailed
}
