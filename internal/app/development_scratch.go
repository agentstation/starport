package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"sync"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/gofrs/flock"
	"github.com/google/uuid"
)

const (
	developmentScratchPrefix        = "starport-dev-"
	developmentScratchOwner         = ".starport-development"
	developmentScratchRecordName    = "session.json"
	developmentScratchLock          = "session.lock"
	developmentScratchRecordLimit   = 4096
	developmentScratchRecordVersion = 1
)

var errDevelopmentScratchChanged = errors.New("development scratch ownership changed")

type developmentScratchRecord struct {
	Version     int               `json:"version"`
	Session     string            `json:"session"`
	Name        string            `json:"name"`
	Root        string            `json:"root"`
	Directories map[string]string `json:"directories"`
	Metadata    map[string]string `json:"metadata"`
}

// developmentScratch owns a temporary directory and its exclusive session lock.
// Its record binds cleanup to native identities instead of a reusable path.
type developmentScratch struct {
	path         string
	directory    *productfiles.Directory
	metadata     *productfiles.Directory
	record       developmentScratchRecord
	encoded      []byte
	published    bool
	lock         *flock.Flock
	lockIdentity os.FileInfo
	closeOnce    sync.Once
	closeErr     error
}

func newDevelopmentScratch(ctx context.Context, temporary string) (_ *developmentScratch, resultErr error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := os.MkdirTemp(temporary, developmentScratchPrefix)
	if err != nil {
		return nil, err
	}
	directory, err := productfiles.ExistingDirectory(path)
	if err != nil {
		return nil, errors.Join(err, os.Remove(path))
	}
	session := &developmentScratch{path: path, directory: directory,
		record: developmentScratchRecord{Version: developmentScratchRecordVersion,
			Session: uuid.NewString(), Name: filepath.Base(path), Directories: make(map[string]string)}}
	// An incomplete owner record cannot authorize recursive cleanup.
	// Failed initialization closes handles and preserves its private directory.
	defer func() {
		if resultErr != nil && session.lock != nil {
			resultErr = errors.Join(resultErr, session.lock.Close())
		}
	}()
	session.record.Root, err = directory.Identity()
	if err != nil {
		return nil, err
	}
	for _, name := range developmentScratchDirectories() {
		child, err := directory.CreateChild(name)
		if err != nil {
			return nil, err
		}
		identity, err := child.Identity()
		if err != nil {
			return nil, err
		}
		session.record.Directories[name] = identity
		if name == developmentScratchOwner {
			session.metadata = child
		}
	}
	if err := session.metadata.CompareAndPublish(ctx, developmentScratchLock, nil, []byte{}); err != nil {
		return nil, err
	}
	session.record.Metadata, err = session.metadataIdentities()
	if err != nil {
		return nil, err
	}
	locked, err := session.acquire()
	if err != nil {
		return nil, err
	}
	if !locked {
		return nil, fmt.Errorf("%w: new session is already locked", errDevelopmentScratchChanged)
	}
	session.encoded, err = json.Marshal(session.record)
	if err != nil {
		return nil, err
	}
	return session, nil
}

// publish permits crash recovery after the host finishes application construction.
func (s *developmentScratch) publish(ctx context.Context) error {
	if err := s.check(); err != nil {
		return err
	}
	// A publication error can report visible bytes whose durability is unknown.
	s.published = true
	return s.metadata.CompareAndPublish(ctx, developmentScratchRecordName, nil, s.encoded)
}

func developmentScratchDirectories() []string {
	return []string{developmentScratchOwner, "files", "catalog", "cache"}
}

func (s *developmentScratch) acquire() (bool, error) {
	if _, err := s.metadata.ReadFile(developmentScratchLock, 0); err != nil {
		return false, err
	}
	identities, err := s.metadataIdentities()
	if err != nil {
		return false, err
	}
	if identities[developmentScratchLock] != s.record.Metadata[developmentScratchLock] || s.record.Metadata[developmentScratchLock] == "" {
		return false, errDevelopmentScratchChanged
	}
	root, err := s.metadata.Open()
	if err != nil {
		return false, err
	}
	info, statErr := root.Lstat(developmentScratchLock)
	if err := errors.Join(statErr, root.Close()); err != nil {
		return false, err
	}
	s.lockIdentity = info
	s.lock = flock.New(filepath.Join(s.path, developmentScratchOwner, developmentScratchLock), flock.SetFlag(os.O_RDWR))
	locked, err := s.lock.TryLock()
	if err != nil || !locked {
		return false, errors.Join(err, s.lock.Close())
	}
	if err := s.checkLock(); err != nil {
		return false, errors.Join(err, s.lock.Close())
	}
	return true, nil
}

func (s *developmentScratch) checkLock() error {
	root, err := s.metadata.Open()
	if err != nil {
		return err
	}
	current, statErr := root.Lstat(developmentScratchLock)
	if err := errors.Join(statErr, root.Close()); err != nil {
		return err
	}
	held, err := s.lock.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(s.lockIdentity, current) || !os.SameFile(s.lockIdentity, held) {
		return errDevelopmentScratchChanged
	}
	_, err = s.metadata.ReadFile(developmentScratchLock, 0)
	return err
}

func (s *developmentScratch) close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		if err := s.remove(); err != nil {
			s.closeErr = fmt.Errorf("preserve development scratch %s: %w", s.path, err)
		}
	})
	return s.closeErr
}

func (s *developmentScratch) check() error {
	identity, err := s.directory.Identity()
	if err != nil {
		return err
	}
	if identity != s.record.Root {
		return errDevelopmentScratchChanged
	}
	if err := s.checkLock(); err != nil {
		return err
	}
	encoded, err := s.metadata.ReadFile(developmentScratchRecordName, developmentScratchRecordLimit)
	if s.published {
		if err != nil {
			return err
		}
		if !bytes.Equal(encoded, s.encoded) {
			return errDevelopmentScratchChanged
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.Join(errDevelopmentScratchChanged, err)
	}
	identities, err := s.metadataIdentities()
	if err != nil {
		return err
	}
	if !maps.Equal(identities, s.record.Metadata) {
		return errDevelopmentScratchChanged
	}
	root, err := s.directory.Open()
	if err != nil {
		return err
	}
	file, err := root.Open(".")
	if err != nil {
		return errors.Join(err, root.Close())
	}
	entries, readErr := file.ReadDir(len(s.record.Directories) + 1)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return errors.Join(readErr, file.Close(), root.Close())
	}
	if err := errors.Join(file.Close(), root.Close()); err != nil {
		return err
	}
	for _, entry := range entries {
		expected, known := s.record.Directories[entry.Name()]
		if !known || !entry.IsDir() {
			return errDevelopmentScratchChanged
		}
		child, err := s.directory.ExistingChild(entry.Name())
		if err != nil {
			return err
		}
		identity, err := child.Identity()
		if err != nil {
			return err
		}
		if identity != expected {
			return errDevelopmentScratchChanged
		}
	}
	return nil
}

func (s *developmentScratch) remove() (resultErr error) {
	defer func() { resultErr = errors.Join(resultErr, s.lock.Close()) }()
	if err := s.check(); err != nil {
		return err
	}
	root, err := s.directory.Open()
	if err != nil {
		return err
	}
	rootClosed := false
	defer func() {
		if !rootClosed {
			resultErr = errors.Join(resultErr, root.Close())
		}
	}()
	for _, name := range []string{"files", "catalog", "cache"} {
		if err := s.check(); err != nil {
			return err
		}
		if err := root.RemoveAll(name); err != nil {
			return err
		}
	}
	if err := s.check(); err != nil {
		return err
	}
	// Windows requires the session lock to close before its directory can disappear.
	// A concurrent collector can make this final cleanup retryable.
	if err := s.lock.Close(); err != nil {
		return err
	}
	if err := root.RemoveAll(developmentScratchOwner); err != nil {
		return err
	}
	err = root.Close()
	rootClosed = true
	if err != nil {
		return err
	}
	parent, err := os.OpenRoot(filepath.Dir(s.path))
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, parent.Close()) }()
	if _, err := s.directory.Identity(); err != nil {
		return err
	}
	return parent.Remove(filepath.Base(s.path))
}
