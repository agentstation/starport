package usage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"
)

// NDJSONContentType is the media type both sinks and the activity export
// speak: one JSON-encoded record per line.
const NDJSONContentType = "application/x-ndjson"

// Sink receives each finalized usage record and streams it out of the
// gateway. A sink batches internally and flushes on an interval and at
// Close. Receive must return quickly: it runs on the request completion
// path, before the asynchronous store write.
type Sink interface {
	Receive(record Record)
	Close(ctx context.Context) error
}

// Sink defaults. An operator tunes neither: the interval keeps an export
// near-live, and the bound keeps a stalled target from growing the process.
const (
	defaultSinkFlushInterval = 5 * time.Second
	defaultSinkMaxPending    = 10000
	httpSinkAttempts         = 3
	httpSinkRetryDelay       = 250 * time.Millisecond
	httpSinkRequestTimeout   = 10 * time.Second
)

// SinkOptions tune a sink. The zero value selects the defaults.
type SinkOptions struct {
	// FlushInterval is how often buffered records flush without waiting for
	// Close.
	FlushInterval time.Duration
	// MaxPending bounds the buffer. When a flush target stalls long enough to
	// fill it, the oldest records drop and OnDrop counts them.
	MaxPending int
	// OnDrop observes every dropped record count. Nil observes nothing.
	OnDrop func(count int)
}

func (o SinkOptions) withDefaults() SinkOptions {
	if o.FlushInterval <= 0 {
		o.FlushInterval = defaultSinkFlushInterval
	}
	if o.MaxPending <= 0 {
		o.MaxPending = defaultSinkMaxPending
	}
	return o
}

// batchingSink owns the buffer, the interval flusher, and the drop bound.
// The write function it wraps attempts one batch and reports failure; a
// failed batch drops, because a sink must never block or grow unbounded to
// protect an analytics copy of data the store already holds.
type batchingSink struct {
	options SinkOptions
	write   func([]Record) error

	mu      sync.Mutex
	pending []Record

	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

func newBatchingSink(options SinkOptions, write func([]Record) error) *batchingSink {
	s := &batchingSink{
		options: options.withDefaults(),
		write:   write,
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	go s.flushLoop()
	return s
}

// Receive buffers one record. When the buffer is full, the oldest record
// drops so the newest survives a stalled target.
func (s *batchingSink) Receive(record Record) {
	s.mu.Lock()
	if len(s.pending) >= s.options.MaxPending {
		s.pending = s.pending[1:]
		s.mu.Unlock()
		s.drop(1)
		s.mu.Lock()
	}
	s.pending = append(s.pending, record)
	s.mu.Unlock()
}

func (s *batchingSink) flushLoop() {
	defer close(s.done)
	ticker := time.NewTicker(s.options.FlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.flush()
		case <-s.stop:
			s.flush()
			return
		}
	}
}

func (s *batchingSink) flush() {
	s.mu.Lock()
	batch := s.pending
	s.pending = nil
	s.mu.Unlock()
	if len(batch) == 0 {
		return
	}
	if err := s.write(batch); err != nil {
		s.drop(len(batch))
	}
}

func (s *batchingSink) drop(count int) {
	if s.options.OnDrop != nil && count > 0 {
		s.options.OnDrop(count)
	}
}

// Close flushes the buffer and stops the flusher. The context bounds the
// wait for the final flush.
func (s *batchingSink) Close(ctx context.Context) error {
	s.stopOnce.Do(func() { close(s.stop) })
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// encodeNDJSON renders a batch as NDJSON: one JSON object per record, one
// record per line.
func encodeNDJSON(batch []Record) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	for _, record := range batch {
		if err := encoder.Encode(record); err != nil {
			return nil, err
		}
	}
	return buffer.Bytes(), nil
}

// NewFileSink appends each record as one NDJSON line to path. The file
// opens once and holds its handle until Close.
func NewFileSink(path string, options SinkOptions) (Sink, error) {
	// #nosec G304 -- The path is the operator's configured export target,
	// not caller input.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open usage export file: %w", err)
	}
	batching := newBatchingSink(options, func(batch []Record) error {
		lines, err := encodeNDJSON(batch)
		if err != nil {
			return err
		}
		_, err = file.Write(lines)
		return err
	})
	return &fileSink{batchingSink: batching, file: file}, nil
}

type fileSink struct {
	*batchingSink
	file *os.File
}

func (s *fileSink) Close(ctx context.Context) error {
	flushErr := s.batchingSink.Close(ctx)
	closeErr := s.file.Close()
	if flushErr != nil {
		return flushErr
	}
	return closeErr
}

// NewHTTPSink posts each batch as an NDJSON body to url. A failed post
// retries a bounded number of times; a batch that never lands drops and
// OnDrop counts it.
func NewHTTPSink(url string, options SinkOptions) Sink {
	client := &http.Client{Timeout: httpSinkRequestTimeout}
	return newBatchingSink(options, func(batch []Record) error {
		body, err := encodeNDJSON(batch)
		if err != nil {
			return err
		}
		var lastErr error
		for attempt := 0; attempt < httpSinkAttempts; attempt++ {
			if attempt > 0 {
				time.Sleep(httpSinkRetryDelay << (attempt - 1))
			}
			lastErr = postNDJSON(client, url, body)
			if lastErr == nil {
				return nil
			}
		}
		return lastErr
	})
}

func postNDJSON(client *http.Client, url string, body []byte) error {
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", NDJSONContentType)
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	_ = response.Body.Close()
	if response.StatusCode >= 300 {
		return fmt.Errorf("usage export target answered %d", response.StatusCode)
	}
	return nil
}
