package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	protocol "github.com/agentstation/starmap/pkg/catalogs/remote"
)

func TestProbeSourceReportsSafeOutcomes(t *testing.T) {
	const secret = "probe-secret-value"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1"+protocol.ManifestPath && r.Header.Get("Authorization") == "Bearer "+secret:
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/api/v1"+protocol.ManifestPath:
			w.WriteHeader(http.StatusUnauthorized)
		case r.URL.Path == "/repos/agentstation/starmap":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	previous := githubProbeBaseURL
	githubProbeBaseURL = server.URL
	t.Cleanup(func() { githubProbeBaseURL = previous })

	file := filepath.Join(t.TempDir(), "catalog.json")
	if err := os.WriteFile(file, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name     string
		settings Settings
		want     string
	}{
		{name: "embedded", settings: Settings{Source: "embedded"}},
		{name: "file", settings: Settings{Source: "file", SourceURL: file}},
		{name: "missing file", settings: Settings{Source: "file", SourceURL: file + ".missing"}, want: "not a readable regular file"},
		{name: "starmap", settings: Settings{Source: "starmap", SourceURL: server.URL + "/api/v1", SourceAPIKey: secret}},
		{name: "refused credential", settings: Settings{Source: "starmap", SourceURL: server.URL + "/api/v1", SourceAPIKey: secret + "-wrong"}, want: "refused the credential"},
		{name: "plain remote", settings: Settings{Source: "starmap", SourceURL: "http://example.com/api/v1"}, want: "must use HTTPS"},
		{name: "public", settings: Settings{Source: "public"}},
		{name: "missing repository", settings: Settings{Source: "github", SourceRepository: "agentstation/absent", SourceToken: secret}, want: "was not found"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ProbeSource(context.Background(), test.settings)
			if test.want == "" {
				if err != nil {
					t.Fatalf("ProbeSource() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ProbeSource() error = %v, want %q", err, test.want)
			}
			if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), server.URL) || strings.Contains(err.Error(), file) {
				t.Fatalf("ProbeSource() error %q discloses a credential or a source location", err)
			}
		})
	}
}
