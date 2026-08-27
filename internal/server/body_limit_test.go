package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// unreadBody stands in for a body the gateway should never touch. It records
// the first read, so a test can prove that a refusal happened on the stated
// size rather than after the bytes arrived.
type unreadBody struct{ read bool }

func (b *unreadBody) Read([]byte) (int, error) { b.read = true; return 0, io.EOF }
func (b *unreadBody) Close() error             { return nil }

// chatBody builds a valid chat request padded to a size.
func chatBody(size int) string {
	const opening = `{"model":"mock/test-model","messages":[{"role":"user","content":"`
	const closing = `"}]}`
	padding := size - len(opening) - len(closing)
	if padding < 0 {
		padding = 0
	}
	return opening + strings.Repeat("a", padding) + closing
}

// errorMessage reads the message out of either wire shape. The OpenAI shape
// names it error.message and so does the OpenRouter one, which is why one
// reader serves both.
func errorMessage(t *testing.T, body string) string {
	t.Helper()
	var decoded struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &decoded), body)
	return decoded.Error.Message
}

// TestStatedOversizeBodyIsRefusedBeforeItIsRead holds the refusal that matters
// for media. A caller attaching a large file states the size in a header, and
// the gateway can answer from the header alone. Reading the upload first, only
// to discard it, spends the memory the limit exists to protect.
func TestStatedOversizeBodyIsRefusedBeforeItIsRead(t *testing.T) {
	const limit = 4096
	config := unauthenticatedConfig()
	config.MaxRequestSize = limit
	server := newTestServer(t, config)

	body := &unreadBody{}
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", http.NoBody)
	request.Header.Set("Content-Type", "application/json")
	request.ContentLength = limit * 3
	request.Body = body

	recorder := httptest.NewRecorder()
	server.router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code, recorder.Body.String())
	message := errorMessage(t, recorder.Body.String())
	require.Contains(t, message, strconv.Itoa(limit), "the refusal does not state the limit")
	require.Contains(t, message, strconv.Itoa(limit*3), "the refusal does not state the received size")
	require.False(t, body.read, "the gateway read a body it had already refused")
}

// TestUnstatedOversizeBodyIsRefusedWhileItIsRead covers the caller that sends
// no length. Nothing can answer that one from a header, so the limit has to
// hold at the reader, and the answer still has to be a 413 that names the
// limit: a 400 would tell a caller with one valid large attachment that the
// request was malformed.
func TestUnstatedOversizeBodyIsRefusedWhileItIsRead(t *testing.T) {
	const limit = 4096
	config := unauthenticatedConfig()
	config.MaxRequestSize = limit
	server := newTestServer(t, config)

	// A reader of unknown length leaves Content-Length unset, which is what a
	// chunked upload does.
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		io.NopCloser(strings.NewReader(chatBody(limit*3))))
	request.Header.Set("Content-Type", "application/json")
	require.Equal(t, int64(-1), request.ContentLength, "the test body stated its length")

	recorder := httptest.NewRecorder()
	server.router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code, recorder.Body.String())
	require.Contains(t, errorMessage(t, recorder.Body.String()), strconv.Itoa(limit))
}

// TestBodyWithinTheLimitStillReachesTheHandler proves the limit refuses only
// what it should. The stated-size check runs before routing, so an error in it
// would refuse every request the gateway serves.
func TestBodyWithinTheLimitStillReachesTheHandler(t *testing.T) {
	config := unauthenticatedConfig()
	config.MaxRequestSize = 1 << 20
	server := newTestServer(t, config)

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(chatBody(2048)))
	request.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	server.router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
}

// TestOversizeRefusalAnswersInTheCallerProtocol holds the shape of the answer.
// The size check runs before the route group states which protocol the caller
// speaks, so without a path fallback an OpenRouter caller would receive the
// OpenAI error shape and its client would not find the message.
func TestOversizeRefusalAnswersInTheCallerProtocol(t *testing.T) {
	const limit = 4096
	config := unauthenticatedConfig()
	config.MaxRequestSize = limit
	server := newTestServer(t, config)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/chat/completions",
		strings.NewReader(chatBody(limit*3)))
	request.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	server.router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)

	// The OpenRouter shape carries the status inside the error value. The
	// OpenAI shape carries a type there and no code.
	var decoded struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &decoded), recorder.Body.String())
	require.Equal(t, http.StatusRequestEntityTooLarge, decoded.Error.Code)
	require.Contains(t, decoded.Error.Message, strconv.Itoa(limit))
}
