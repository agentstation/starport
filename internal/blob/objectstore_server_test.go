package blob_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// objectServer serves the part of the S3 API the blob backend calls, in
// process and in memory.
//
// It exists so the contract test exercises the real client: the AWS SDK signs
// the request, encodes the body, reads the response, and maps the error. A
// hand-written fake of the blob interface would prove none of that, and the
// adapter is the only thing FIL2 adds.
type objectServer struct {
	mu      sync.Mutex
	objects map[string][]byte

	// bucket is the one bucket this server serves. A request for another
	// bucket answers 404, which catches a prefix or bucket the backend
	// assembles wrongly.
	bucket string
}

func newObjectServer(t *testing.T, bucket string) (*objectServer, string) {
	t.Helper()
	server := &objectServer{objects: map[string][]byte{}, bucket: bucket}
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	return server, httpServer.URL
}

// stored reports one object, so a test asserts on the medium rather than on
// the answer the backend gave.
func (s *objectServer) stored(key string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.objects[key]
	return value, ok
}

// count reports how many objects the server holds.
func (s *objectServer) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.objects)
}

func (s *objectServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	bucket, key, ok := s.route(r.URL.Path)
	if !ok || bucket != s.bucket {
		s.writeError(w, r, http.StatusNotFound, "NoSuchBucket")
		return
	}
	switch r.Method {
	case http.MethodPut:
		s.put(w, r, key)
	case http.MethodGet:
		s.get(w, r, key)
	case http.MethodHead:
		s.head(w, r, key)
	case http.MethodDelete:
		s.remove(w, key)
	default:
		s.writeError(w, r, http.StatusMethodNotAllowed, "MethodNotAllowed")
	}
}

// route splits a path-style URL into its bucket and its key.
func (s *objectServer) route(path string) (string, string, bool) {
	trimmed := strings.TrimPrefix(path, "/")
	bucket, key, found := strings.Cut(trimmed, "/")
	if !found || bucket == "" || key == "" {
		return "", "", false
	}
	return bucket, key, true
}

func (s *objectServer) put(w http.ResponseWriter, r *http.Request, key string) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		// A body that stops early never becomes an object. This is the
		// property the interrupted-put cases depend on.
		s.writeError(w, r, http.StatusBadRequest, "IncompleteBody")
		return
	}
	s.mu.Lock()
	s.objects[key] = body
	s.mu.Unlock()
	w.Header().Set("ETag", fmt.Sprintf("%q", key))
	w.WriteHeader(http.StatusOK)
}

func (s *objectServer) get(w http.ResponseWriter, r *http.Request, key string) {
	value, ok := s.stored(key)
	if !ok {
		s.writeError(w, r, http.StatusNotFound, "NoSuchKey")
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(value)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(value)
}

func (s *objectServer) head(w http.ResponseWriter, r *http.Request, key string) {
	value, ok := s.stored(key)
	if !ok {
		// A HEAD answer carries no body, so the status is the only signal the
		// client reads. The backend has to map it without an error code.
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(value)))
	w.WriteHeader(http.StatusOK)
}

func (s *objectServer) remove(w http.ResponseWriter, key string) {
	s.mu.Lock()
	delete(s.objects, key)
	s.mu.Unlock()
	// S3 answers a delete of an absent key with success, so a repeated sweep
	// does not fail the second time.
	w.WriteHeader(http.StatusNoContent)
}

func (s *objectServer) writeError(w http.ResponseWriter, r *http.Request, status int, code string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>%s</Code><Message>%s</Message></Error>`, code, code)
}
