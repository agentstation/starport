// Package files owns the record of a stored file: who owns it, what it is
// called, what it is for, how large it is, and when it stops being readable.
//
// The bytes live in internal/blob under an opaque key. That key lives in this
// record and nowhere else, so the only way to reach a file is through a record
// this package agrees to hand out.
package files

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxFilenameLength bounds the name a caller may attach to an upload. The name
// is a label a person reads back, not a path anything opens.
const MaxFilenameLength = 255

var (
	// ErrInvalidFile reports a record that cannot be stored as given.
	ErrInvalidFile = errors.New("files: invalid file")
	// ErrInvalidPurpose reports a purpose this gateway does not serve.
	ErrInvalidPurpose = errors.New("files: unsupported purpose")
)

// Purpose states what a caller intends to do with a stored file.
//
// The set is deliberately small. OpenAI names several more, and each of the
// others belongs to a product Starport does not run: an assistant, a batch, or
// a fine-tune. A gateway that accepted them would take an upload it can never
// use and bill storage for it.
type Purpose string

const (
	// PurposeUserData is a file a model reads as part of a request.
	PurposeUserData Purpose = "user_data"
	// PurposeVision is an image a model reads as part of a request.
	PurposeVision Purpose = "vision"
)

// Purposes lists every purpose this gateway accepts.
func Purposes() []Purpose { return []Purpose{PurposeUserData, PurposeVision} }

// Valid reports whether this gateway serves the purpose.
func (p Purpose) Valid() bool {
	for _, known := range Purposes() {
		if p == known {
			return true
		}
	}
	return false
}

// FileState is where one record sits between an upload and a deletion.
//
// The states exist because a file is two writes, a record and its bytes, and a
// process can stop between them. A state tells a later sweep which of the two
// writes it has to finish or undo.
type FileState string

const (
	// FileStatePending marks a record written ahead of its bytes. It is not
	// readable, and a sweep deletes it and the bytes it names.
	FileStatePending FileState = "pending"
	// FileStateReady marks a record whose bytes landed.
	FileStateReady FileState = "ready"
	// FileStateDeleting marks a record on its way out. FIL5 gives it meaning.
	FileStateDeleting FileState = "deleting"
)

// File is one stored file.
//
// Every field a caller may read is exported. The blob key is not one of them.
// It stays unexported, so no encoder, no template, and no response body can
// carry it out of this package by accident.
type File struct {
	ID        string
	Tenant    string
	Filename  string
	Purpose   Purpose
	Bytes     int64
	State     FileState
	CreatedAt time.Time
	ExpiresAt time.Time

	blobKey string
}

// Validate reports whether the record can be stored.
func (f File) Validate() error {
	switch {
	case strings.TrimSpace(f.ID) == "":
		return fmt.Errorf("%w: it has no identifier", ErrInvalidFile)
	case strings.TrimSpace(f.Tenant) == "":
		return fmt.Errorf("%w: it names no tenant", ErrInvalidFile)
	case f.blobKey == "":
		return fmt.Errorf("%w: it names no stored bytes", ErrInvalidFile)
	case f.Bytes < 0:
		return fmt.Errorf("%w: it reports a negative size", ErrInvalidFile)
	case f.CreatedAt.IsZero():
		return fmt.Errorf("%w: it has no creation time", ErrInvalidFile)
	}
	if !f.Purpose.Valid() {
		return fmt.Errorf("%w: %q", ErrInvalidPurpose, f.Purpose)
	}
	switch f.State {
	case FileStatePending, FileStateReady, FileStateDeleting:
	default:
		return fmt.Errorf("%w: unknown state %q", ErrInvalidFile, f.State)
	}
	return validateFilename(f.Filename)
}

// validateFilename bounds the label rather than interpreting it. The name never
// reaches a filesystem, so the rules here protect a reader and a log line, not
// a path.
func validateFilename(name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return fmt.Errorf("%w: it has no filename", ErrInvalidFile)
	case utf8.RuneCountInString(name) > MaxFilenameLength:
		return fmt.Errorf("%w: the filename is longer than %d characters", ErrInvalidFile, MaxFilenameLength)
	case !utf8.ValidString(name):
		return fmt.Errorf("%w: the filename is not valid text", ErrInvalidFile)
	case strings.ContainsAny(name, "\x00\n\r"):
		return fmt.Errorf("%w: the filename contains a control character", ErrInvalidFile)
	}
	return nil
}

// Expired reports whether the file stopped being readable at the given time. A
// zero expiry never expires.
func (f File) Expired(now time.Time) bool {
	return !f.ExpiresAt.IsZero() && !now.Before(f.ExpiresAt)
}
