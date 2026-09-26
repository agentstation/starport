package policyrecord

import (
	"errors"
	"strings"
	"testing"
)

func TestEncodedRecordBound(t *testing.T) {
	if data, err := Marshal(strings.Repeat("a", MaxBytes-2)); err != nil || len(data) != MaxBytes {
		t.Fatalf("boundary = %d, %v", len(data), err)
	}
	if data, err := Marshal(strings.Repeat("é", MaxBytes/2)); !errors.Is(err, ErrTooLarge) || data != nil {
		t.Fatalf("oversized UTF-8 = %d, %v", len(data), err)
	}
}
