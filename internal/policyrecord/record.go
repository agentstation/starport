// Package policyrecord bounds serialized authorization records before decoding.
package policyrecord

import (
	"encoding/json"
	"errors"
)

// MaxBytes is the maximum encoded size of one key, account, user, or team record.
const MaxBytes = 64 << 10

// ErrTooLarge refuses oversized policy without treating it as absent.
var ErrTooLarge = errors.New("authorization record exceeds size limit")

// Marshal prevents repository writes that bounded policy reads cannot accept.
func Marshal(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(data) > MaxBytes {
		return nil, ErrTooLarge
	}
	return data, nil
}
