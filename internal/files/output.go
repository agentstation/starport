package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"math"
	"strings"
	"time"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/storage"
)

var (
	ErrOutputIncomplete = errors.New("files: output is incomplete")
	ErrOutputExpired    = errors.New("files: output retention expired")
	ErrOutputConflict   = errors.New("files: output differs from retained evidence")
)

const outputWriteTimeout = 10 * time.Minute

// PrepareOutput retains one output identity before its producer starts.
// Repeated preparation preserves the original expiry and byte claim.
func (s *Service) PrepareOutput(ctx context.Context, account, identity, name string, bound int64) (File, error) {
	if strings.TrimSpace(account) == "" || strings.TrimSpace(identity) == "" || len(identity) > 512 || bound < 0 {
		return File{}, ErrInvalidFile
	}
	if err := s.blobs.EnsurePublicationReady(ctx); err != nil {
		return File{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, outputWriteTimeout)
	defer cancel()
	sum := sha256.Sum256([]byte(account + "\x00" + identity))
	id := "file-" + hex.EncodeToString(sum[:])
	existing, err := s.records.Get(ctx, account, id)
	if err == nil {
		return s.sameOutput(existing, identity, name, bound)
	}
	if !errors.Is(err, ErrFileNotFound) {
		return File{}, err
	}
	now := s.now().UTC()
	file := File{ID: id, Account: account, Filename: name, Purpose: PurposeBatchOutput, State: FileStatePending, CreatedAt: now, ExpiresAt: now.Add(s.retention), blobKey: newBlobKey(), metered: s.meter != nil, outputIdentity: identity, outputBound: bound}
	if err := file.Validate(); err != nil {
		return File{}, err
	}
	if err := s.prepare(ctx, file, bound); err != nil {
		// An exact retry can recover a successful metadata write whose reply was lost.
		existing, readErr := s.records.Get(ctx, account, id)
		if readErr == nil {
			return s.sameOutput(existing, identity, name, bound)
		}
		return File{}, err
	}
	return file, nil
}

func (s *Service) sameOutput(file File, identity, name string, bound int64) (File, error) {
	if file.outputIdentity != identity || file.Filename != name || file.outputBound != bound || file.Purpose != PurposeBatchOutput {
		return File{}, ErrOutputConflict
	}
	if file.Expired(s.now().UTC()) {
		return File{}, ErrOutputExpired
	}
	if file.State == FileStateDeleting {
		return File{}, ErrOutputConflict
	}
	return file, nil
}

// CommitOutput pins the expected bytes before writing them. A retry can only
// write that same content. Failed writes retain metadata for recovery.
func (s *Service) CommitOutput(ctx context.Context, account, id string, size int64, digest string, content io.Reader) (File, error) {
	if size < 0 || size == math.MaxInt64 || content == nil || !validOutputDigest(digest) {
		return File{}, ErrOutputConflict
	}
	ctx, cancel := context.WithTimeout(ctx, outputWriteTimeout)
	defer cancel()
	file, err := s.pinOutput(ctx, account, id, size, digest)
	if err != nil {
		return File{}, err
	}
	if file.State == FileStateReady {
		return s.RecoverOutput(ctx, account, id)
	}
	if s.meter != nil {
		if err := s.meter.Resize(ctx, account, id, size); err != nil {
			return File{}, err
		}
	}
	verified := &outputReader{reader: content, hash: sha256.New(), remaining: size, digest: digest}
	info, err := s.blobs.Publish(ctx, file.blobKey, verified)
	if errors.Is(err, blob.ErrPublicationExists) {
		return s.RecoverOutput(ctx, account, id)
	}
	if err != nil {
		return File{}, err
	}
	if !verified.complete || info.Size != size {
		return File{}, ErrOutputConflict
	}
	return s.publishOutput(ctx, file)
}

func (s *Service) pinOutput(ctx context.Context, account, id string, size int64, digest string) (File, error) {
	for range 8 {
		file, err := s.records.Get(ctx, account, id)
		if err != nil {
			return File{}, err
		}
		if file.outputIdentity == "" || file.State == FileStateDeleting {
			return File{}, ErrOutputConflict
		}
		if file.Expired(s.now().UTC()) {
			return File{}, ErrOutputExpired
		}
		if file.outputDigest != "" {
			if file.outputDigest != digest || file.Bytes != size {
				return File{}, ErrOutputConflict
			}
			return file, nil
		}
		file.outputDigest = digest
		file.Bytes = size
		err = s.records.Replace(ctx, file)
		if errors.Is(err, storage.ErrConflict) {
			continue
		}
		return file, err
	}
	return File{}, storage.ErrConflict
}

// RecoverOutput verifies retained bytes before completing interrupted publication.
// Missing bytes remain incomplete. It never asks a provider to generate them.
func (s *Service) RecoverOutput(ctx context.Context, account, id string) (File, error) {
	ctx, cancel := context.WithTimeout(ctx, outputWriteTimeout)
	defer cancel()
	file, err := s.records.Get(ctx, account, id)
	if err != nil {
		return File{}, err
	}
	if file.outputIdentity == "" || file.State == FileStateDeleting {
		return File{}, ErrOutputConflict
	}
	if file.Expired(s.now().UTC()) {
		return File{}, ErrOutputExpired
	}
	if file.outputDigest == "" {
		return File{}, ErrOutputIncomplete
	}
	reader, err := s.blobs.ReadPublished(ctx, file.blobKey)
	if err != nil {
		return File{}, errors.Join(ErrOutputIncomplete, err)
	}
	defer func() { _ = reader.Close() }()
	verified := &outputReader{reader: reader, hash: sha256.New(), remaining: file.Bytes, digest: file.outputDigest}
	if _, err := io.Copy(io.Discard, verified); err != nil {
		return File{}, err
	}
	if !verified.complete {
		return File{}, ErrOutputIncomplete
	}
	if s.meter != nil {
		if err := s.meter.Resize(ctx, account, id, file.Bytes); err != nil {
			return File{}, err
		}
	}
	if file.State == FileStateReady {
		return file, nil
	}
	return s.publishOutput(ctx, file)
}

func (s *Service) publishOutput(ctx context.Context, file File) (File, error) {
	if file.Expired(s.now().UTC()) {
		return File{}, ErrOutputExpired
	}
	file.State = FileStateReady
	if err := s.records.Replace(ctx, file); err != nil {
		// Preserve an accepted publication after a lost acknowledgment.
		current, readErr := s.records.Get(ctx, file.Account, file.ID)
		if readErr == nil && current == file {
			return current, nil
		}
		return File{}, err
	}
	return file, nil
}

func validOutputDigest(digest string) bool {
	bytes, err := hex.DecodeString(digest)
	return err == nil && len(bytes) == sha256.Size && digest == strings.ToLower(digest)
}

type outputReader struct {
	reader    io.Reader
	hash      hash.Hash
	remaining int64
	digest    string
	complete  bool
}

func (r *outputReader) Read(p []byte) (int, error) {
	if r.complete {
		return 0, io.EOF
	}
	if int64(len(p)) > r.remaining+1 {
		p = p[:r.remaining+1]
	}
	n, err := r.reader.Read(p)
	if int64(n) > r.remaining {
		return 0, ErrOutputConflict
	}
	_, _ = r.hash.Write(p[:n])
	r.remaining -= int64(n)
	if errors.Is(err, io.EOF) {
		if r.remaining != 0 || hex.EncodeToString(r.hash.Sum(nil)) != r.digest {
			return 0, ErrOutputConflict
		}
		r.complete = true
	}
	return n, err
}
