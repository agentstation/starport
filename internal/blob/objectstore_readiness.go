package blob

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// probePublication retains its markers. Delayed probe writes must not replace
// them. Incomplete multipart uploads need the deployment's abort lifecycle.
func (o *ObjectStore) probePublication(ctx context.Context) error {
	key := "readiness-" + rand.Text()
	const payload = "conditional-publication-probe"
	if _, err := o.Publish(ctx, key, strings.NewReader(payload)); err != nil {
		return err
	}
	if err := o.expectPublicationRefused(ctx, key); err != nil {
		return err
	}
	reader, err := o.ReadPublished(ctx, key)
	if err != nil {
		return err
	}
	data, readErr := io.ReadAll(io.LimitReader(reader, int64(len(payload)+1)))
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil {
		return errors.Join(readErr, closeErr)
	}
	if string(data) != payload {
		return ErrConditionalPublicationUnsupported
	}
	if err := o.Retire(ctx, key); err != nil {
		return err
	}
	if err := o.expectPublicationRefused(ctx, key); err != nil {
		return err
	}
	if err := o.expectRetired(ctx, key); err != nil {
		return err
	}
	// Verify ordinary multipart completion before testing its refusal. A server
	// that rejects all completions does not satisfy the publication contract.
	if err := o.probeMultipart(ctx, "readiness-"+rand.Text(), "new"); err != nil {
		return err
	}
	if err := o.probeMultipart(ctx, "readiness-"+rand.Text(), "live"); err != nil {
		return err
	}
	return o.probeMultipart(ctx, "readiness-"+rand.Text(), "retired")
}

func (o *ObjectStore) expectPublicationRefused(ctx context.Context, key string) error {
	_, err := o.Publish(ctx, key, strings.NewReader("replacement"))
	if errors.Is(err, ErrPublicationExists) {
		return nil
	}
	if err == nil {
		return ErrConditionalPublicationUnsupported
	}
	return err
}

func (o *ObjectStore) expectRetired(ctx context.Context, key string) error {
	reader, err := o.ReadPublished(ctx, key)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if reader != nil {
		_ = reader.Close()
	}
	if err == nil {
		return ErrConditionalPublicationUnsupported
	}
	return err
}

func (o *ObjectStore) probeMultipart(ctx context.Context, key string, state string) error {
	object := aws.String(o.objectKey(blobAddress(retainedDir, key)))
	bucket := aws.String(o.bucket)
	created, err := o.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket: bucket, Key: object, Metadata: map[string]string{retainedObjectMetadataKey: liveObjectMetadata},
	})
	if err != nil {
		return fmt.Errorf("blob: create readiness upload: %w", err)
	}
	if aws.ToString(created.UploadId) == "" {
		return ErrConditionalPublicationUnsupported
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = o.client.AbortMultipartUpload(cleanup, &s3.AbortMultipartUploadInput{Bucket: bucket, Key: object, UploadId: created.UploadId})
	}()
	const payload = liveEnvelope + "multipart-readiness"
	part, err := o.client.UploadPart(ctx, &s3.UploadPartInput{Bucket: bucket, Key: object, UploadId: created.UploadId, PartNumber: aws.Int32(1), Body: strings.NewReader(payload)})
	if err != nil {
		return err
	}
	if state == "live" {
		if _, err := o.Publish(ctx, key, strings.NewReader("original")); err != nil {
			return err
		}
	}
	if state == "retired" {
		if err := o.Retire(ctx, key); err != nil {
			return err
		}
	}
	_, err = o.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket: bucket, Key: object, UploadId: created.UploadId, IfNoneMatch: aws.String("*"),
		MultipartUpload: &s3types.CompletedMultipartUpload{Parts: []s3types.CompletedPart{{ETag: part.ETag, PartNumber: aws.Int32(1)}}},
	})
	if state != "new" {
		if !isPublicationConflict(err) {
			if err == nil {
				return ErrConditionalPublicationUnsupported
			}
			return err
		}
		if state == "retired" {
			return o.expectRetired(ctx, key)
		}
		reader, err := o.ReadPublished(ctx, key)
		if err != nil {
			return err
		}
		data, readErr := io.ReadAll(io.LimitReader(reader, 9))
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil {
			return errors.Join(readErr, closeErr)
		}
		if string(data) != "original" {
			return ErrConditionalPublicationUnsupported
		}
		return o.Retire(ctx, key)
	}
	if err != nil {
		return err
	}
	reader, err := o.ReadPublished(ctx, key)
	if err != nil {
		return err
	}
	data, readErr := io.ReadAll(io.LimitReader(reader, int64(len(payload)+1)))
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil {
		return errors.Join(readErr, closeErr)
	}
	if string(data) != strings.TrimPrefix(payload, liveEnvelope) {
		return ErrConditionalPublicationUnsupported
	}
	return o.Retire(ctx, key)
}
