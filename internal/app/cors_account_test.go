package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentstation/starport/internal/server"
)

func TestServerAccountSelectionPreflight(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		cfg := validProductionConfig(t)
		cfg.Security.EnableCORS = enabled
		cfg.Security.AllowedOrigins = "https://console.example.com"
		handler := server.CORS(serverConfig(cfg, authRuntime{}).CORS)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		req := httptest.NewRequest(http.MethodOptions, "/api/v1/models", nil)
		req.Header.Set("Origin", "https://console.example.com")
		req.Header.Set("Access-Control-Request-Method", http.MethodGet)
		req.Header.Set("Access-Control-Request-Headers", "X-Starport-Account-ID")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		allowed := strings.EqualFold(response.Header().Get("Access-Control-Allow-Headers"), "X-Starport-Account-ID")
		if allowed != enabled {
			t.Fatalf("CORS enabled = %v, account selection permitted = %v", enabled, allowed)
		}
		if enabled && response.Header().Get("Access-Control-Allow-Credentials") != "true" {
			t.Fatal("configured console origin cannot send session credentials")
		}
	}
}
