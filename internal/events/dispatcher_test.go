package events

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// delivery is what the test receiver saw: one body and its signature.
type delivery struct {
	body      []byte
	signature string
}

// newTestReceiver serves 200 and records each delivery on the channel.
func newTestReceiver(t *testing.T) (*httptest.Server, chan delivery) {
	t.Helper()
	deliveries := make(chan delivery, 16)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read delivery body: %v", err)
		}
		deliveries <- delivery{body: body, signature: r.Header.Get(SignatureHeader)}
	}))
	t.Cleanup(server.Close)
	return server, deliveries
}

func receiveDelivery(t *testing.T, deliveries chan delivery) delivery {
	t.Helper()
	select {
	case d := <-deliveries:
		return d
	case <-time.After(5 * time.Second):
		t.Fatal("no delivery arrived")
		return delivery{}
	}
}

// The acceptance walk: a budget-exhausted event and a job-completed event
// arrive at a receiver, each signed, each verifiable with the shared
// secret.
func TestDispatcherDeliversSignedEvents(t *testing.T) {
	server, deliveries := newTestReceiver(t)
	secret := "whsec_test"
	dispatcher := NewDispatcher([]string{server.URL}, secret, Options{})

	dispatcher.Emit(TypeBudgetExhausted, map[string]string{
		"scope": "account", "dimension": "spend", "interval": "day",
	})
	dispatcher.Emit(TypeJobCompleted, map[string]string{
		"job_id": "job-1", "provider": "openai", "state": "completed",
	})

	first := receiveDelivery(t, deliveries)
	second := receiveDelivery(t, deliveries)
	if err := dispatcher.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}

	for i, d := range []delivery{first, second} {
		if !Verify([]byte(secret), d.body, d.signature) {
			t.Fatalf("delivery %d signature does not verify: %s", i, d.signature)
		}
		var event Event
		if err := json.Unmarshal(d.body, &event); err != nil {
			t.Fatalf("delivery %d body: %v", i, err)
		}
		if !strings.HasPrefix(event.ID, "evt-") {
			t.Fatalf("delivery %d id = %q", i, event.ID)
		}
		if _, err := time.Parse(time.RFC3339, event.Time); err != nil {
			t.Fatalf("delivery %d time %q: %v", i, event.Time, err)
		}
	}

	var budget, job Event
	if err := json.Unmarshal(first.body, &budget); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second.body, &job); err != nil {
		t.Fatal(err)
	}
	if budget.Type != TypeBudgetExhausted || budget.Data["dimension"] != "spend" {
		t.Fatalf("first event = %+v", budget)
	}
	if job.Type != TypeJobCompleted || job.Data["job_id"] != "job-1" {
		t.Fatalf("second event = %+v", job)
	}
}

// A receiver that fails and recovers gets the event on a later attempt,
// and nothing dead-letters.
func TestDispatcherRetriesAFailedDelivery(t *testing.T) {
	var attempts atomic.Int32
	delivered := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		body, _ := io.ReadAll(r.Body)
		delivered <- body
	}))
	defer server.Close()

	var deadLetters atomic.Int32
	dispatcher := NewDispatcher([]string{server.URL}, "s", Options{
		OnDeadLetter: func(count int) { deadLetters.Add(int32(count)) },
	})
	dispatcher.Emit(TypeJobFailed, map[string]string{"job_id": "job-2"})

	select {
	case <-delivered:
	case <-time.After(10 * time.Second):
		t.Fatal("the third attempt never landed")
	}
	if err := dispatcher.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
	if got := deadLetters.Load(); got != 0 {
		t.Fatalf("dead letters = %d, want 0", got)
	}
}

// An endpoint that never answers well costs exactly one dead letter per
// event after the attempts are spent.
func TestDispatcherCountsADeadLetterWhenTheEndpointStaysDown(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	var deadLetters atomic.Int32
	dispatcher := NewDispatcher([]string{server.URL}, "s", Options{
		OnDeadLetter: func(count int) { deadLetters.Add(int32(count)) },
	})
	dispatcher.Emit(TypeKeyDeleted, map[string]string{"key_id": "key_1"})
	if err := dispatcher.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
	if got := deadLetters.Load(); got != 1 {
		t.Fatalf("dead letters = %d, want 1", got)
	}
}

// An emit after Close cannot deliver, so it counts instead of vanishing.
func TestEmitAfterCloseCountsADeadLetter(t *testing.T) {
	server, _ := newTestReceiver(t)
	var deadLetters atomic.Int32
	dispatcher := NewDispatcher([]string{server.URL}, "s", Options{
		OnDeadLetter: func(count int) { deadLetters.Add(int32(count)) },
	})
	if err := dispatcher.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	dispatcher.Emit(TypeKeyCreated, map[string]string{"key_id": "key_2"})
	if got := deadLetters.Load(); got != 1 {
		t.Fatalf("dead letters = %d, want 1", got)
	}
}

// Stats states what the admin surface reports: the receivers without
// their secrets, what waits in the queue, and what will never deliver.
// The dead-letter count is retained on the dispatcher, so it reads the
// same with or without a metric observer.
func TestDispatcherStatsReportQueueDepthAndDeadLetters(t *testing.T) {
	dispatcher := NewDispatcher(
		[]string{"https://receiver@hooks.example.com/starport?ticket=t1"},
		"s", Options{MaxPending: 4},
	)
	if err := dispatcher.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	dispatcher.Emit(TypeKeyCreated, map[string]string{"key_id": "key_3"})
	dispatcher.Emit(TypeKeyDeleted, map[string]string{"key_id": "key_3"})

	stats := dispatcher.Stats()
	if len(stats.Endpoints) != 1 || stats.Endpoints[0] != "https://hooks.example.com/starport" {
		t.Fatalf("endpoints = %q, want the redacted receiver", stats.Endpoints)
	}
	if stats.QueueDepth != 0 || stats.QueueCapacity != 4 {
		t.Fatalf("queue = %d of %d, want 0 of 4", stats.QueueDepth, stats.QueueCapacity)
	}
	if stats.DeadLetters != 2 {
		t.Fatalf("dead letters = %d, want 2", stats.DeadLetters)
	}
	if got := (*Dispatcher)(nil).Stats(); got.QueueCapacity != 0 || len(got.Endpoints) != 0 {
		t.Fatalf("nil stats = %+v, want the unconfigured zero", got)
	}
}

// RedactEndpoint keeps where a delivery goes and drops how it
// authenticates.
func TestRedactEndpoint(t *testing.T) {
	cases := map[string]string{
		"https://hooks.example.com/starport":                    "https://hooks.example.com/starport",
		"https://receiver@hooks.example.com/starport?ticket=t1": "https://hooks.example.com/starport",
		"http://127.0.0.1:9000/?key=abc":                        "http://127.0.0.1:9000/",
		"not a url?ticket=t1":                                   "not a url",
	}
	for raw, want := range cases {
		if got := RedactEndpoint(raw); got != want {
			t.Errorf("RedactEndpoint(%q) = %q, want %q", raw, got, want)
		}
	}
}

// No configured endpoint means no dispatcher at all, and the nil
// dispatcher is safe everywhere an emit site holds one.
func TestNewDispatcherWithoutEndpointsIsOff(t *testing.T) {
	dispatcher := NewDispatcher(nil, "s", Options{})
	if dispatcher != nil {
		t.Fatal("a dispatcher exists with no endpoint to serve")
	}
	dispatcher.Emit(TypeBudgetExhausted, nil)
	if err := dispatcher.Close(context.Background()); err != nil {
		t.Fatalf("nil close: %v", err)
	}
}

func TestVerifyRejectsATamperedBody(t *testing.T) {
	secret := []byte("whsec_test")
	body := []byte(`{"id":"evt-1"}`)
	signature := Sign(secret, body)
	if !Verify(secret, body, signature) {
		t.Fatal("the untouched body must verify")
	}
	if Verify(secret, []byte(`{"id":"evt-2"}`), signature) {
		t.Fatal("a tampered body must not verify")
	}
	if Verify([]byte("other"), body, signature) {
		t.Fatal("a wrong secret must not verify")
	}
}

// The operator guide documents this exact sample. A receiver implementing
// the documented check verifies real deliveries, held by this pin.
func TestVerifyMatchesTheDocumentedSample(t *testing.T) {
	secret := []byte("whsec_demo_secret")
	body := []byte(`{"id":"evt-sample","type":"budget.exhausted",` +
		`"time":"2026-08-30T00:00:00Z","data":{"scope":"account"}}`)
	want := "sha256=45f5f4544c3390994afa396a4fe0415d0c9574a32a3e1973bd9342b3e02a5b1d"
	if got := Sign(secret, body); got != want {
		t.Fatalf("documented signature drifted: %s", got)
	}
	if !Verify(secret, body, want) {
		t.Fatal("the documented sample must verify")
	}
}

func TestTypeForJobState(t *testing.T) {
	cases := map[string]string{
		"completed": TypeJobCompleted,
		"cancelled": TypeJobCancelled,
		"failed":    TypeJobFailed,
		"":          TypeJobFailed,
	}
	for state, want := range cases {
		if got := TypeForJobState(state); got != want {
			t.Fatalf("TypeForJobState(%q) = %s, want %s", state, got, want)
		}
	}
}
