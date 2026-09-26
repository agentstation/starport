package setup

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"slices"

	"github.com/agentstation/starmap/pkg/productfiles"
)

const (
	setupFileLimit = 64 << 20
	setupTreeLimit = 128 << 20
	setupFileCount = 64
)

// setupReceipt binds a closed initial database to its directory and file contents.
// Setup accepts regular direct children only. Badger owns the database format.
type setupReceipt struct {
	Directory string                     `json:"directory"`
	Files     map[string]setupFileRecord `json:"files"`
}

type setupFileRecord struct {
	Identity string `json:"identity"`
	Size     int64  `json:"size"`
	Mode     uint32 `json:"mode"`
	Modified int64  `json:"modified"`
	Digest   string `json:"digest"`
}

func captureSetupReceipt(directory *productfiles.Directory) (_ setupReceipt, resultErr error) {
	identity, err := directory.Identity()
	if err != nil {
		return setupReceipt{}, err
	}
	root, err := directory.Open()
	if err != nil {
		return setupReceipt{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	before, err := root.Stat(".")
	if err != nil {
		return setupReceipt{}, err
	}
	handle, err := root.Open(".")
	if err != nil {
		return setupReceipt{}, err
	}
	entries, readErr := handle.ReadDir(setupFileCount + 1)
	closeErr := handle.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return setupReceipt{}, fmt.Errorf("read setup database inventory: %w", readErr)
	}
	if closeErr != nil {
		return setupReceipt{}, closeErr
	}
	if len(entries) == 0 || len(entries) > setupFileCount {
		return setupReceipt{}, fmt.Errorf("%w: setup database inventory is empty or exceeds %d entries", ErrPartialState, setupFileCount)
	}
	receipt := setupReceipt{Directory: identity, Files: make(map[string]setupFileRecord, len(entries))}
	var total int64
	for _, entry := range entries {
		record, err := setupRecord(root, entry.Name())
		if err != nil {
			return setupReceipt{}, err
		}
		total += record.Size
		if total > setupTreeLimit {
			return setupReceipt{}, fmt.Errorf("%w: setup database exceeds the recovery byte limit", ErrPartialState)
		}
		receipt.Files[entry.Name()] = record
	}
	current, err := directory.Identity()
	if err != nil || current != identity {
		return setupReceipt{}, errors.Join(ErrPartialState, err)
	}
	after, err := root.Stat(".")
	if err != nil || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
		return setupReceipt{}, errors.Join(ErrPartialState, err)
	}
	return receipt, nil
}

func setupRecord(root *os.Root, name string) (_ setupFileRecord, resultErr error) {
	before, err := root.Lstat(name)
	if err != nil {
		return setupFileRecord{}, err
	}
	if !before.Mode().IsRegular() || before.Size() > setupFileLimit {
		return setupFileRecord{}, fmt.Errorf("%w: unexpected setup database entry %q", ErrPartialState, name)
	}
	// The private engine directory controls access. Badger owns its file modes.
	file, err := root.Open(name)
	if err != nil {
		return setupFileRecord{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	opened, err := file.Stat()
	if err != nil {
		return setupFileRecord{}, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return setupFileRecord{}, fmt.Errorf("%w: setup database entry %q changed before reading", ErrPartialState, name)
	}
	identity, err := productfiles.FileIdentity(file)
	if err != nil {
		return setupFileRecord{}, err
	}
	digest := sha256.New()
	size, err := io.Copy(digest, io.LimitReader(file, setupFileLimit+1))
	if err != nil {
		return setupFileRecord{}, err
	}
	if size > setupFileLimit {
		return setupFileRecord{}, fmt.Errorf("%w: setup database entry %q exceeds the byte limit", ErrPartialState, name)
	}
	after, err := root.Lstat(name)
	if err != nil {
		return setupFileRecord{}, err
	}
	if !os.SameFile(before, opened) || !os.SameFile(before, after) || before.Size() != after.Size() || size != after.Size() ||
		before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
		return setupFileRecord{}, fmt.Errorf("%w: setup database entry %q changed", ErrPartialState, name)
	}
	return setupFileRecord{Identity: identity, Size: after.Size(), Mode: uint32(after.Mode()),
		Modified: after.ModTime().UnixNano(), Digest: hex.EncodeToString(digest.Sum(nil))}, nil
}

func verifySetupReceipt(directory *productfiles.Directory, expected setupReceipt) error {
	actual, err := captureSetupReceipt(directory)
	if err != nil {
		return errors.Join(ErrPartialState, err)
	}
	if !reflect.DeepEqual(actual, expected) {
		return fmt.Errorf("%w: setup database identity, inventory, or contents changed", ErrPartialState)
	}
	return nil
}

// remainingSetupFiles verifies every surviving entry before cleanup resumes.
// A removal transaction permits missing receipt entries but preserves unknown files.
func remainingSetupFiles(root *os.Root, receipt setupReceipt) ([]string, error) {
	file, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	entries, readErr := file.ReadDir(setupFileCount + 1)
	closeErr := file.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return nil, errors.Join(readErr, closeErr)
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(entries) > setupFileCount {
		return nil, ErrPartialState
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		expected, ok := receipt.Files[entry.Name()]
		if !ok {
			return nil, fmt.Errorf("%w: setup cleanup preserves an unknown database entry", ErrPartialState)
		}
		actual, err := setupRecord(root, entry.Name())
		if err != nil || actual != expected {
			return nil, errors.Join(ErrPartialState, err)
		}
		names = append(names, entry.Name())
	}
	slices.Sort(names)
	return names, nil
}
