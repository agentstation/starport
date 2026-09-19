package server

import (
	"bytes"
	"net/http"
	"reflect"

	"github.com/agentstation/starport/internal/catalog/disclosure"
	"github.com/agentstation/starport/internal/providers/connectors"
)

// requireCatalogDisclosure retains one catalog and current caller policy until delivery.
func (s *Server) requireCatalogDisclosure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		refuse := func() {
			writeProtocolError(w, r, http.StatusServiceUnavailable, "server_error", "Catalog is unavailable")
		}
		if s.discoveryRegistry == nil {
			refuse()
			return
		}
		viewer, err := s.auth.discoveryViewer(r)
		if err != nil || !viewer.Valid() {
			refuse()
			return
		}
		lease, err := s.discoveryRegistry.AcquireRuntime()
		if err != nil || lease == nil {
			refuse()
			return
		}
		defer lease.Release()
		snapshot := lease.Snapshot()
		if snapshot == nil || snapshot.CheckNewAttempt() != nil {
			refuse()
			return
		}
		ctx := connectors.ContextWithRuntimeLease(r.Context(), lease)
		ctx = disclosure.WithPolicy(ctx, disclosure.New(snapshot, viewer.Key, viewer.Account))
		response := catalogResponse{header: make(http.Header)}
		next.ServeHTTP(&response, r.WithContext(ctx))
		current, err := s.auth.discoveryViewer(r)
		if err != nil || !current.Valid() || !reflect.DeepEqual(viewer, current) || snapshot.CheckNewAttempt() != nil {
			refuse()
			return
		}
		if r.Context().Err() != nil {
			return
		}
		for name, values := range response.header {
			w.Header()[name] = values
		}
		w.Header().Set("Cache-Control", "no-store")
		if response.status != 0 {
			w.WriteHeader(response.status)
		}
		_, _ = w.Write(response.body.Bytes())
	})
}

// catalogResponse buffers metadata until the final permission check.
type catalogResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (r *catalogResponse) Header() http.Header { return r.header }
func (r *catalogResponse) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}
func (r *catalogResponse) Write(data []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.body.Write(data)
}
