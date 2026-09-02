package usage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func sinkTestRecord(requestID string) Record {
	return Record{
		RequestID: requestID,
		KeyID:     "key-a",
		Timestamp: time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC),
		Operation: OperationChat,
		ModelUsed: "openai/gpt-4o",
		Provider:  "openai",
		Status:    StatusOK,
		Tokens:    Tokens{Input: 10, Output: 5, Total: 15},
		Cost:      &Cost{NanoUSD: 1200, Currency: "USD"},
		LatencyMS: 42,
	}
}

func TestFileSinkAppendsOneNDJSONLinePerRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.ndjson")
	sink, err := NewFileSink(path, SinkOptions{FlushInterval: time.Hour})
	if err != nil {
		t.Fatalf("open file sink: %v", err)
	}
	record := sinkTestRecord("req-1")
	sink.Receive(record)
	if err := sink.Close(context.Background()); err != nil {
		t.Fatalf("close file sink: %v", err)
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read export file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(contents)), "\n")
	if len(lines) != 1 {
		t.Fatalf("exported lines = %d, want 1", len(lines))
	}
	want, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if lines[0] != string(want) {
		t.Errorf("exported line = %s, want the stored record %s", lines[0], want)
	}
}

func TestHTTPSinkRetriesAFailedPost(t *testing.T) {
	var calls atomic.Int64
	var mu sync.Mutex
	var received []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var lines []string
		decoder := json.NewDecoder(r.Body)
		for decoder.More() {
			var record Record
			if err := decoder.Decode(&record); err != nil {
				t.Errorf("decode NDJSON body: %v", err)
				return
			}
			lines = append(lines, record.RequestID)
		}
		mu.Lock()
		received = append(received, lines...)
		mu.Unlock()
	}))
	defer server.Close()

	sink := NewHTTPSink(server.URL, SinkOptions{FlushInterval: time.Hour})
	sink.Receive(sinkTestRecord("req-retry"))
	if err := sink.Close(context.Background()); err != nil {
		t.Fatalf("close http sink: %v", err)
	}

	if got := calls.Load(); got != 3 {
		t.Errorf("post attempts = %d, want 3 (two failures, one success)", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 || received[0] != "req-retry" {
		t.Errorf("received records = %v, want [req-retry]", received)
	}
}

func TestHTTPSinkCountsDropsWhenTheTargetStaysDown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	var dropped atomic.Int64
	sink := NewHTTPSink(server.URL, SinkOptions{
		FlushInterval: time.Hour,
		OnDrop:        func(count int) { dropped.Add(int64(count)) },
	})
	sink.Receive(sinkTestRecord("req-a"))
	sink.Receive(sinkTestRecord("req-b"))
	if err := sink.Close(context.Background()); err != nil {
		t.Fatalf("close http sink: %v", err)
	}

	if got := dropped.Load(); got != 2 {
		t.Errorf("dropped records = %d, want 2", got)
	}
	// The sink retains the same count for the admin surface, which holds
	// no observer of its own.
	if got := sink.Dropped(); got != 2 {
		t.Errorf("sink.Dropped() = %d, want 2", got)
	}
}

func TestSinkDropsOldestWhenTheBufferFills(t *testing.T) {
	var dropped atomic.Int64
	var mu sync.Mutex
	var flushed []string
	sink := newBatchingSink(SinkOptions{
		FlushInterval: time.Hour,
		MaxPending:    2,
		OnDrop:        func(count int) { dropped.Add(int64(count)) },
	}, func(batch []Record) error {
		mu.Lock()
		defer mu.Unlock()
		for _, record := range batch {
			flushed = append(flushed, record.RequestID)
		}
		return nil
	})

	sink.Receive(sinkTestRecord("req-1"))
	sink.Receive(sinkTestRecord("req-2"))
	sink.Receive(sinkTestRecord("req-3"))
	if err := sink.Close(context.Background()); err != nil {
		t.Fatalf("close sink: %v", err)
	}

	if got := dropped.Load(); got != 1 {
		t.Errorf("dropped records = %d, want 1 (the oldest)", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(flushed) != 2 || flushed[0] != "req-2" || flushed[1] != "req-3" {
		t.Errorf("flushed records = %v, want the newest two [req-2 req-3]", flushed)
	}
}
