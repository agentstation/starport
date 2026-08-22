package connectors

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func sseStream(body string) ChatStream {
	return newOpenAICompatibleStream(&http.Response{
		Body: io.NopCloser(strings.NewReader(body)),
	})
}

// Groq rejects an over-quota streaming request with HTTP 200 and an SSE
// "event: error" frame. The codec must surface that frame as a provider
// failure with the rate-limit status, never as a silent skip or an empty
// chunk followed by a clean [DONE].
func TestStreamRecvSurfacesEventErrorFrame(t *testing.T) {
	stream := sseStream(
		"event: error\n" +
			`data: {"error":{"message":"Rate limit reached for model","type":"tokens","code":"rate_limit_exceeded"}}` + "\n\n" +
			"data: [DONE]\n\n",
	)

	chunk, err := stream.Recv()
	require.Nil(t, chunk)
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, http.StatusTooManyRequests, apiErr.StatusCode)
	require.Equal(t, "rate_limit_exceeded", apiErr.Code)
	require.Contains(t, apiErr.Message, "Rate limit reached")
}

// Some OpenAI-compatible providers deliver the error object as a plain data
// frame without an event name. A top-level error object is never a chunk.
func TestStreamRecvSurfacesErrorDataFrame(t *testing.T) {
	stream := sseStream(
		`data: {"error":{"message":"Invalid API Key","type":"invalid_request_error","code":"invalid_api_key"}}` + "\n\n",
	)

	chunk, err := stream.Recv()
	require.Nil(t, chunk)
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, http.StatusUnauthorized, apiErr.StatusCode)
	require.Equal(t, "invalid_api_key", apiErr.Code)
}

// An error frame after content must end the stream as a failure, not fall
// through to a clean EOF.
func TestStreamRecvSurfacesMidStreamErrorFrame(t *testing.T) {
	stream := sseStream(
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"partial"}}]}` + "\n\n" +
			"event: error\n" +
			`data: {"error":{"message":"Rate limit reached","type":"tokens","code":"rate_limit_exceeded"}}` + "\n\n",
	)

	chunk, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "partial", chunk.Choices[0].Delta.Content)

	chunk, err = stream.Recv()
	require.Nil(t, chunk)
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, http.StatusTooManyRequests, apiErr.StatusCode)
}

// An error frame without a recognized code still fails the stream with the
// provider text instead of decoding into an empty chunk.
func TestStreamRecvSurfacesUnknownErrorFrame(t *testing.T) {
	stream := sseStream(
		"event: error\n" +
			`data: {"error":{"message":"internal provider fault"}}` + "\n\n",
	)

	chunk, err := stream.Recv()
	require.Nil(t, chunk)
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, http.StatusBadGateway, apiErr.StatusCode)
	require.Equal(t, "internal provider fault", apiErr.Message)
}

// A healthy stream still decodes chunks and terminates on [DONE].
func TestStreamRecvKeepsHealthyStreamBehavior(t *testing.T) {
	stream := sseStream(
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"hello"}}]}` + "\n\n" +
			"data: [DONE]\n\n",
	)

	chunk, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "hello", chunk.Choices[0].Delta.Content)

	chunk, err = stream.Recv()
	require.Nil(t, chunk)
	require.ErrorIs(t, err, io.EOF)
}
