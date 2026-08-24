package localauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

const (
	// tokenFileMode keeps the secret readable by its owner alone. It is the
	// whole security model of the file: the token is a claim about which
	// account you are on this machine, and a group-readable file would make it
	// a claim about which machine you are on.
	tokenFileMode = 0o600
	// tokenDirMode matches. A directory anyone can list is a directory anyone
	// can watch for the file appearing.
	tokenDirMode = 0o700

	// lockTimeout bounds the wait for the file lock. Two gateways starting at
	// once contend for milliseconds; anything approaching this bound is a stale
	// lock or a wedged process, and failing to start with a clear error beats
	// hanging with none.
	lockTimeout = 5 * time.Second
	// lockPoll is how often the wait retries. It is short because the expected
	// wait is a single file write.
	lockPoll = 20 * time.Millisecond
)

// ErrPathRequired reports a store built without a token file path.
var ErrPathRequired = errors.New("a local admin token path is required")

// ErrNotFound reports that no token has been minted on this machine.
var ErrNotFound = errors.New("no local admin token exists")

// Store is the token file and the rules for touching it safely.
//
// Every operation takes an exclusive lock on a sibling lock file, and every
// write lands through a temporary file and a rename. The lock keeps two
// processes from minting different tokens on the same first boot; the rename
// keeps a reader from ever seeing half a record, including a reader that is not
// holding the lock at all.
type Store struct {
	path string
}

// NewStore opens the token file at path. The path is not read here, because a
// missing file is the ordinary first-boot state and not a failure to open.
func NewStore(path string) (*Store, error) {
	if path == "" {
		return nil, ErrPathRequired
	}
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("%w: %q is not an absolute path", ErrPathRequired, path)
	}
	return &Store{path: filepath.Clean(path)}, nil
}

// Path is the token file this store owns. Commands print it so an operator can
// find, inspect, or delete the credential without guessing at a platform
// convention.
func (s *Store) Path() string { return s.path }

// Load reads the current token. It returns ErrNotFound when nothing has been
// minted, which is a state a caller can act on rather than an error.
func (s *Store) Load(ctx context.Context) (Token, error) {
	return s.locked(ctx, func() (Token, error) {
		token, found, err := s.read()
		if err != nil {
			return Token{}, err
		}
		if !found {
			return Token{}, ErrNotFound
		}
		return token, nil
	})
}

// LoadOrMint reads the current token, minting one if the machine has none. The
// second return value reports whether this call is the one that minted it, so a
// first boot can say so and every later boot can stay quiet.
func (s *Store) LoadOrMint(ctx context.Context, now time.Time) (Token, bool, error) {
	var minted bool
	token, err := s.locked(ctx, func() (Token, error) {
		existing, found, err := s.read()
		if err != nil {
			return Token{}, err
		}
		if found {
			return existing, nil
		}
		fresh, err := Mint(1, now)
		if err != nil {
			return Token{}, err
		}
		if err := s.write(fresh); err != nil {
			return Token{}, err
		}
		minted = true
		return fresh, nil
	})
	if err != nil {
		return Token{}, false, err
	}
	return token, minted, nil
}

// Rotate replaces the secret and records that an operator asked for it.
//
// It works on a machine with no token at all: rotating what does not exist
// still leaves the operator holding a secret that was never printed at boot,
// which is the whole point of the rotation.
func (s *Store) Rotate(ctx context.Context, now time.Time) (Token, error) {
	return s.locked(ctx, func() (Token, error) {
		generation := uint64(1)
		current, found, err := s.read()
		switch {
		case err != nil && !errors.Is(err, ErrCorruptRecord) && !errors.Is(err, ErrUnsupportedVersion):
			return Token{}, err
		case err == nil && found:
			generation = current.Generation + 1
		}
		rotated, err := Mint(generation, now)
		if err != nil {
			return Token{}, err
		}
		rotatedAt := now.UTC()
		rotated.RotatedAt = &rotatedAt
		if err := s.write(rotated); err != nil {
			return Token{}, err
		}
		return rotated, nil
	})
}

// locked runs operation while holding the exclusive file lock, and bounds the
// wait for it.
func (s *Store) locked(ctx context.Context, operation func() (Token, error)) (Token, error) {
	if err := os.MkdirAll(filepath.Dir(s.path), tokenDirMode); err != nil {
		return Token{}, fmt.Errorf("create the local admin token directory: %w", err)
	}
	lock := flock.New(s.path + ".lock")
	waitCtx, cancel := context.WithTimeout(ctx, lockTimeout)
	defer cancel()
	held, err := lock.TryLockContext(waitCtx, lockPoll)
	if err != nil {
		return Token{}, fmt.Errorf("lock the local admin token file: %w", err)
	}
	if !held {
		return Token{}, fmt.Errorf("lock the local admin token file %s: still held after %s", s.path, lockTimeout)
	}
	defer func() { _ = lock.Unlock() }()
	return operation()
}

// read returns the stored token. A missing file is reported as not found; a
// file that exists but does not decode is an error, because silently minting
// over an unreadable record would destroy the credential an operator is holding.
func (s *Store) read() (Token, bool, error) {
	raw, err := os.ReadFile(s.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return Token{}, false, nil
	case err != nil:
		return Token{}, false, fmt.Errorf("read the local admin token file: %w", err)
	}
	var token Token
	if err := json.Unmarshal(raw, &token); err != nil {
		return Token{}, false, fmt.Errorf("%w: %s: %w", ErrCorruptRecord, s.path, err)
	}
	if err := token.Validate(); err != nil {
		return Token{}, false, fmt.Errorf("%s: %w", s.path, err)
	}
	return token, true, nil
}

// write replaces the token file atomically at mode 0600.
func (s *Store) write(token Token) error {
	if err := token.Validate(); err != nil {
		return err
	}
	// #nosec G117 -- the secret is the record. This file is the credential, and
	// it lands at mode 0600 through the temporary file below.
	encoded, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the local admin token: %w", err)
	}
	encoded = append(encoded, '\n')

	directory := filepath.Dir(s.path)
	temporary, err := os.CreateTemp(directory, ".local-admin-token-*")
	if err != nil {
		return fmt.Errorf("create a temporary local admin token file: %w", err)
	}
	name := temporary.Name()
	// Every failure past this point leaves a temporary file behind unless it is
	// removed, and a stray file holding a valid secret is worse than the error
	// that produced it.
	defer func() { _ = os.Remove(name) }()

	// The mode is set before the content is written, so the secret is never on
	// disk at a mode another account could read, however briefly.
	if err := temporary.Chmod(tokenFileMode); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("restrict the temporary local admin token file: %w", err)
	}
	if _, err := temporary.Write(encoded); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write the temporary local admin token file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("flush the temporary local admin token file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close the temporary local admin token file: %w", err)
	}
	if err := os.Rename(name, s.path); err != nil {
		return fmt.Errorf("replace the local admin token file: %w", err)
	}
	// A file that already existed keeps its own mode through a rename, so the
	// mode is asserted on the destination rather than assumed from the source.
	if err := os.Chmod(s.path, tokenFileMode); err != nil {
		return fmt.Errorf("restrict the local admin token file: %w", err)
	}
	return nil
}
