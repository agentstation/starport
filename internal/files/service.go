package files

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/agentstation/starport/internal/blob"
)

// DefaultPendingGrace is how long a pending record may stay pending before a
// sweep treats it as abandoned.
//
// It is longer than any upload this gateway accepts, because a sweep that ran
// while an upload was still streaming would delete the bytes out from under it.
const DefaultPendingGrace = time.Hour

var (
	// ErrServiceRequired reports a service built without a record store or a
	// byte store.
	ErrServiceRequired = errors.New("files: a record store and a byte store are required")
)

// Service writes a file as two writes and keeps them consistent.
//
// The record comes first, then the bytes, then the commit. The order matters:
// a process that stops after the first write leaves a pending record that
// names its bytes, and the sweep can find and delete both. The opposite order
// would leave bytes that no record names, and the byte store lists no keys, so
// nothing could ever find them again.
type Service struct {
	records      Repository
	blobs        blob.Store
	now          func() time.Time
	pendingGrace time.Duration
}

// Option changes one service setting.
type Option func(*Service)

// WithClock replaces the source of time. A test uses it to age a record
// without waiting.
func WithClock(now func() time.Time) Option {
	return func(s *Service) {
		if now != nil {
			s.now = now
		}
	}
}

// WithPendingGrace sets how long a pending record may stay pending.
func WithPendingGrace(grace time.Duration) Option {
	return func(s *Service) {
		if grace > 0 {
			s.pendingGrace = grace
		}
	}
}

// NewService builds a file service over a record store and a byte store.
func NewService(records Repository, blobs blob.Store, options ...Option) (*Service, error) {
	if records == nil || blobs == nil {
		return nil, ErrServiceRequired
	}
	service := &Service{
		records:      records,
		blobs:        blobs,
		now:          time.Now,
		pendingGrace: DefaultPendingGrace,
	}
	for _, option := range options {
		option(service)
	}
	return service, nil
}

// UploadRequest names everything about a file except its bytes.
type UploadRequest struct {
	Tenant    string
	Filename  string
	Purpose   Purpose
	ExpiresAt time.Time
}

// Upload stores the bytes and returns the readable record.
func (s *Service) Upload(ctx context.Context, request UploadRequest, r io.Reader) (File, error) {
	if r == nil {
		return File{}, fmt.Errorf("%w: it carries no bytes", ErrInvalidFile)
	}
	if !request.Purpose.Valid() {
		return File{}, fmt.Errorf("%w: %q", ErrInvalidPurpose, request.Purpose)
	}

	created := s.now().UTC()
	pending := File{
		ID:        newFileID(),
		Tenant:    strings.TrimSpace(request.Tenant),
		Filename:  request.Filename,
		Purpose:   request.Purpose,
		State:     FileStatePending,
		CreatedAt: created,
		ExpiresAt: request.ExpiresAt,
		blobKey:   newBlobKey(),
	}
	if err := s.records.Create(ctx, pending); err != nil {
		return File{}, err
	}

	info, err := s.blobs.Put(ctx, pending.blobKey, r)
	if err != nil {
		// The record is the only thing that names the bytes, so it goes last
		// and comes back first. A failure here leaves the record for the
		// sweep, which is why the sweep deletes the bytes before the record.
		s.discard(ctx, pending)
		return File{}, fmt.Errorf("files: store the bytes: %w", err)
	}

	committed := pending
	committed.Bytes = info.Size
	committed.State = FileStateReady
	if err := s.records.Replace(ctx, committed); err != nil {
		s.discard(ctx, pending)
		return File{}, err
	}
	return committed, nil
}

// Get returns one readable file. A pending record reads as not found, because
// a caller that could see it could also read bytes that never finished
// landing.
func (s *Service) Get(ctx context.Context, tenant, id string) (File, error) {
	file, err := s.records.Get(ctx, tenant, id)
	if err != nil {
		return File{}, err
	}
	if file.State != FileStateReady {
		return File{}, ErrFileNotFound
	}
	return file, nil
}

// Open returns the record and a reader over its bytes.
func (s *Service) Open(ctx context.Context, tenant, id string) (File, io.ReadCloser, error) {
	file, err := s.Get(ctx, tenant, id)
	if err != nil {
		return File{}, nil, err
	}
	reader, err := s.blobs.Get(ctx, file.blobKey)
	if err != nil {
		if errors.Is(err, blob.ErrNotFound) {
			// The record outlived its bytes. A caller asked for a file that no
			// longer reads, and not-found is the honest answer.
			return File{}, nil, ErrFileNotFound
		}
		return File{}, nil, fmt.Errorf("files: read the bytes: %w", err)
	}
	return file, reader, nil
}

// List returns the readable files one tenant owns.
func (s *Service) List(ctx context.Context, tenant string, limit int) ([]File, error) {
	records, err := s.records.List(ctx, tenant, limit)
	if err != nil {
		return nil, err
	}
	readable := make([]File, 0, len(records))
	for _, file := range records {
		if file.State == FileStateReady {
			readable = append(readable, file)
		}
	}
	return readable, nil
}

// Delete removes a file this tenant owns. FIL5 gives the deleting state its
// own step; here the two writes run in the order the sweep can finish.
func (s *Service) Delete(ctx context.Context, tenant, id string) error {
	file, err := s.records.Get(ctx, tenant, id)
	if err != nil {
		return err
	}
	s.discard(ctx, file)
	return nil
}

// SweepResult counts what one sweep finished.
type SweepResult struct {
	// Abandoned counts pending records older than the grace window.
	Abandoned int
}

// Sweep finishes the work a stopped process left behind.
//
// A pending record older than the grace window names bytes that no caller can
// reach. The sweep deletes the bytes first and the record second, so an
// interrupted sweep leaves a pending record rather than an unreachable object.
func (s *Service) Sweep(ctx context.Context) (SweepResult, error) {
	records, err := s.records.Scan(ctx, 0)
	if err != nil {
		return SweepResult{}, err
	}
	cutoff := s.now().UTC().Add(-s.pendingGrace)
	var result SweepResult
	for _, file := range records {
		if file.State != FileStatePending || file.CreatedAt.After(cutoff) {
			continue
		}
		if err := s.remove(ctx, file); err != nil {
			return result, err
		}
		result.Abandoned++
	}
	return result, nil
}

// remove deletes the bytes and then the record.
func (s *Service) remove(ctx context.Context, file File) error {
	if err := s.blobs.Delete(ctx, file.blobKey); err != nil {
		return fmt.Errorf("files: delete the bytes: %w", err)
	}
	return s.records.Delete(ctx, file.Tenant, file.ID)
}

// discard removes a file and reports nothing. The caller is already returning
// another error, or is deleting on purpose, and a failure here leaves work the
// sweep repeats.
func (s *Service) discard(ctx context.Context, file File) {
	_ = s.blobs.Delete(ctx, file.blobKey)
	_ = s.records.Delete(ctx, file.Tenant, file.ID)
}

// newFileID names a file the way a caller sees it. The prefix makes an
// identifier recognizable in a log line and in a request body.
func newFileID() string {
	return "file-" + strings.ReplaceAll(uuid.NewString(), "-", "")
}

// newBlobKey names the bytes. It is not the file identifier and not derived
// from it, so a leaked identifier names nothing in the byte store, and one
// deployment cannot guess another's objects.
func newBlobKey() string {
	value := uuid.New()
	return hex.EncodeToString(value[:])
}
