package proxy

import (
	"github.com/agentstation/starport/internal/providers/connectors"
)

// StreamWrapper wraps a connector stream to add additional functionality.
// This is the core streaming wrapper that enhances the basic connector stream.
type StreamWrapper struct {
	stream  connectors.ChatStream
	modelID string
}

// NewStreamWrapper creates a new stream wrapper that adds model information.
func NewStreamWrapper(stream connectors.ChatStream, modelID string) ChatCompletionStreamResponse {
	return &StreamWrapper{
		stream:  stream,
		modelID: modelID,
	}
}

// Read returns the next chunk from the stream, adding model information if needed.
func (w *StreamWrapper) Read() (*connectors.ChatStreamChunk, error) {
	chunk, err := w.stream.Recv()

	// Add model_used to the chunk if present
	if chunk != nil && chunk.Model == "" {
		chunk.Model = w.modelID
	}

	return chunk, err
}

// Close closes the underlying stream.
func (w *StreamWrapper) Close() error {
	return w.stream.Close()
}

// EnhancedStreamWrapper provides additional functionality on top of StreamWrapper.
// This can be used to add metrics, logging, or other cross-cutting concerns.
type EnhancedStreamWrapper struct {
	ChatCompletionStreamResponse
	onRead  func(*connectors.ChatStreamChunk, error)
	onClose func(error)
}

// NewEnhancedStreamWrapper creates a stream wrapper with callbacks.
func NewEnhancedStreamWrapper(
	stream ChatCompletionStreamResponse,
	onRead func(*connectors.ChatStreamChunk, error),
	onClose func(error),
) ChatCompletionStreamResponse {
	return &EnhancedStreamWrapper{
		ChatCompletionStreamResponse: stream,
		onRead:                       onRead,
		onClose:                      onClose,
	}
}

// Read calls the underlying stream and triggers the onRead callback.
func (e *EnhancedStreamWrapper) Read() (*connectors.ChatStreamChunk, error) {
	chunk, err := e.ChatCompletionStreamResponse.Read()
	if e.onRead != nil {
		e.onRead(chunk, err)
	}
	return chunk, err
}

// Close calls the underlying stream and triggers the onClose callback.
func (e *EnhancedStreamWrapper) Close() error {
	err := e.ChatCompletionStreamResponse.Close()
	if e.onClose != nil {
		e.onClose(err)
	}
	return err
}

// BufferedStreamWrapper provides buffering capabilities for a stream.
// This can be useful for scenarios where you need to peek at chunks
// or buffer them for processing.
type BufferedStreamWrapper struct {
	stream ChatCompletionStreamResponse
	buffer []*connectors.ChatStreamChunk
	index  int
}

// NewBufferedStreamWrapper creates a new buffered stream wrapper.
func NewBufferedStreamWrapper(stream ChatCompletionStreamResponse) *BufferedStreamWrapper {
	return &BufferedStreamWrapper{
		stream: stream,
		buffer: make([]*connectors.ChatStreamChunk, 0),
		index:  0,
	}
}

// Read returns buffered chunks first, then reads from the underlying stream.
func (b *BufferedStreamWrapper) Read() (*connectors.ChatStreamChunk, error) {
	// If we have buffered chunks, return them first
	if b.index < len(b.buffer) {
		chunk := b.buffer[b.index]
		b.index++
		return chunk, nil
	}

	// Otherwise, read from the underlying stream
	return b.stream.Read()
}

// Buffer adds a chunk to the buffer.
func (b *BufferedStreamWrapper) Buffer(chunk *connectors.ChatStreamChunk) {
	b.buffer = append(b.buffer, chunk)
}

// Reset clears the buffer and resets the index.
func (b *BufferedStreamWrapper) Reset() {
	b.buffer = b.buffer[:0]
	b.index = 0
}

// Close closes the underlying stream.
func (b *BufferedStreamWrapper) Close() error {
	return b.stream.Close()
}
