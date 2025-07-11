package server

import "errors"

// Request validation errors
var (
	// ErrModelRequired is returned when neither model nor models array is provided
	ErrModelRequired = errors.New("model or models array is required")
	// ErrMessagesRequired is returned when messages are not provided
	ErrMessagesRequired = errors.New("messages are required")
	// ErrInvalidTemperature is returned when temperature is out of valid range
	ErrInvalidTemperature = errors.New("temperature must be between 0 and 2")
	// ErrInvalidTopP is returned when top_p is out of valid range
	ErrInvalidTopP = errors.New("top_p must be between 0 and 1")
	// ErrInvalidMaxTokens is returned when max_tokens is less than 1
	ErrInvalidMaxTokens = errors.New("max_tokens must be at least 1")
	// ErrInvalidN is returned when n is less than 1
	ErrInvalidN = errors.New("n must be at least 1")
	// ErrInvalidPresencePenalty is returned when presence_penalty is out of valid range
	ErrInvalidPresencePenalty = errors.New("presence_penalty must be between -2 and 2")
	// ErrInvalidFrequencyPenalty is returned when frequency_penalty is out of valid range
	ErrInvalidFrequencyPenalty = errors.New("frequency_penalty must be between -2 and 2")
	// ErrInvalidMinP is returned when min_p is out of valid range
	ErrInvalidMinP = errors.New("min_p must be between 0 and 1")
	// ErrInvalidTopA is returned when top_a is out of valid range
	ErrInvalidTopA = errors.New("top_a must be between 0 and 1")
	// ErrInvalidRepetitionPenalty is returned when repetition_penalty is out of valid range
	ErrInvalidRepetitionPenalty = errors.New("repetition_penalty must be greater than 0")

	// Embeddings validation errors
	// ErrEmbeddingsModelRequired is returned when model is not provided for embeddings
	ErrEmbeddingsModelRequired = errors.New("model is required")
	// ErrInputRequired is returned when input is not provided
	ErrInputRequired = errors.New("input is required")
	// ErrInvalidEncodingFormat is returned when encoding format is invalid
	ErrInvalidEncodingFormat = errors.New("encoding_format must be 'float' or 'base64'")
)
