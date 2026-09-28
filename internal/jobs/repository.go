package jobs

import (
	"context"
	"errors"
	"github.com/agentstation/starport/internal/storage"
)

var (
	// ErrRepositoryRequired reports an absent job record storage adapter.
	ErrRepositoryRequired = errors.New("jobs: record storage is required")
	// ErrJobNotFound reports a job this account cannot see.
	//
	// A job another account owns produces this error rather than a refusal. A
	// refusal would confirm that the identifier exists, and an identifier is
	// the only thing a caller needs to guess.
	ErrJobNotFound = errors.New("jobs: job not found")
	// ErrJobExists reports an identifier already in use.
	ErrJobExists = errors.New("jobs: job already exists")
	// ErrCorruptRecord reports durable job data this package cannot read.
	ErrCorruptRecord = errors.New("jobs: job record is invalid")
)

// Repository is the durable job record contract.
//
// Every method a request path calls takes the account, so a store cannot answer
// with a record its caller does not own. Replace carries the complete record.
// A state change also records related fields. A terminal change stamps a time.
// A provider response also records its identifier.
//
// Scan is the one method that names no account. The sweep that reclaims expired
// asset storage is a deployment-wide pass, and no request path calls it.
type Repository interface {
	CorrectionRepository
	Create(context.Context, Job) error
	// CreateClaimed atomically stores the job and its prepared claim attachment.
	CreateClaimed(context.Context, Job, storage.CompareAndSwapMutation) error
	Get(context.Context, string, string) (Job, error)
	List(context.Context, string, int) ([]Job, error)
	Scan(context.Context, int) ([]Job, error)
	RecoveryPage(context.Context, string) (RecoveryPage[Job], error)
	// Replace binds the change to the caller's observed record.
	// Concurrent changes refuse with storage.ErrConflict, even within one state.
	Replace(ctx context.Context, expected, next Job) error
	// Replacement prepares a validated change without publishing it.
	Replacement(ctx context.Context, expected, next Job) (storage.CompareAndSwapMutation, error)
	Delete(context.Context, string, string) error
}
