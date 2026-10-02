package catalog

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	protocol "github.com/agentstation/starmap/pkg/catalogs/remote"
	"github.com/agentstation/starmap/runtime"
)

// sourceProbeTimeout bounds one source reachability probe.
const sourceProbeTimeout = 10 * time.Second

// defaultProbeRepository is the public catalog repository that a public or
// github source reads when no repository is set.
const defaultProbeRepository = "agentstation/starmap"

// githubProbeBaseURL is the GitHub API root. Tests replace it.
var githubProbeBaseURL = "https://api.github.com"

// ProbeError is a source reachability failure. Its message never holds a
// credential or a source URL.
type ProbeError struct {
	Message string
}

func (e *ProbeError) Error() string { return e.Message }

// ProbeSource checks that the catalog source of the settings answers within a
// bounded time. It reads no catalog payload and changes no state. An embedded
// source needs no connection. A file source must be a regular file. A starmap
// source must serve its current manifest. A public or github source must show
// its repository to the configured token.
func ProbeSource(ctx context.Context, settings Settings) error {
	ctx, cancel := context.WithTimeout(ctx, sourceProbeTimeout)
	defer cancel()
	switch runtime.SourceKind(strings.TrimSpace(settings.Source)) {
	case runtime.SourceEmbedded:
		return nil
	case runtime.SourceFile:
		info, err := os.Stat(strings.TrimSpace(settings.SourceURL))
		if err != nil || !info.Mode().IsRegular() {
			return &ProbeError{Message: "the catalog file is not a readable regular file"}
		}
		return nil
	case runtime.SourceStarmap:
		return probeStarmap(ctx, settings)
	case runtime.SourcePublic, runtime.SourceGitHub, "":
		return probeGitHub(ctx, settings)
	default:
		return &ProbeError{Message: "the catalog source kind is not known"}
	}
}

func probeStarmap(ctx context.Context, settings Settings) error {
	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(settings.SourceURL), "/"))
	if err != nil || (base.Scheme != "https" && base.Scheme != "http") || base.Host == "" || base.User != nil {
		return &ProbeError{Message: "the catalog source URL is not an absolute HTTP(S) URL"}
	}
	if base.Scheme == "http" && !probeLoopback(base.Hostname()) {
		return &ProbeError{Message: "a non-loopback catalog source must use HTTPS"}
	}
	base.Path = path.Join(base.Path, protocol.ManifestPath)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return &ProbeError{Message: "the catalog source request cannot be built"}
	}
	request.Header.Set("Accept", protocol.ManifestMediaType)
	if key := strings.TrimSpace(settings.SourceAPIKey); key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	return probeResponse(request, "the catalog source")
}

func probeGitHub(ctx context.Context, settings Settings) error {
	repository := strings.TrimSpace(settings.SourceRepository)
	if repository == "" {
		repository = defaultProbeRepository
	}
	owner, name, found := strings.Cut(repository, "/")
	if !found || owner == "" || name == "" || strings.Contains(name, "/") {
		return &ProbeError{Message: "the catalog source repository is not owner/name"}
	}
	target := strings.TrimRight(githubProbeBaseURL, "/") + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return &ProbeError{Message: "the catalog repository request cannot be built"}
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	if token := strings.TrimSpace(settings.SourceToken); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	return probeResponse(request, "the catalog repository")
}

// probeResponse sends one probe request and maps its outcome to a message
// that names no URL and no credential.
func probeResponse(request *http.Request, subject string) error {
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return &ProbeError{Message: subject + " did not answer in time"}
		}
		return &ProbeError{Message: subject + " is not reachable"}
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	switch response.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return &ProbeError{Message: subject + " refused the credential"}
	case http.StatusNotFound:
		return &ProbeError{Message: subject + " was not found, or the credential cannot read it"}
	default:
		return &ProbeError{Message: subject + " answered with HTTP " + http.StatusText(response.StatusCode)}
	}
}

func probeLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
