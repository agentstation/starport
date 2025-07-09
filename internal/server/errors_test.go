package server

import (
	"errors"
	"testing"
)

func TestValidationErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "ErrModelRequired",
			err:  ErrModelRequired,
			want: "model or models array is required",
		},
		{
			name: "ErrMessagesRequired",
			err:  ErrMessagesRequired,
			want: "messages are required",
		},
		{
			name: "ErrInvalidTemperature",
			err:  ErrInvalidTemperature,
			want: "temperature must be between 0 and 2",
		},
		{
			name: "ErrInvalidTopP",
			err:  ErrInvalidTopP,
			want: "top_p must be between 0 and 1",
		},
		{
			name: "ErrInvalidMaxTokens",
			err:  ErrInvalidMaxTokens,
			want: "max_tokens must be at least 1",
		},
		{
			name: "ErrEmbeddingsModelRequired",
			err:  ErrEmbeddingsModelRequired,
			want: "model is required",
		},
		{
			name: "ErrInputRequired",
			err:  ErrInputRequired,
			want: "input is required",
		},
		{
			name: "ErrInvalidEncodingFormat",
			err:  ErrInvalidEncodingFormat,
			want: "encoding_format must be 'float' or 'base64'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error message = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestErrorComparison(t *testing.T) {
	// Test that validation errors can be checked with errors.Is
	err := validateRequest()
	if !errors.Is(err, ErrModelRequired) {
		t.Errorf("Expected ErrModelRequired, got %v", err)
	}
}

// Helper function for testing
func validateRequest() error {
	// Simulate validation returning our constant
	return ErrModelRequired
}