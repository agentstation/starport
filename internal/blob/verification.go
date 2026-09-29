package blob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
)

// VerifyPublished checks retained payload length and an optional content digest.
// It streams bytes and never changes publication or retirement state.
func VerifyPublished(ctx context.Context, source PublicationReader, key string, size int64, digest string) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if source == nil || size < 0 {
		return ErrCorruptPublication
	}
	if digest != "" {
		decoded, err := hex.DecodeString(digest)
		if err != nil || len(decoded) != sha256.Size {
			return ErrCorruptPublication
		}
	}
	reader, err := source.ReadPublished(ctx, key)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, reader.Close()) }()
	hash := sha256.New()
	if _, err := io.CopyN(hash, &contextReader{ctx: ctx, r: reader}, size); err != nil {
		return errors.Join(ErrCorruptPublication, err)
	}
	var extra [1]byte
	if n, err := io.ReadFull(&contextReader{ctx: ctx, r: reader}, extra[:]); n != 0 || !errors.Is(err, io.EOF) {
		return ErrCorruptPublication
	}
	if digest != "" && hex.EncodeToString(hash.Sum(nil)) != digest {
		return ErrCorruptPublication
	}
	return nil
}
