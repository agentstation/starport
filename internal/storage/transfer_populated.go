package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"hash"
	"strings"
)

const (
	// A populated claim retains this persistent receipt after it closes the earlier native epoch.
	transferPopulatedPrefix = "storage:populated:v1:"
	importControlMaxBytes   = 4096
	populatedCensusDomain   = "starport-populated-census-v1\x00"
)

// ErrPopulatedImportUnsupported refuses a populated claim on a backend without an atomic native claim.
var ErrPopulatedImportUnsupported = errors.New("populated import claim requires Valkey storage")

// ImportControls holds the exact current native import roots of an unclaimed target.
// A present root holds its exact persistent bytes. An absent root holds no bytes.
type ImportControls struct {
	ActivationPresent     bool
	Activation            []byte
	ReconciliationPresent bool
	Reconciliation        []byte
}

// CapturedRecordSource reads an independent immutable capture of a fenced target.
// Enumerate must visit each record once in strictly ascending key order.
// ReadCaptured returns the recorded expiry and does not apply current time.
type CapturedRecordSource interface {
	RecordSource
	ReadCaptured(context.Context, string, int) (TransferRecord, error)
}

// PopulatedImportClaimer places an import barrier on a populated target and copies no records.
// The caller must fence all writers from the control observation through activation.
// The caller retains the observation independently. The claim grants no admission.
// After the claim, the shared import operations accept the claim as an ordinary claim.
type PopulatedImportClaimer interface {
	ObserveImportControls(context.Context) (ImportControls, error)
	ClaimPopulated(context.Context, []byte, ImportControls, CapturedRecordSource) error
}

type populatedClaimReceipt struct {
	Version              int    `json:"version"`
	ClaimSHA256          string `json:"claim_sha256"`
	ActivationSHA256     string `json:"activation_sha256"`
	ReconciliationSHA256 string `json:"reconciliation_sha256"`
	CensusSHA256         string `json:"census_sha256"`
}

func (c ImportControls) validate() error {
	if !c.ActivationPresent && len(c.Activation) != 0 || !c.ReconciliationPresent && len(c.Reconciliation) != 0 ||
		len(c.Activation) > importControlMaxBytes || len(c.Reconciliation) > importControlMaxBytes {
		return ErrInvalidMutation
	}
	return nil
}

func (c ImportControls) equal(other ImportControls) bool {
	return c.ActivationPresent == other.ActivationPresent && bytes.Equal(c.Activation, other.Activation) &&
		c.ReconciliationPresent == other.ReconciliationPresent && bytes.Equal(c.Reconciliation, other.Reconciliation)
}

// guards returns the exact root preimages. A nil value means absence.
func (c ImportControls) guards() []CompareAndSwapMutation {
	expected := func(present bool, value []byte) []byte {
		if !present {
			return nil
		}
		return append([]byte{}, value...)
	}
	return []CompareAndSwapMutation{
		{Key: transferActivationCurrent, ExpectedValue: expected(c.ActivationPresent, c.Activation)},
		{Key: transferReconciliationCurrent, ExpectedValue: expected(c.ReconciliationPresent, c.Reconciliation)},
	}
}

func importControlDigest(present bool, value []byte) string {
	if !present {
		return ""
	}
	return reconciliationDigest(value)
}

func populatedClaimReceiptAt(claim []byte, controls ImportControls, census string) (string, []byte, error) {
	if err := validateTransferClaim(claim); err != nil {
		return "", nil, err
	}
	if err := controls.validate(); err != nil {
		return "", nil, err
	}
	if !transferDigest(census) {
		return "", nil, ErrInvalidMutation
	}
	receipt := populatedClaimReceipt{
		Version:              1,
		ClaimSHA256:          reconciliationDigest(claim),
		ActivationSHA256:     importControlDigest(controls.ActivationPresent, controls.Activation),
		ReconciliationSHA256: importControlDigest(controls.ReconciliationPresent, controls.Reconciliation),
		CensusSHA256:         census,
	}
	encoded, err := json.Marshal(receipt)
	return transferPopulatedPrefix + receipt.ClaimSHA256, encoded, err
}

func validatePopulatedHistory(record TransferRecord) error {
	if !strings.HasPrefix(record.Key, transferPopulatedPrefix) {
		return nil
	}
	var receipt populatedClaimReceipt
	optional := func(value string) bool { return value == "" || transferDigest(value) }
	if record.ExpiresAtMillis != 0 || len(record.Value) > 512 || json.Unmarshal(record.Value, &receipt) != nil ||
		receipt.Version != 1 || !transferDigest(receipt.ClaimSHA256) || !optional(receipt.ActivationSHA256) ||
		!optional(receipt.ReconciliationSHA256) || !transferDigest(receipt.CensusSHA256) ||
		record.Key != transferPopulatedPrefix+receipt.ClaimSHA256 {
		return ErrInvalidMutation
	}
	canonical, err := json.Marshal(receipt)
	if err != nil || !bytes.Equal(canonical, record.Value) {
		return ErrInvalidMutation
	}
	return nil
}

// populatedCensus digests captured records in their required strict key order.
type populatedCensus struct {
	digest  hash.Hash
	started bool
	last    string
}

func newPopulatedCensus() *populatedCensus {
	digest := sha256.New()
	digest.Write([]byte(populatedCensusDomain))
	return &populatedCensus{digest: digest}
}

func (c *populatedCensus) add(record TransferRecord) error {
	if err := record.Validate(); err != nil {
		return err
	}
	if c.started && record.Key <= c.last {
		return ErrInvalidMutation
	}
	c.started, c.last = true, record.Key
	frame := binary.BigEndian.AppendUint64(nil, uint64(len(record.Key)))
	frame = append(frame, record.Key...)
	frame = binary.BigEndian.AppendUint64(frame, uint64(len(record.Value)))
	c.digest.Write(frame)
	c.digest.Write(record.Value)
	c.digest.Write(binary.BigEndian.AppendUint64(nil, uint64(record.ExpiresAtMillis))) // #nosec G115 -- Validate bounds expiry to a nonnegative value.
	return nil
}

func (c *populatedCensus) sum() string {
	return hex.EncodeToString(c.digest.Sum(nil))
}
