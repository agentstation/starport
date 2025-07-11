package storage

import (
	"errors"
	"testing"
)

func TestErrorConstants(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "ErrTransactionClosed",
			err:  ErrTransactionClosed,
			want: "transaction is closed",
		},
		{
			name: "ErrTransactionCommitted",
			err:  ErrTransactionCommitted,
			want: "transaction already committed",
		},
		{
			name: "ErrTransactionRolledBack",
			err:  ErrTransactionRolledBack,
			want: "transaction already rolled back",
		},
		{
			name: "ErrPubSubClosed",
			err:  ErrPubSubClosed,
			want: "pubsub client is closed",
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

func TestErrorIs(t *testing.T) {
	// Test that errors.Is works correctly with our constants
	tests := []struct {
		name   string
		err    error
		target error
		want   bool
	}{
		{
			name:   "Same error",
			err:    ErrTransactionClosed,
			target: ErrTransactionClosed,
			want:   true,
		},
		{
			name:   "Different error",
			err:    ErrTransactionClosed,
			target: ErrTransactionCommitted,
			want:   false,
		},
		{
			name:   "Nil error",
			err:    nil,
			target: ErrTransactionClosed,
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := errors.Is(tt.err, tt.target); got != tt.want {
				t.Errorf("errors.Is() = %v, want %v", got, tt.want)
			}
		})
	}
}
