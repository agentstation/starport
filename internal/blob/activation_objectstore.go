package blob

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type objectActivation struct{ store *ObjectStore }

func (t objectRestoreTarget) ActivateImport(ctx context.Context, operation string, expected Snapshot, decisionSHA256 string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	claim, err := makeBlobClaim(operation, expected)
	if err != nil {
		return err
	}
	return activateBlobImport(ctx, objectActivation(t), claim, decisionSHA256)
}

func (o objectActivation) get(ctx context.Context, name string) ([]byte, string, error) {
	result, err := o.store.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(o.store.bucket), Key: aws.String(o.store.objectKey(name))})
	if isAbsent(err) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	value, readErr := io.ReadAll(io.LimitReader(result.Body, 4097))
	if err := errors.Join(readErr, result.Body.Close()); err != nil {
		return nil, "", err
	}
	if len(value) > 4096 || aws.ToString(result.ETag) == "" {
		return nil, "", ErrActivationConflict
	}
	return value, *result.ETag, nil
}
func (o objectActivation) read(ctx context.Context, name string) ([]byte, error) {
	value, _, err := o.get(ctx, name)
	return value, err
}
func (o objectActivation) compare(ctx context.Context, name string, previous, value []byte) error {
	current, tag, err := o.get(ctx, name)
	if err == nil && bytes.Equal(current, value) {
		return nil
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if (err == nil && (previous == nil || !bytes.Equal(current, previous))) || (errors.Is(err, ErrNotFound) && previous != nil) {
		return ErrActivationConflict
	}
	input := &s3.PutObjectInput{Bucket: aws.String(o.store.bucket), Key: aws.String(o.store.objectKey(name)), Body: bytes.NewReader(value)}
	if err == nil {
		input.IfMatch = aws.String(tag)
	} else {
		input.IfNoneMatch = aws.String("*")
	}
	_, err = o.store.client.PutObject(ctx, input)
	if isPublicationConflict(err) {
		current, readErr := o.read(ctx, name)
		if readErr == nil && bytes.Equal(current, value) {
			return nil
		}
		return errors.Join(ErrActivationConflict, readErr)
	}
	return err
}
func (o objectActivation) prepare(ctx context.Context) error {
	if err := o.store.probeActivationConditions(ctx); err != nil {
		return err
	}
	if err := o.store.readLayout(ctx); err == nil {
		return nil
	} else if !isAbsent(err) {
		return err
	}
	_, err := o.store.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(o.store.bucket), Key: aws.String(o.store.objectKey(blobLayoutKey)), Body: strings.NewReader(blobLayoutVersion), IfNoneMatch: aws.String("*")})
	if err != nil && !isPublicationConflict(err) {
		return err
	}
	return o.store.readLayout(ctx)
}
func (o objectActivation) confirm(ctx context.Context) error { return ctx.Err() }

var _ ImportActivator = objectRestoreTarget{}
