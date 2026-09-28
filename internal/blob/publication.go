package blob

import (
	"context"
	"errors"
	"io"
)

var (
	ErrPublicationExists  = errors.New("blob: publication identity already exists")
	ErrCorruptPublication = errors.New("blob: corrupt publication envelope")
)

const (
	retainedDir     = "retained-v1"
	liveEnvelope    = "SPBLOB1L"
	retiredEnvelope = "SPBLOB1R"
)

// PublicationStore owns immutable bytes and permanent retirement at each key.
// Its namespace is separate from mutable Put/Get objects. An error can mean
// that a write succeeded but its acknowledgment was lost.
//
// Retirement markers must survive restart and backup restoration. A caller
// must not remove them while an earlier writer can resume. Payload quota does
// not include these markers. Operators must budget for their retained storage.
type PublicationStore interface {
	// EnsurePublicationReady checks capability before new work needs durable bytes.
	EnsurePublicationReady(context.Context) error
	// Publish creates a complete object only under an unused identity.
	// Publish never replaces existing live bytes or retirement markers.
	Publish(context.Context, string, io.Reader) (Info, error)
	// ReadPublished opens live bytes. Missing and retired identities return ErrNotFound.
	ReadPublished(context.Context, string) (io.ReadCloser, error)
	// StatPublished reports the payload length, excluding the private envelope.
	StatPublished(context.Context, string) (Info, error)
	// Retire durably prevents every later publication, including an earlier
	// writer that resumes. Only success permits the owner to release quota.
	Retire(context.Context, string) error
}

func readEnvelope(reader io.Reader) error {
	var header [len(liveEnvelope)]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return errors.Join(ErrCorruptPublication, err)
	}
	switch string(header[:]) {
	case liveEnvelope:
		return nil
	case retiredEnvelope:
		return ErrNotFound
	default:
		return ErrCorruptPublication
	}
}
