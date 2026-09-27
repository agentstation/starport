package files

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/agentstation/starport/internal/blob"
	"github.com/agentstation/starport/internal/storage"
)

// DefaultPendingGrace is how long a pending record may stay pending before a
// sweep treats it as abandoned.
//
// It is longer than any upload this gateway accepts, because a sweep that ran
// while an upload was still streaming would delete the bytes out from under it.
const DefaultPendingGrace = time.Hour

// DefaultRetention is how long a stored file stays readable when an upload
// names no shorter window.
//
// Every file expires. OpenAI keeps an upload until a caller deletes it, and
// Starport does not, because storage that only grows is an unbounded cost and
// an unbounded liability. An operator raises or lowers the window, and an
// upload shortens it, but no upload escapes it.
const DefaultRetention = 30 * 24 * time.Hour

// MinRetention is the shortest window an upload may ask for. A window under an
// hour would expire a file while the request that stored it is still running.
const MinRetention = time.Hour

var (
	// ErrServiceRequired reports a service built without a record store or a
	// byte store.
	ErrServiceRequired = errors.New("files: a record store and a byte store are required")

	// ErrRetentionTooLong reports an upload asking to outlive the window this
	// deployment set. An upload shortens the window and never extends it.
	ErrRetentionTooLong = errors.New("files: the requested retention exceeds the deployment window")

	// ErrRetentionTooShort reports an upload asking for a window under
	// MinRetention.
	ErrRetentionTooShort = errors.New("files: the requested retention is shorter than one hour")
)

// Meter bounds how many bytes one account keeps in storage at a time.
//
// This package names the primitive rather than importing the limit
// vocabulary. A stored file knows its size and its owner, and nothing about
// who set the bound or where the number came from. The storage meter in
// internal/limits satisfies the contract.
type Meter interface {
	InitializeEmpty(context.Context, string) error
	Total(context.Context, string) (int64, error)
	Reserve(ctx context.Context, holder, id string, size, bound int64) error
	Resize(context.Context, string, string, int64) error
	Attachment(context.Context, string, string) (storage.CompareAndSwapMutation, error)
	Release(ctx context.Context, holder, id string) error
	Abort(context.Context, string, string) error
	RecoverPending(context.Context) error
}

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
	retention    time.Duration
	meter        Meter
	sweepMu      sync.Mutex
	sweepCursor  string
	sweepNext    string
	sweepPending []File
	sweepLoaded  bool
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

// WithRetention sets the window every upload gets and no upload exceeds.
func WithRetention(window time.Duration) Option {
	return func(s *Service) {
		if window > 0 {
			s.retention = window
		}
	}
}

// WithMeter counts and bounds account storage. Production supplies a meter
// even when an account has no configured bound.
func WithMeter(meter Meter) Option {
	return func(s *Service) {
		if meter != nil {
			s.meter = meter
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
		retention:    DefaultRetention,
	}
	for _, option := range options {
		option(service)
	}
	return service, nil
}

// UploadRequest names everything about a file except its bytes.
type UploadRequest struct {
	Account  string
	Filename string
	Purpose  Purpose

	// Size is what the caller says the upload weighs, and the service claims
	// it against the bound before it writes a byte. A claim after the write
	// has already spent the storage it was supposed to protect.
	//
	// The service reconciles the claim against the real size once the write
	// lands, so a caller that understated the upload gains nothing.
	Size int64

	// StoredBytesBound is the total this account may keep. Zero leaves the
	// account unbounded, and the service still counts its bytes so a bound set
	// later reads a true number.
	StoredBytesBound int64

	// Retention shortens the window this file gets. A zero value takes the
	// window the deployment set. A longer one is refused rather than clamped,
	// because a caller that asked for a year and silently got a month would
	// find out when the file stopped reading.
	Retention time.Duration
}

// Retention reports the window this deployment gives a stored file.
func (s *Service) Retention() time.Duration { return s.retention }

// expiryFor resolves the moment a file stops reading.
func (s *Service) expiryFor(created time.Time, requested time.Duration) (time.Time, error) {
	window := s.retention
	switch {
	case requested == 0:
	case requested < MinRetention:
		return time.Time{}, fmt.Errorf("%w: %s", ErrRetentionTooShort, requested)
	case requested > window:
		return time.Time{}, fmt.Errorf("%w: %s is longer than %s", ErrRetentionTooLong, requested, window)
	default:
		window = requested
	}
	return created.Add(window), nil
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
	expiresAt, err := s.expiryFor(created, request.Retention)
	if err != nil {
		return File{}, err
	}
	account := strings.TrimSpace(request.Account)

	// The claim goes in before anything is written. A bound checked after the
	// write has already spent the storage it exists to protect.
	held := max(request.Size, 0)

	pending := File{
		ID:        newFileID(),
		Account:   account,
		Filename:  request.Filename,
		Purpose:   request.Purpose,
		Bytes:     held,
		State:     FileStatePending,
		CreatedAt: created,
		ExpiresAt: expiresAt,
		blobKey:   newBlobKey(),
		metered:   s.meter != nil,
	}
	if err := pending.Validate(); err != nil {
		return File{}, err
	}
	if err := s.prepare(ctx, pending, request.StoredBytesBound); err != nil {
		return File{}, err
	}

	info, putErr := s.blobs.Put(ctx, pending.blobKey, r)
	if putErr != nil {
		// The record is the only thing that names the bytes, so it goes last
		// and comes back first. A failure here leaves the record for the
		// sweep, which is why the sweep deletes the bytes before the record.
		s.discard(ctx, pending)
		return File{}, fmt.Errorf("files: store the bytes: %w", putErr)
	}

	// The write knows what the upload really weighed. A caller that understated
	// it pays the difference here, and one that overstated it gets the room
	// back rather than holding storage nothing occupies.
	if err := s.settle(ctx, &pending, info.Size); err != nil {
		s.discard(ctx, pending)
		return File{}, err
	}

	committed := pending
	committed.State = FileStateReady
	if err := s.records.Replace(ctx, committed); err != nil {
		s.discard(ctx, pending)
		return File{}, err
	}
	return committed, nil
}

// prepare attaches one durable byte claim to the pending file.
func (s *Service) prepare(ctx context.Context, file File, bound int64) error {
	if s.meter == nil {
		return s.records.Create(ctx, file)
	}
	if _, err := s.meter.Total(ctx, file.Account); errors.Is(err, storage.ErrNotFound) {
		existing, listErr := s.records.List(ctx, file.Account, 1)
		if listErr != nil {
			return listErr
		}
		if len(existing) > 0 {
			return fmt.Errorf("files: storage accounting requires recovery")
		}
		if err := s.meter.InitializeEmpty(ctx, file.Account); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err := s.meter.Reserve(ctx, file.Account, file.ID, file.Bytes, bound); err != nil {
		return err
	}
	claim, err := s.meter.Attachment(ctx, file.Account, file.ID)
	if err == nil {
		err = s.records.CreateClaimed(ctx, file, claim)
	}
	if err != nil {
		// A lost successful acknowledgment can leave attached metadata. Abort
		// fences only an unattached claim. Recovery retains the published file.
		_ = s.meter.Abort(ctx, file.Account, file.ID)
	}
	return err
}

// settle records the measured size once before the file becomes readable.
func (s *Service) settle(ctx context.Context, pending *File, actual int64) error {
	if s.meter != nil {
		if err := s.meter.Resize(ctx, pending.Account, pending.ID, actual); err != nil {
			return err
		}
	}
	pending.Bytes = actual
	return nil
}

// Get returns one readable file. A pending record reads as not found, because
// a caller that could see it could also read bytes that never finished
// landing. So does a record on its way out, and so does an expired one.
//
// Expiry is decided on the read rather than by the sweep. The sweep runs on an
// interval, and a file that answered for the length of that interval past its
// stated window would make the window a suggestion.
func (s *Service) Get(ctx context.Context, account, id string) (File, error) {
	file, err := s.records.Get(ctx, account, id)
	if err != nil {
		return File{}, err
	}
	if file.State != FileStateReady || file.Expired(s.now().UTC()) {
		return File{}, ErrFileNotFound
	}
	return file, nil
}

// Open returns the record and a reader over its bytes.
func (s *Service) Open(ctx context.Context, account, id string) (File, io.ReadCloser, error) {
	file, err := s.Get(ctx, account, id)
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

// List returns the readable files one account owns.
func (s *Service) List(ctx context.Context, account string, limit int) ([]File, error) {
	if limit <= 0 {
		limit = defaultListLimit
	}
	if strings.TrimSpace(account) == "" {
		return nil, nil
	}
	limit = min(limit, defaultListLimit)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	now := s.now().UTC()
	var readable []File
	cursor := ""
	for {
		page, err := s.records.Page(ctx, account, cursor)
		if err != nil {
			return nil, err
		}
		for _, file := range page.Records {
			if file.State == FileStateReady && file.outputIdentity == "" && !file.Expired(now) {
				readable = append(readable, file)
				if len(readable) == limit {
					return readable, nil
				}
			}
		}
		cursor = page.Next
		if cursor == "" {
			return readable, nil
		}
	}

}

// Delete removes a file this account owns.
//
// The record is marked first, then the bytes go, then the record goes. Marking
// first is what makes the delete resumable: a process that stops part way
// leaves a record in the deleting state, which reads as not found and which
// the next sweep finishes. A delete that removed the bytes without marking
// would leave a ready record over bytes that no longer exist.
func (s *Service) Delete(ctx context.Context, account, id string) error {
	file, err := s.records.Get(ctx, account, id)
	if err != nil {
		return err
	}
	return s.retire(ctx, file)
}

// retire marks a record deleting and then removes both halves.
func (s *Service) retire(ctx context.Context, file File) error {
	if file.State != FileStateDeleting {
		marked := file
		marked.State = FileStateDeleting
		if err := s.records.Replace(ctx, marked); err != nil {
			return err
		}
		file = marked
	}
	return s.remove(ctx, file)
}

// SweepResult counts what one sweep finished. An operator reads it to tell a
// quiet deployment from a sweep that never runs.
type SweepResult struct {
	// Abandoned counts pending records older than the grace window.
	Abandoned int
	// Expired counts ready records that passed their retention window.
	Expired int
	// Resumed counts records an interrupted delete left in the deleting state.
	Resumed int
}

// Total counts every record this sweep removed.
func (r SweepResult) Total() int { return r.Abandoned + r.Expired + r.Resumed }

// Sweep reclaims the storage that nothing reads any more.
//
// It handles three cases, and every one of them deletes the bytes before the
// record. An interrupted sweep therefore leaves a record naming bytes that may
// already be gone, which the next sweep finishes, rather than an object that
// no record names and that nothing can ever find again.
//
// One failing record does not stop the pass. A sweep that returned on the
// first error would let one unreachable object hold every later one hostage,
// and the caller runs on a ticker that would repeat the same failure forever.
func (s *Service) Sweep(ctx context.Context) (SweepResult, error) {
	if !s.sweepMu.TryLock() {
		return SweepResult{}, storage.ErrConflict
	}
	defer s.sweepMu.Unlock()
	var failures error
	if s.meter != nil {
		failures = s.meter.RecoverPending(ctx)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	now := s.now().UTC()
	abandonedBefore := now.Add(-s.pendingGrace)
	var result SweepResult
	for {
		if err := ctx.Err(); err != nil {
			return result, errors.Join(failures, err)
		}
		if !s.sweepLoaded {
			page, err := s.records.Page(ctx, "", s.sweepCursor)
			if err != nil && failures == nil {
				failures = err
			}
			if !page.Scanned {
				return result, failures
			}
			s.sweepPending, s.sweepNext, s.sweepLoaded = page.Records, page.Next, true
		}
		for len(s.sweepPending) > 0 {
			if err := ctx.Err(); err != nil {
				return result, errors.Join(failures, err)
			}
			file := s.sweepPending[0]
			counter := s.sweepReason(file, now, abandonedBefore, &result)
			if counter != nil {
				if err := s.retire(ctx, file); err != nil {
					if !errors.Is(err, ErrFileNotFound) && failures == nil {
						failures = fmt.Errorf("files: sweep %s: %w", file.ID, err)
					}
				} else {
					*counter++
				}
			}
			s.sweepPending[0] = File{}
			s.sweepPending = s.sweepPending[1:]
		}
		s.sweepCursor, s.sweepLoaded, s.sweepPending = s.sweepNext, false, nil
		if s.sweepCursor == "" {
			return result, failures
		}
	}

}

// sweepReason reports which counter one record belongs to, or nil when the
// sweep leaves it alone.
func (s *Service) sweepReason(file File, now, abandonedBefore time.Time, result *SweepResult) *int {
	switch file.State {
	case FileStateDeleting:
		// A delete that stopped part way. Nothing reads it, and only the sweep
		// finishes it.
		return &result.Resumed
	case FileStatePending:
		if file.outputIdentity != "" {
			if file.Expired(now) {
				return &result.Expired
			}
			return nil
		}
		if file.CreatedAt.After(abandonedBefore) {
			// A live upload looks exactly like an abandoned one. Only time
			// separates them, and the grace window is that time.
			return nil
		}
		return &result.Abandoned
	case FileStateReady:
		if !file.Expired(now) {
			return nil
		}
		return &result.Expired
	default:
		return nil
	}
}

// remove deletes the bytes and then the record.
//
// A missing object is not a failure here. Both shipped backends already treat
// a delete of an absent object as done, and the guard states the rule for any
// backend that does not: the record is what makes a file reachable, so a
// second pass over a half-finished delete has to get to the record.
func (s *Service) remove(ctx context.Context, file File) error {
	if err := s.blobs.Delete(ctx, file.blobKey); err != nil && !errors.Is(err, blob.ErrNotFound) {
		return fmt.Errorf("files: delete the bytes: %w", err)
	}
	if file.metered {
		if s.meter == nil {
			return fmt.Errorf("files: storage meter required for cleanup")
		}
		if err := s.meter.Release(ctx, file.Account, file.ID); err != nil {
			return err
		}
	}
	return s.records.Delete(ctx, file.Account, file.ID)
}

// discard keeps failed cleanup discoverable by the recovery sweep.
func (s *Service) discard(ctx context.Context, file File) {
	// Settlement can change the byte count before metadata publication.
	current, err := s.records.Get(ctx, file.Account, file.ID)
	if err == nil {
		_ = s.retire(ctx, current)
	}
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
