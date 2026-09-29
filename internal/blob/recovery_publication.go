package blob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// PublicationState distinguishes an unused identity from permanent retirement.
// Digest and size refer to payload bytes, excluding the retained envelope.
type PublicationState struct {
	Kind   string `json:"kind"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// RecoveryPublicationReader exposes exact retained identity state without writes.
type RecoveryPublicationReader interface {
	InspectPublication(context.Context, string) (PublicationState, error)
}

func inspectPublicationBytes(ctx context.Context, input io.Reader) (PublicationState, error) {
	var header [len(liveEnvelope)]byte
	if _, err := io.ReadFull(input, header[:]); err != nil {
		return PublicationState{}, errors.Join(ErrCorruptPublication, err)
	}
	switch string(header[:]) {
	case retiredEnvelope:
		var extra [1]byte
		n, err := input.Read(extra[:])
		if n != 0 || !errors.Is(err, io.EOF) {
			return PublicationState{}, ErrCorruptPublication
		}
		return PublicationState{Kind: publicationRetired}, nil
	case liveEnvelope:
		hash := sha256.New()
		size, err := io.Copy(hash, io.LimitReader(&contextReader{ctx: ctx, r: input}, ImportPublicationMaxBytes+1))
		if err != nil || size > ImportPublicationMaxBytes {
			return PublicationState{}, errors.Join(ErrCorruptPublication, err)
		}
		return PublicationState{Kind: publicationLive, Size: size, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
	default:
		return PublicationState{}, ErrCorruptPublication
	}
}

// InspectPublication reads complete retained bytes and checks their digest.
func (f *Filesystem) InspectPublication(ctx context.Context, key string) (result PublicationState, resultErr error) {
	if ctx == nil || ValidateKey(key) != nil {
		return result, ErrCorruptPublication
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	input, err := os.Open(f.publicationPath(key))
	if errors.Is(err, os.ErrNotExist) {
		return PublicationState{Kind: publicationAbsent}, nil
	}
	if err != nil {
		return result, err
	}
	defer func() {
		resultErr = errors.Join(resultErr, input.Close())
		if resultErr != nil {
			result = PublicationState{}
		}
	}()
	return inspectPublicationBytes(ctx, input)
}

// InspectPublication reads complete retained bytes without creating layout records.
func (o *ObjectStore) InspectPublication(ctx context.Context, key string) (result PublicationState, resultErr error) {
	if ctx == nil || ValidateKey(key) != nil {
		return result, ErrCorruptPublication
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	response, err := o.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(o.bucket), Key: aws.String(o.objectKey(blobAddress(retainedDir, key)))})
	if isAbsent(err) {
		return PublicationState{Kind: publicationAbsent}, nil
	}
	if err != nil {
		return result, err
	}
	defer func() {
		resultErr = errors.Join(resultErr, response.Body.Close())
		if resultErr != nil {
			result = PublicationState{}
		}
	}()
	result, err = inspectPublicationBytes(ctx, response.Body)
	if err != nil {
		return result, err
	}
	want := liveObjectMetadata
	if result.Kind == publicationRetired {
		want = retiredObjectMetadata
	}
	if response.Metadata[retainedObjectMetadataKey] != want {
		return PublicationState{}, ErrCorruptPublication
	}
	return result, nil
}

func (s *snapshotView) InspectPublication(ctx context.Context, key string) (PublicationState, error) {
	return s.store.InspectPublication(ctx, key)
}
