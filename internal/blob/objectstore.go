package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"

	//nolint:staticcheck // The replacement, feature/s3/transfermanager, is a
	// pre-1.0 module. This package holds durable bytes, so it waits for a
	// stable API rather than tracking one that may still change.
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// objectStoreBackend is the name an operator reads at startup.
const objectStoreBackend = "objectstore"

// ObjectStoreOptions addresses one S3-compatible bucket.
//
// One client reaches AWS S3, Cloudflare R2, MinIO, and Backblaze B2, because
// each of them serves the same API. Endpoint selects the implementation, and
// an absent Endpoint selects AWS itself.
type ObjectStoreOptions struct {
	Bucket   string
	Region   string
	Endpoint string
	Prefix   string

	// AccessKeyID and SecretAccessKey state static credentials. Both empty
	// selects the ambient AWS credential chain.
	AccessKeyID     string
	SecretAccessKey string
}

// ObjectStore stores objects in an S3-compatible bucket. It serves every node
// of a deployment, which the filesystem backend cannot.
type ObjectStore struct {
	client *s3.Client
	//nolint:staticcheck // See the import comment.
	uploader *manager.Uploader
	bucket   string
	prefix   string
}

// NewObjectStore opens a store against the bucket the options name.
//
// It builds the client and returns. It does not reach the bucket, because a
// network call at construction would make startup depend on a remote service
// that the first upload will reach anyway.
func NewObjectStore(ctx context.Context, options ObjectStoreOptions) (*ObjectStore, error) {
	if strings.TrimSpace(options.Bucket) == "" {
		return nil, errors.New("blob: the object store names no bucket")
	}

	loadOptions := []func(*awsconfig.LoadOptions) error{}
	if options.Region != "" {
		loadOptions = append(loadOptions, awsconfig.WithRegion(options.Region))
	}
	if options.AccessKeyID != "" && options.SecretAccessKey != "" {
		loadOptions = append(loadOptions, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(options.AccessKeyID, options.SecretAccessKey, ""),
		))
	}
	awsConfig, err := awsconfig.LoadDefaultConfig(ctx, loadOptions...)
	if err != nil {
		// The message carries the reason the SDK gave for the chain, and the
		// chain never puts a secret in it.
		return nil, fmt.Errorf("blob: load the object store credentials: %w", err)
	}

	client := s3.NewFromConfig(awsConfig, func(o *s3.Options) {
		if options.Endpoint != "" {
			o.BaseEndpoint = aws.String(options.Endpoint)
			// An implementation other than AWS usually serves one bucket at a
			// path rather than at a subdomain, and a bucket name in a hostname
			// needs DNS that a private deployment rarely has.
			o.UsePathStyle = true
		}
	})

	return &ObjectStore{
		client: client,
		//nolint:staticcheck // See the import comment.
		uploader: manager.NewUploader(client),
		bucket:   options.Bucket,
		prefix:   strings.Trim(strings.TrimSpace(options.Prefix), "/"),
	}, nil
}

// Backend implements Store.
func (o *ObjectStore) Backend() string { return objectStoreBackend }

// objectKey scopes a key under the configured prefix, so one bucket holds more
// than one deployment without either one reading the other's objects.
func (o *ObjectStore) objectKey(key string) string {
	if o.prefix == "" {
		return key
	}
	return o.prefix + "/" + key
}

// Put implements Store.
//
// The uploader sends the whole object in one request, or in parts when the
// stream is large. Either way the object becomes reachable only after the last
// part lands, so a failed put leaves no readable object at a key that held
// none, and leaves the prior object intact at a key that did.
func (o *ObjectStore) Put(ctx context.Context, key string, r io.Reader) (Info, error) {
	if err := ValidateKey(key); err != nil {
		return Info{}, err
	}
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	//nolint:staticcheck // See the import comment.
	if _, err := o.uploader.Upload(ctx, &s3.PutObjectInput{
		Bucket: aws.String(o.bucket),
		Key:    aws.String(o.objectKey(key)),
		Body:   &contextReader{ctx: ctx, r: r},
	}); err != nil {
		return Info{}, uploadError(err)
	}
	// The size comes from the store rather than from a count of the bytes
	// read. The uploader may read the stream more than once when it retries a
	// part, and a local counter would then report more bytes than the object
	// holds.
	return o.Stat(ctx, key)
}

// Get implements Store.
func (o *ObjectStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ValidateKey(key); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	output, err := o.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(o.bucket),
		Key:    aws.String(o.objectKey(key)),
	})
	if err != nil {
		if isAbsent(err) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, key)
		}
		return nil, fmt.Errorf("blob: open the object: %w", err)
	}
	return output.Body, nil
}

// Stat implements Store.
func (o *ObjectStore) Stat(ctx context.Context, key string) (Info, error) {
	if err := ValidateKey(key); err != nil {
		return Info{}, err
	}
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	output, err := o.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(o.bucket),
		Key:    aws.String(o.objectKey(key)),
	})
	if err != nil {
		if isAbsent(err) {
			return Info{}, fmt.Errorf("%w: %s", ErrNotFound, key)
		}
		return Info{}, fmt.Errorf("blob: stat the object: %w", err)
	}
	return Info{Key: key, Size: aws.ToInt64(output.ContentLength)}, nil
}

// Delete implements Store.
func (o *ObjectStore) Delete(ctx context.Context, key string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := o.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(o.bucket),
		Key:    aws.String(o.objectKey(key)),
	}); err != nil && !isAbsent(err) {
		return fmt.Errorf("blob: delete the object: %w", err)
	}
	return nil
}

// uploadError unwraps the reason an upload stopped.
//
// The uploader wraps the reader's error in its own, and a caller that watches
// for its own sentinel needs to find it. Cancellation matters most: a caller
// that cannot tell a canceled request from a failed bucket would retry an
// upload nobody is waiting for.
func uploadError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("blob: write the object: %w", err)
}

// isAbsent reports the several shapes an S3-compatible store gives a missing
// key. GetObject answers with a typed NoSuchKey. HeadObject carries no body,
// so it answers with a typed NotFound. An implementation other than AWS
// sometimes returns neither, and only the status separates a missing object
// from a broken one.
func isAbsent(err error) bool {
	var noSuchKey *s3types.NoSuchKey
	if errors.As(err, &noSuchKey) {
		return true
	}
	var notFound *s3types.NotFound
	if errors.As(err, &notFound) {
		return true
	}
	var response *smithyhttp.ResponseError
	if errors.As(err, &response) {
		return response.HTTPStatusCode() == http.StatusNotFound
	}
	return false
}
