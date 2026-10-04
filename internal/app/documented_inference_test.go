package app

import (
	"bufio"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/markdowntest"
)

var (
	documentedHeader = regexp.MustCompile(`-H "([^"]+)"`)
	documentedBody   = regexp.MustCompile(`-d '([^']+)'`)
	documentedURL    = regexp.MustCompile(`(http://\S+)`)
)

// documentedRequest is the curl request of the README quick start.
type documentedRequest struct {
	headers []string
	body    string
	path    string
}

// readDocumentedRequest reads the curl block that sends a body in the
// "Terminal 2" quick start section of the README.
func readDocumentedRequest(t *testing.T) documentedRequest {
	t.Helper()
	_, sections := markdowntest.Parse(markdowntest.ReadFile(t, "README.md"))
	for _, section := range sections {
		if section.Heading != "Quick start" {
			continue
		}
		for _, child := range section.Subsections {
			if !strings.HasPrefix(child.Heading, "Terminal 2:") {
				continue
			}
			for _, fence := range markdowntest.Fences(child.Body) {
				command := strings.Join(fence.Lines, "\n")
				body := documentedBody.FindStringSubmatch(command)
				if body == nil {
					continue
				}
				request := documentedRequest{body: body[1]}
				for _, header := range documentedHeader.FindAllStringSubmatch(command, -1) {
					request.headers = append(request.headers, header[1])
				}
				target := documentedURL.FindAllString(command, -1)
				require.NotEmpty(t, target, "the README request has no URL")
				parsed, err := url.Parse(target[len(target)-1])
				require.NoError(t, err)
				request.path = parsed.Path
				return request
			}
		}
	}
	t.Fatal("the README quick start has no Terminal 2 request with a body")
	return documentedRequest{}
}

// TestDocumentedInferenceRequestStreamsThroughGateway sends the README request,
// with its headers and body, through the composed gateway to a fake provider.
func TestDocumentedInferenceRequestStreamsThroughGateway(t *testing.T) {
	documented := readDocumentedRequest(t)
	require.Equal(t, "/api/v1/chat/completions", documented.path)
	require.ElementsMatch(t, []string{"Authorization: Bearer $STARPORT_API_KEY", "Content-Type: application/json"}, documented.headers)

	f := newPerformanceFixture(t, 0)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, f.gateway.URL+documented.path, strings.NewReader(documented.body))
	require.NoError(t, err)
	for _, header := range documented.headers {
		name, value, found := strings.Cut(header, ": ")
		require.True(t, found, header)
		request.Header.Set(name, strings.ReplaceAll(value, "$STARPORT_API_KEY", performanceGatewayKey))
	}
	response, err := f.client.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.True(t, strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream"), response.Header.Get("Content-Type"))

	var events []string
	content := 0
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		events = append(events, line)
		if strings.HasPrefix(line, "data: {") && strings.Contains(line, `"content":"`) {
			content++
		}
	}
	require.NoError(t, scanner.Err())
	require.Positive(t, content, "the stream has no content event: %q", events)
	require.Equal(t, "data: [DONE]", events[len(events)-1])
	require.EqualValues(t, 1, f.calls.Load())

	select {
	case <-f.handlers:
	case <-time.After(time.Second):
		t.Fatal("the gateway handler did not complete")
	}
}
