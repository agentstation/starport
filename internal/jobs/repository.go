package jobs

import (
	"context"
	"errors"
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
// with a record its caller does not own. Replace carries the whole record
// rather than a state word, because a state change is never the only change: a
// terminal move stamps a time, and a provider answer records an identifier
// with it.
//
// Scan is the one method that names no account. The sweep that reclaims expired
// asset storage is a deployment-wide pass, and no request path calls it.
type Repository interface {
	Create(context.Context, Job) error
	Get(context.Context, string, string) (Job, error)
	List(context.Context, string, int) ([]Job, error)
	Scan(context.Context, int) ([]Job, error)
	Replace(context.Context, Job) error
	Delete(context.Context, string, string) error
}
