package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sethvargo/go-envconfig"
)

func TestAccountSelectionCORS(t *testing.T) {
	for _, tc := range []struct {
		name    string
		values  map[string]string
		allowed bool
	}{
		{name: "default", allowed: true},
		{name: "explicit denial", values: map[string]string{"CORS_ALLOWED_HEADERS": "Content-Type"}},
		{name: "explicit permission", values: map[string]string{"CORS_ALLOWED_HEADERS": "Content-Type,X-Starport-Account-ID"}, allowed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cfg CORSConfig
			if err := envconfig.ProcessWith(t.Context(), &envconfig.Config{Target: &cfg, Lookuper: envconfig.MapLookuper(tc.values)}); err != nil {
				t.Fatal(err)
			}
			called := false
			handler := CORS(cfg)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
			req := httptest.NewRequest(http.MethodOptions, "/api/v1/models", nil)
			req.Header.Set("Origin", "https://console.example.com")
			req.Header.Set("Access-Control-Request-Method", http.MethodGet)
			req.Header.Set("Access-Control-Request-Headers", "X-Starport-Account-ID")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			allowed := strings.EqualFold(response.Header().Get("Access-Control-Allow-Headers"), "X-Starport-Account-ID")
			if allowed != tc.allowed {
				t.Fatalf("account selection permitted = %v, want %v; headers: %v", allowed, tc.allowed, response.Header())
			}
			if called {
				t.Fatal("preflight reached application handler")
			}
		})
	}
}
