package usage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/agentstation/starport/internal/storage"
)

const adjustmentSchemaVersion = 1
const maxAdjustmentBytes = 64 << 10

// Adjustment preserves the original usage and one corrected billing decision.
// ID and PreviousID order decisions. RecordedAt comes from the durable job audit.
// Private operator evidence belongs to that audit, not to usage reporting.
type Adjustment struct {
	ID                 string    `json:"id"`
	PreviousID         string    `json:"previous_id,omitempty"`
	Original           Record    `json:"original"`
	RecordedAt         time.Time `json:"recorded_at"`
	Cost               Cost      `json:"cost"`
	Tokens             int64     `json:"tokens"`
	BillingDisposition string    `json:"billing_disposition"`
}

// BillingAdjustment identifies the revised billing fields in an activity result.
// Provider measurements remain in the original record and its media fields.
type BillingAdjustment struct {
	OriginalTokensUnknown         bool      `json:"original_tokens_unknown,omitempty"`
	OriginalTokensEstimated       bool      `json:"original_tokens_estimated,omitempty"`
	ID                            string    `json:"id"`
	PreviousID                    string    `json:"previous_id,omitempty"`
	RecordedAt                    time.Time `json:"recorded_at"`
	OriginalCost                  *Cost     `json:"original_cost,omitempty"`
	OriginalCostUnavailableReason string    `json:"original_cost_unavailable_reason,omitempty"`
	OriginalTokens                Tokens    `json:"original_tokens"`
	OriginalBillingDisposition    string    `json:"original_billing_disposition,omitempty"`
}

// AdjustmentWriter changes reporting totals without changing required budgets.
// Exact retries preserve the accepted receipt and never add another request.
type AdjustmentWriter interface {
	Adjust(context.Context, Adjustment) error
}

// AdjustmentReader reads a correction within its original usage identity.
type AdjustmentReader interface {
	InspectAdjustment(ctx context.Context, original Record, id string) (Adjustment, error)
}

type adjustmentRecord struct {
	Version    int        `json:"version"`
	Adjustment Adjustment `json:"adjustment"`
}

func adjustmentID(value string) bool {
	return strings.TrimSpace(value) != "" && len(value) <= 256 && strings.IndexFunc(value, func(r rune) bool { return r < 32 || r == 127 }) < 0
}

// Validate checks billing units, identity, and the original reporting record.
func (a Adjustment) Validate() error {
	if !adjustmentID(a.ID) || (a.PreviousID != "" && (!adjustmentID(a.PreviousID) || a.PreviousID == a.ID)) || a.RecordedAt.IsZero() || a.RecordedAt.Before(a.Original.Timestamp) {
		return ErrInvalidRecord
	}
	if err := a.Original.Validate(); err != nil {
		return err
	}
	if a.Original.BillingAdjustment != nil || a.Original.Operation != OperationVideos || a.Original.ExtractionCost != nil || len(a.Original.Extractions) != 0 || a.Original.Tokens.Total < 0 || a.Cost.NanoUSD < 0 || a.Cost.Currency != "USD" || a.Tokens < 0 {
		return ErrInvalidRecord
	}
	if a.Original.Cost != nil && (a.Original.Cost.NanoUSD < 0 || a.Original.Cost.Currency != "USD") {
		return ErrInvalidRecord
	}
	switch a.BillingDisposition {
	case "administrator_no_charge":
		if a.Cost.NanoUSD != 0 || a.Tokens != 0 {
			return ErrInvalidRecord
		}
	case "administrator_usage":
	default:
		return ErrInvalidRecord
	}
	return nil
}

func adjustmentHeadKey(key string) string {
	digest := sha256.Sum256([]byte(key))
	return StoragePrefix + "adjustment-head:" + hex.EncodeToString(digest[:])
}

func adjustmentReceiptKey(key, id string) string {
	digest := sha256.Sum256([]byte(key))
	return StoragePrefix + "adjustment:" + hex.EncodeToString(digest[:]) + ":" + encodeSegment(id)
}

func encodeAdjustment(a Adjustment) ([]byte, error) {
	if err := a.Validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(adjustmentRecord{Version: adjustmentSchemaVersion, Adjustment: a}, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	if len(data) > maxAdjustmentBytes {
		return nil, ErrInvalidRecord
	}
	return data, nil
}

func decodeAdjustment(data []byte) (Adjustment, error) {
	var stored adjustmentRecord
	if len(data) == 0 || len(data) > maxAdjustmentBytes || json.Unmarshal(data, &stored) != nil || stored.Version != adjustmentSchemaVersion || stored.Adjustment.Validate() != nil {
		return Adjustment{}, ErrCorruptRecord
	}
	return stored.Adjustment, nil
}

// InspectAdjustment reads retained reporting evidence without changing any totals.
func (r *repository) InspectAdjustment(ctx context.Context, original Record, id string) (Adjustment, error) {
	if !adjustmentID(id) || original.Validate() != nil || original.BillingAdjustment != nil {
		return Adjustment{}, ErrInvalidRecord
	}
	key := recordKey(original.KeyID, original.Timestamp, original.RequestID)
	data, err := r.store.Get(ctx, adjustmentReceiptKey(key, id))
	if err != nil {
		return Adjustment{}, err
	}
	a, err := decodeAdjustment(data)
	if err != nil {
		return Adjustment{}, err
	}
	expected, err := encodeRecord(original)
	if err != nil {
		return Adjustment{}, err
	}
	actual, err := encodeRecord(a.Original)
	if err != nil || a.ID != id || !bytes.Equal(expected, actual) {
		return Adjustment{}, ErrCorruptRecord
	}
	return a, nil
}

// Adjust stores a receipt and the latest decision with all affected reporting totals.
// The original usage bytes and request counts remain unchanged. Retention uses the
// original event deadline, so replay cannot recreate expired reporting windows.
func (r *repository) Adjust(ctx context.Context, a Adjustment) error {
	data, err := encodeAdjustment(a)
	if err != nil {
		return err
	}
	original, err := encodeRecord(a.Original)
	if err != nil {
		return err
	}
	key := recordKey(a.Original.KeyID, a.Original.Timestamp, a.Original.RequestID)
	headKey, receiptKey := adjustmentHeadKey(key), adjustmentReceiptKey(key, a.ID)
	// Include zero counters so a no-charge record can later receive measured usage.
	all := a.Original
	all.Tokens.Total, all.Cost = 1, &Cost{NanoUSD: 1, Currency: "USD"}
	changes := r.counterChanges(all)
	keys := []string{key, headKey, receiptKey}
	for _, change := range changes {
		keys = append(keys, change.key)
	}
	expires := a.Original.Timestamp.Add(r.retention).Truncate(time.Second)
	for range maxCommitConflicts {
		if err := ctx.Err(); err != nil {
			return err
		}
		values, err := r.store.BatchGet(ctx, keys)
		if err != nil {
			return err
		}
		if previous, exists := values[receiptKey]; exists {
			if bytes.Equal(previous, data) {
				return nil
			}
			return ErrRecordConflict
		}
		now := time.Now()
		if !now.Before(expires) {
			return ErrRecordExpired
		}
		if !bytes.Equal(values[key], original) {
			return ErrRecordConflict
		}
		oldTokens, oldSpend := a.Original.Tokens.Total, a.Original.knownSpendNanoUSD()
		if previous, exists := values[headKey]; exists {
			head, err := decodeAdjustment(previous)
			if err != nil {
				return err
			}
			encodedOriginal, err := encodeRecord(head.Original)
			if err != nil || !bytes.Equal(encodedOriginal, original) {
				return ErrCorruptRecord
			}
			if head.ID != a.PreviousID {
				return ErrRecordConflict
			}
			oldTokens, oldSpend = head.Tokens, head.Cost.NanoUSD
		} else if a.PreviousID != "" {
			return ErrRecordConflict
		}
		ttl := expires.Sub(now).Round(time.Millisecond)
		if ttl < expires.Sub(now) {
			ttl += time.Millisecond
		}
		writes := []storage.CompareAndSwapMutation{
			{Key: key, ExpectedValue: values[key], NewValue: values[key]},
			{Key: headKey, ExpectedValue: values[headKey], NewValue: data, TTL: ttl},
			{Key: receiptKey, NewValue: data, TTL: ttl},
		}
		for _, change := range changes {
			before, after := int64(1), int64(1)
			switch change.counter {
			case counterTokens:
				before, after = oldTokens, a.Tokens
			case counterSpend:
				before, after = oldSpend, a.Cost.NanoUSD
			}
			previous, exists := values[change.key]
			total := int64(0)
			if exists {
				total, err = strconv.ParseInt(string(previous), 10, 64)
				if err != nil || total < 0 {
					return ErrCorruptRecord
				}
			}
			if total < before {
				return ErrCorruptRecord
			}
			remaining := total - before
			if after > math.MaxInt64-remaining {
				return ErrInvalidRecord
			}
			mutation := storage.CompareAndSwapMutation{Key: change.key, ExpectedValue: previous, NewValue: []byte(strconv.FormatInt(remaining+after, 10))}
			if !exists {
				mutation.TTL = change.expires.Sub(now)
			}
			writes = append(writes, mutation)
		}
		if err := r.store.CompareAndSwapBatch(ctx, writes); !errors.Is(err, storage.ErrConflict) {
			return err
		}
	}
	return storage.ErrConflict
}

func applyBillingAdjustment(record Record, data []byte) (Record, error) {
	if len(data) == 0 {
		return record, nil
	}
	a, err := decodeAdjustment(data)
	if err != nil {
		return Record{}, err
	}
	original, err := encodeRecord(record)
	if err != nil {
		return Record{}, err
	}
	expected, err := encodeRecord(a.Original)
	if err != nil || !bytes.Equal(original, expected) {
		return Record{}, ErrCorruptRecord
	}
	record.BillingAdjustment = &BillingAdjustment{ID: a.ID, PreviousID: a.PreviousID, RecordedAt: a.RecordedAt, OriginalCost: record.Cost, OriginalCostUnavailableReason: record.CostUnavailableReason, OriginalTokens: record.Tokens, OriginalTokensUnknown: record.TokensUnknown, OriginalTokensEstimated: record.TokensEstimated, OriginalBillingDisposition: record.BillingDisposition}
	record.Cost, record.CostUnavailableReason = &a.Cost, ""
	record.Tokens = Tokens{Total: a.Tokens}
	record.TokensEstimated, record.TokensUnknown = false, false
	record.BillingDisposition = a.BillingDisposition
	return record, nil
}
