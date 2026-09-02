package events

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// Dispatcher defaults. An operator tunes none of them: the attempt count
// and backoff keep a flapping receiver from stalling the queue, and the
// pending bound keeps a dead receiver from growing the process.
const (
	defaultMaxPending  = 1000
	deliveryAttempts   = 3
	deliveryRetryDelay = 250 * time.Millisecond
	deliveryTimeout    = 10 * time.Second
)

// Options tune a dispatcher. The zero value selects the defaults.
type Options struct {
	// MaxPending bounds the undelivered queue. An emit against a full
	// queue drops the event and OnDeadLetter counts it.
	MaxPending int
	// OnDeadLetter observes every event that will never deliver: dropped
	// at a full queue, or spent through every attempt at an endpoint.
	// Nil observes nothing.
	OnDeadLetter func(count int)
	// Now replaces the clock. A test states its own times.
	Now func() time.Time
}

// Dispatcher delivers each emitted event to every configured endpoint,
// signed, from its own goroutine. An emit never blocks the caller: the
// emit sites sit on the request path and the poller's pass, and a webhook
// receiver must not be able to slow either.
//
// A nil *Dispatcher emits nothing, so every emit site holds one
// unconditionally. That is the unconfigured deployment: no endpoint, no
// outbound push.
type Dispatcher struct {
	endpoints []string
	secret    []byte
	options   Options
	client    *http.Client

	queue chan Event
	stop  chan struct{}
	done  chan struct{}

	// deadLetters retains what OnDeadLetter only observes, so the admin
	// surface can state the count without a scrape.
	deadLetters atomic.Int64
}

// Stats is the delivery state the admin surface reports. Endpoints carries
// each receiver with its credentials and query removed, so the summary
// never repeats a secret a receiver URL embeds.
type Stats struct {
	// Endpoints lists the configured receivers, redacted.
	Endpoints []string
	// QueueDepth counts the events waiting for delivery.
	QueueDepth int
	// QueueCapacity is the pending bound the queue drops at.
	QueueCapacity int
	// DeadLetters counts every event that will never deliver since start.
	DeadLetters int64
}

// Types lists every event name the gateway emits, in the order the
// operator guide documents them.
func Types() []string {
	return []string{
		TypeBudgetExhausted,
		TypeJobCompleted,
		TypeJobFailed,
		TypeJobCancelled,
		TypeProviderHealthChanged,
		TypeKeyCreated,
		TypeKeyDeleted,
	}
}

// RedactEndpoint strips the userinfo and the query from a receiver URL. A
// receiver often authenticates through a token in either place, and the
// admin surface shows where deliveries go, not how they authenticate. A
// value that does not parse as a URL redacts to its scheme and host
// portion only.
func RedactEndpoint(endpoint string) string {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Host == "" {
		before, _, _ := strings.Cut(endpoint, "?")
		return before
	}
	redacted := &url.URL{Scheme: parsed.Scheme, Host: parsed.Host, Path: parsed.Path}
	return redacted.String()
}

// Stats reports the delivery state. A nil dispatcher is the unconfigured
// deployment and reports no endpoints and nothing queued.
func (d *Dispatcher) Stats() Stats {
	if d == nil {
		return Stats{}
	}
	endpoints := make([]string, 0, len(d.endpoints))
	for _, endpoint := range d.endpoints {
		endpoints = append(endpoints, RedactEndpoint(endpoint))
	}
	return Stats{
		Endpoints:     endpoints,
		QueueDepth:    len(d.queue),
		QueueCapacity: cap(d.queue),
		DeadLetters:   d.deadLetters.Load(),
	}
}

// NewDispatcher builds a dispatcher for the configured endpoints. It
// returns nil when endpoints is empty: webhooks stay off until
// configuration names a receiver.
func NewDispatcher(endpoints []string, secret string, options Options) *Dispatcher {
	if len(endpoints) == 0 {
		return nil
	}
	if options.MaxPending <= 0 {
		options.MaxPending = defaultMaxPending
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	d := &Dispatcher{
		endpoints: endpoints,
		secret:    []byte(secret),
		options:   options,
		client:    &http.Client{Timeout: deliveryTimeout},
		queue:     make(chan Event, options.MaxPending),
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
	go d.run()
	return d
}

// Emit queues one event for delivery and returns immediately. A full
// queue or a closed dispatcher drops the event and counts a dead letter.
func (d *Dispatcher) Emit(eventType string, data map[string]string) {
	if d == nil {
		return
	}
	event := Event{
		ID:   "evt-" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		Type: eventType,
		Time: d.options.Now().UTC().Format(time.RFC3339),
		Data: data,
	}
	select {
	case <-d.stop:
		d.deadLetter(1)
		return
	default:
	}
	select {
	case d.queue <- event:
	default:
		d.deadLetter(1)
	}
}

// Close delivers what is already queued and stops the worker. The context
// bounds the wait.
func (d *Dispatcher) Close(ctx context.Context) error {
	if d == nil {
		return nil
	}
	select {
	case <-d.stop:
	default:
		close(d.stop)
	}
	select {
	case <-d.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *Dispatcher) run() {
	defer close(d.done)
	for {
		select {
		case event := <-d.queue:
			d.deliver(event)
		case <-d.stop:
			for {
				select {
				case event := <-d.queue:
					d.deliver(event)
				default:
					return
				}
			}
		}
	}
}

// deliver posts one event to every endpoint. Each endpoint gets its own
// bounded attempts; an endpoint that spends them all costs one dead
// letter and the others still receive theirs.
func (d *Dispatcher) deliver(event Event) {
	body, err := json.Marshal(event)
	if err != nil {
		d.deadLetter(1)
		return
	}
	signature := Sign(d.secret, body)
	for _, endpoint := range d.endpoints {
		if err := d.post(endpoint, body, signature); err != nil {
			log.Warn().Err(err).Str("event", event.Type).
				Msg("Webhook delivery failed after every attempt")
			d.deadLetter(1)
		}
	}
}

func (d *Dispatcher) post(endpoint string, body []byte, signature string) error {
	var lastErr error
	for attempt := 0; attempt < deliveryAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(deliveryRetryDelay << (attempt - 1))
		}
		lastErr = d.attempt(endpoint, body, signature)
		if lastErr == nil {
			return nil
		}
	}
	return lastErr
}

func (d *Dispatcher) attempt(endpoint string, body []byte, signature string) error {
	request, err := http.NewRequestWithContext(context.Background(),
		http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(SignatureHeader, signature)
	response, err := d.client.Do(request)
	if err != nil {
		return err
	}
	_ = response.Body.Close()
	if response.StatusCode >= 300 {
		return fmt.Errorf("webhook endpoint answered %d", response.StatusCode)
	}
	return nil
}

func (d *Dispatcher) deadLetter(count int) {
	if count <= 0 {
		return
	}
	d.deadLetters.Add(int64(count))
	if d.options.OnDeadLetter != nil {
		d.options.OnDeadLetter(count)
	}
}
