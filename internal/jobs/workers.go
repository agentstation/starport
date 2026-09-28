package jobs

import (
	"context"
	"errors"
	"sync"
	"time"
)

const (
	// DefaultExecutionTimeout bounds one native video attempt, including its response.
	DefaultExecutionTimeout = 10 * time.Minute
	// DefaultMaxWorkers bounds in-flight video submissions on one replica.
	DefaultMaxWorkers = 2
)

var (
	// ErrWorkersBusy refuses work before provider dispatch when all workers hold requests.
	ErrWorkersBusy = errors.New("jobs: video workers are busy; retry later")
	// ErrServiceClosed refuses work after shutdown starts.
	ErrServiceClosed = errors.New("jobs: service is closed")
)

type workerState struct {
	mu     sync.Mutex
	closed bool
	active map[*submissionWorker]struct{}
	wg     sync.WaitGroup
}
type submissionWorker struct{ cancel context.CancelFunc }
type submissionOutcome struct {
	job Job
	err error
}

// WithWorkers sets a replica's concurrent submission and elapsed-time bounds.
func WithWorkers(limit int, timeout time.Duration) ServiceOption {
	return func(s *Service) {
		if limit > 0 {
			s.maxWorkers = limit
		}
		if timeout > 0 {
			s.executionTimeout = timeout
		}
	}
}

// SubmitBackground returns after a native dispatch has durable ownership.
// Ordinary asynchronous providers still return their acceptance. A disconnected
// caller does not cancel native work after its durable dispatch. A restart never
// replays an uncertain submission.
func (s *Service) SubmitBackground(ctx context.Context, open OpenRunner, submission Submission) (Job, error) {
	executionCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.executionTimeout)
	worker := &submissionWorker{cancel: cancel}
	if err := s.startWorker(worker); err != nil {
		cancel()
		return Job{}, err
	}
	stopCaller := context.AfterFunc(ctx, cancel)
	runner, recorder, err := s.beginSubmission(executionCtx, open, submission)
	if err != nil {
		stopCaller()
		s.finishWorker(worker)
		return Job{}, err
	}
	ready := make(chan Job, 1)
	done := make(chan submissionOutcome, 1)
	recorder.onNativeDispatch = func(job Job) {
		stopCaller()
		job.Valuation = copyValuation(job.Valuation)
		job.Measurement = copyMeasurement(job.Measurement)
		ready <- job
	}
	go func() {
		defer s.finishWorker(worker)
		defer stopCaller()
		job, err := s.executeSubmission(executionCtx, runner, recorder)
		done <- submissionOutcome{job, err}
	}()
	select {
	case job := <-ready:
		return job, nil
	case result := <-done:
		return result.job, result.err
	case <-ctx.Done():
		return Job{}, ctx.Err()
	}
}

func (s *Service) startWorker(worker *submissionWorker) error {
	s.workers.mu.Lock()
	defer s.workers.mu.Unlock()
	if s.workers.closed {
		return ErrServiceClosed
	}
	if len(s.workers.active) >= s.maxWorkers {
		return ErrWorkersBusy
	}
	if s.workers.active == nil {
		s.workers.active = make(map[*submissionWorker]struct{})
	}
	s.workers.active[worker] = struct{}{}
	s.workers.wg.Add(1)
	return nil
}
func (s *Service) finishWorker(worker *submissionWorker) {
	worker.cancel()
	s.workers.mu.Lock()
	delete(s.workers.active, worker)
	s.workers.mu.Unlock()
	s.workers.wg.Done()
}

// Close refuses new workers, cancels active calls, and waits for durable cleanup.
func (s *Service) Close(ctx context.Context) error {
	s.workers.mu.Lock()
	s.workers.closed = true
	for worker := range s.workers.active {
		worker.cancel()
	}
	s.workers.mu.Unlock()
	done := make(chan struct{})
	go func() { s.workers.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
