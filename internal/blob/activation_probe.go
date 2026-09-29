package blob

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// probeActivationConditions tests conditional replacement on
// an owned disposable object before an activation can release its barrier.
func (o *ObjectStore) probeActivationConditions(ctx context.Context) (resultErr error) {
	name := blobAddress(objectsDir, "activation-probe-"+rand.Text())
	key := o.objectKey(name)
	const original = "activation-condition-probe"
	_, err := o.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(o.bucket), Key: aws.String(key), Body: strings.NewReader(original), IfNoneMatch: aws.String("*")})
	if err != nil {
		return err
	}
	defer func() {
		// Only this invocation owns the random probe name. Cleanup never reaches a
		// recovery receipt or import barrier, even on an unsupported service.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, err := o.client.DeleteObject(cleanup, &s3.DeleteObjectInput{Bucket: aws.String(o.bucket), Key: aws.String(key)})
		resultErr = errors.Join(resultErr, err)
	}()
	records := objectActivation{store: o}
	value, tag, err := records.get(ctx, name)
	if err != nil || string(value) != original {
		return fmt.Errorf("probe stage failed: %w", errors.Join(ErrConditionalPublicationUnsupported, err))
	}

	_, err = o.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(o.bucket), Key: aws.String(key), Body: strings.NewReader("incorrect"), IfNoneMatch: aws.String("*")})
	if !isPublicationConflict(err) {
		return errors.Join(ErrConditionalPublicationUnsupported, err)
	}
	wrong := aws.String("\"00000000000000000000000000000000\"")
	_, err = o.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(o.bucket), Key: aws.String(key), Body: strings.NewReader("incorrect"), IfMatch: wrong})
	if !isPublicationConflict(err) {
		return fmt.Errorf("conditional replacement: %w", errors.Join(ErrConditionalPublicationUnsupported, err))
	}
	value, after, err := records.get(ctx, name)
	if err != nil || string(value) != original || after != tag {
		return errors.Join(ErrConditionalPublicationUnsupported, err)
	}
	// A matching conditional replacement must also publish the intended bytes.
	_, err = o.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(o.bucket), Key: aws.String(key), Body: strings.NewReader("accepted"), IfMatch: aws.String(tag)})
	if err != nil {
		return err
	}
	value, _, err = records.get(ctx, name)
	if err != nil || string(value) != "accepted" {
		return errors.Join(ErrConditionalPublicationUnsupported, err)
	}
	return nil
}
