package reservation

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"slices"
	"strings"

	"github.com/agentstation/starport/internal/storage"
)

const maxCorrectionPublicationRecords = 8
const maxCorrectionPublicationKeyBytes = 4096

// CorrectWith commits a correction with its owner's job and audit publication.
// Publication must use the same storage authority and persistent records.
// Exact retries bind both the decision and the complete publication contents.
func (r *Repository) CorrectWith(ctx context.Context, id string, correction Correction, publication []storage.CompareAndSwapMutation) (*CorrectionReceipt, error) {
	owned, digest, err := correctionPublication(publication)
	if err != nil {
		return nil, err
	}
	return r.correct(ctx, id, correction, owned, digest)
}

// correctionPublication detaches and orders the owner's prepared record changes.
func correctionPublication(publication []storage.CompareAndSwapMutation) ([]storage.CompareAndSwapMutation, string, error) {
	if len(publication) == 0 || len(publication) > maxCorrectionPublicationRecords {
		return nil, "", ErrInvalid
	}
	owned := make([]storage.CompareAndSwapMutation, len(publication))
	for i, mutation := range publication {
		if strings.TrimSpace(mutation.Key) == "" || len(mutation.Key) > maxCorrectionPublicationKeyBytes || strings.HasPrefix(mutation.Key, "budget:v1:") || mutation.TTL != 0 || len(mutation.NewValue) == 0 || len(mutation.NewValue) > maxRecordSize || len(mutation.ExpectedValue) > maxRecordSize || (mutation.ExpectedValue != nil && len(mutation.ExpectedValue) == 0) {
			return nil, "", ErrInvalid
		}
		owned[i] = storage.CompareAndSwapMutation{Key: mutation.Key, ExpectedValue: bytes.Clone(mutation.ExpectedValue), NewValue: bytes.Clone(mutation.NewValue)}
	}
	slices.SortFunc(owned, func(a, b storage.CompareAndSwapMutation) int { return cmp.Compare(a.Key, b.Key) })
	for i := 1; i < len(owned); i++ {
		if owned[i-1].Key == owned[i].Key {
			return nil, "", ErrInvalid
		}
	}
	type record struct {
		Key      string `json:"key"`
		Absent   bool   `json:"absent"`
		Expected []byte `json:"expected"`
		Value    []byte `json:"value"`
	}
	records := make([]record, len(owned))
	for i, mutation := range owned {
		records[i] = record{Key: mutation.Key, Absent: mutation.ExpectedValue == nil, Expected: mutation.ExpectedValue, Value: mutation.NewValue}
	}
	encoded, err := json.Marshal(records, json.Deterministic(true))
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	return owned, hex.EncodeToString(digest[:]), nil
}

func validPublicationDigest(digest string) bool {
	if digest == "" {
		return true
	}
	decoded, err := hex.DecodeString(digest)
	return err == nil && len(decoded) == sha256.Size && digest == strings.ToLower(digest)
}

func (r *Repository) checkCorrectionPublication(ctx context.Context, publication []storage.CompareAndSwapMutation) error {
	for _, mutation := range publication {
		value, lifetime, err := r.store.ReadWithLifetime(ctx, mutation.Key, max(1, len(mutation.ExpectedValue)))
		if errors.Is(err, storage.ErrNotFound) {
			if mutation.ExpectedValue == nil {
				continue
			}
			return storage.ErrConflict
		}
		if errors.Is(err, storage.ErrValueTooLarge) {
			return storage.ErrConflict
		}
		if err != nil {
			return err
		}
		if mutation.ExpectedValue == nil || lifetime != 0 || !bytes.Equal(value, mutation.ExpectedValue) {
			return storage.ErrConflict
		}
	}
	return nil
}
