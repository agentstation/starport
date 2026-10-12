package endpoint

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

// ErrRecordHeld reports that another gateway holds the state root.
var ErrRecordHeld = errors.New("another gateway holds this state root")

const (
	// recordLockAttempts bounds the retries of a lock attempt. A sibling
	// command that probes the lock holds it for a moment, so one refusal is
	// not proof of a running gateway.
	recordLockAttempts = 5
	recordLockRetry    = 20 * time.Millisecond
)

// RecordLock is the gateway seat of one state root. The gateway that serves
// the root holds it from before storage opens until after it removes its
// record, so one gateway serves one state root whatever storage it selects.
// The operating system releases the lock when the process exits, so a record
// whose lock is free is stale, whatever its process ID says.
type RecordLock struct {
	file *flock.Flock
}

// LockPath returns the lock file beside the record at path.
func LockPath(path string) string {
	return path + ".lock"
}

// LockRecord takes the seat beside the record at path. A seat that another
// gateway holds returns ErrRecordHeld.
func LockRecord(path string) (*RecordLock, error) {
	lockPath := LockPath(path)
	if err := os.MkdirAll(filepath.Dir(lockPath), recordDirectoryMode); err != nil {
		return nil, fmt.Errorf("create the endpoint record directory: %w", err)
	}
	file := flock.New(lockPath, flock.SetFlag(os.O_CREATE|os.O_RDWR), flock.SetPermissions(recordFileMode))
	for attempt := range recordLockAttempts {
		locked, err := file.TryLock()
		if err != nil {
			return nil, errors.Join(fmt.Errorf("lock the endpoint record: %w", err), file.Close())
		}
		if locked {
			return &RecordLock{file: file}, nil
		}
		if attempt+1 < recordLockAttempts {
			time.Sleep(recordLockRetry)
		}
	}
	return nil, errors.Join(ErrRecordHeld, file.Close())
}

// Close releases the seat.
func (l *RecordLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

// held reports whether a process holds the lock beside the record at path. A
// probe that takes a free lock releases it at once and never creates the file.
func held(path string) bool {
	lockPath := LockPath(path)
	if _, err := os.Stat(lockPath); err != nil {
		return false
	}
	file := flock.New(lockPath, flock.SetFlag(os.O_RDONLY))
	defer func() { _ = file.Close() }()
	locked, err := file.TryLock()
	if err != nil {
		return false
	}
	return !locked
}
