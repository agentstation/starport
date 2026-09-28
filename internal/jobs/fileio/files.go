// Package fileio connects durable batch work to account-owned files.
package fileio

import (
	"context"
	"errors"
	"io"
	"strconv"

	"github.com/agentstation/starport/internal/files"
	"github.com/agentstation/starport/internal/jobs"
)

// Store binds batch files to one account and storage bound.
type Store struct {
	Files                *files.Service
	Account, InputFileID string
	StoredBytesBound     int64
}

// OpenInput reads the retained batch input.
func (s Store) OpenInput(ctx context.Context) (io.ReadCloser, error) {
	_, reader, err := s.Files.Open(ctx, s.Account, s.InputFileID)
	return reader, err
}

// PrepareResult reserves one line output identity and its original expiry.
func (s Store) PrepareResult(ctx context.Context, line jobs.BatchLine) (jobs.ResultFile, error) {
	if line.Account != s.Account {
		return jobs.ResultFile{}, jobs.ErrInvalidBatch
	}
	file, err := s.Files.PrepareOutput(ctx, s.Account, "batch-line:"+line.RequestID, line.BatchID+"_line_"+strconv.Itoa(line.Number)+".jsonl", s.StoredBytesBound)
	return jobs.ResultFile{ID: file.ID, ExpiresAt: file.ExpiresAt}, err
}

// StoreResult publishes bytes under the retained line output identity.
func (s Store) StoreResult(ctx context.Context, id string, size int64, digest string, reader io.Reader) error {
	_, err := s.Files.CommitOutput(ctx, s.Account, id, size, digest, reader)
	return err
}

// OpenResult reads a retained line output.
func (s Store) OpenResult(ctx context.Context, id string) (io.ReadCloser, error) {
	_, reader, err := s.Files.Open(ctx, s.Account, id)
	return reader, err
}

// RecoverResult repairs metadata from retained output evidence.
func (s Store) RecoverResult(ctx context.Context, id string) error {
	_, err := s.Files.RecoverOutput(ctx, s.Account, id)
	return err
}

// ConfirmAggregate exposes the completed aggregate file.
func (s Store) ConfirmAggregate(ctx context.Context, id string) error {
	return s.Files.ExposeOutput(ctx, s.Account, id)
}

// DeleteResult retires a line checkpoint and permits exact retries.
func (s Store) DeleteResult(ctx context.Context, id string) error {
	err := s.Files.Delete(ctx, s.Account, id)
	if errors.Is(err, files.ErrFileNotFound) {
		return nil
	}
	return err
}

// StoreAggregate publishes an aggregate under its stable batch identity.
func (s Store) StoreAggregate(ctx context.Context, batch jobs.Batch, failed bool, size int64, digest string, reader io.Reader) (string, error) {
	if batch.Account != s.Account {
		return "", jobs.ErrInvalidBatch
	}
	kind := "output"
	if failed {
		kind = "errors"
	}
	identity := "batch-aggregate:" + batch.ID + ":" + kind
	file, err := s.Files.PrepareOutput(ctx, s.Account, identity, batch.ID+"_"+kind+".jsonl", batch.StoredBytesBound)
	if err != nil {
		return "", err
	}
	_, err = s.Files.CommitOutput(ctx, s.Account, file.ID, size, digest, reader)
	if err == nil {
		_, err = io.Copy(io.Discard, reader)
	}
	return file.ID, err
}
