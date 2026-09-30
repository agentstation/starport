package blob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"io"
	"strings"
)

const (
	blobActivationCurrent = ".starport/activation-current"
	blobActivationPrefix  = ".starport/activation-"
	blobActivationLimit   = 256
)

// ErrActivationConflict preserves an import or a different activation decision.
var ErrActivationConflict = errors.New("blob: activation conflicts with retained ownership")

// ImportActivator releases an exact import after the recovery coordinator checks
// fencing, history, and cross-store readiness. Activation alone grants no admission.
// The operation must be the complete bundle-bound operation used during restore.
type ImportActivator interface {
	ActivateImport(context.Context, string, Snapshot, string) error
}

type blobActivationReceipt struct {
	Version        int    `json:"version"`
	ClaimSHA256    string `json:"claim_sha256"`
	DecisionSHA256 string `json:"decision_sha256"`
	Phase          string `json:"phase,omitempty"`
}

func activationDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func makeActivation(claim []byte, decision string) (string, []byte, []byte, []byte, error) {
	if !activationDigest(decision) {
		return "", nil, nil, nil, ErrActivationConflict
	}
	digest := sha256.Sum256(claim)
	r := blobActivationReceipt{Version: 1, ClaimSHA256: hex.EncodeToString(digest[:]), DecisionSHA256: decision}
	history, _ := json.Marshal(r)
	r.Phase = "preparing"
	pending, _ := json.Marshal(r)
	r.Phase = "active"
	active, _ := json.Marshal(r)
	return blobActivationPrefix + r.ClaimSHA256, history, pending, active, nil
}

func validActivationAddress(address string) bool {
	digest, ok := strings.CutPrefix(address, blobActivationPrefix)
	return ok && activationDigest(digest)
}

func inspectActivationHistory(address string, size int64, input io.Reader) (io.Reader, bool, error) {
	if !validActivationAddress(address) || size < 1 || size > blobActivationLimit {
		return nil, false, ErrActivationConflict
	}
	data, err := io.ReadAll(io.LimitReader(input, size+1))
	if err != nil {
		return nil, false, err
	}
	var r blobActivationReceipt
	if int64(len(data)) != size || json.Unmarshal(data, &r) != nil || r.Version != 1 || r.Phase != "" ||
		!activationDigest(r.ClaimSHA256) || !activationDigest(r.DecisionSHA256) || address != blobActivationPrefix+r.ClaimSHA256 {
		return nil, false, ErrActivationConflict
	}
	canonical, err := json.Marshal(r)
	if err != nil || !bytes.Equal(data, canonical) {
		return nil, false, ErrActivationConflict
	}
	return bytes.NewReader(data), false, nil
}

// activationRecords owns only recovery control records. Missing reads return
// ErrNotFound. Mutations compare complete prior bytes and preserve conflicts.
type activationRecords interface {
	read(context.Context, string) ([]byte, error)
	compare(context.Context, string, []byte, []byte) error
	prepare(context.Context) error
	confirm(context.Context) error
}

func activateBlobImport(ctx context.Context, records activationRecords, claim []byte, decision string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	key, history, pending, active, err := makeActivation(claim, decision)
	if err != nil {
		return err
	}
	current, currentErr := records.read(ctx, blobActivationCurrent)
	if currentErr != nil && !errors.Is(currentErr, ErrNotFound) {
		return currentErr
	}
	barrier, err := records.read(ctx, blobImportKey)
	if err != nil {
		return errors.Join(ErrActivationConflict, err)
	}
	if bytes.Equal(current, active) {
		if !bytes.Equal(barrier, active) {
			return ErrActivationConflict
		}
		return verifyBlobActivation(ctx, records, key, history, active)
	}
	if bytes.Equal(barrier, active) {
		if !bytes.Equal(current, pending) {
			return ErrActivationConflict
		}
		recorded, err := records.read(ctx, key)
		if err != nil || !bytes.Equal(recorded, history) {
			return errors.Join(ErrActivationConflict, err)
		}
	} else {
		if !bytes.Equal(barrier, claim) {
			return ErrActivationConflict
		}
		if errors.Is(currentErr, ErrNotFound) {
			_, err := records.read(ctx, key)
			if !errors.Is(err, ErrNotFound) {
				return errors.Join(ErrActivationConflict, err)
			}
			if err := records.prepare(ctx); err != nil {
				return err
			}
			if err := records.compare(ctx, blobActivationCurrent, nil, pending); err != nil {
				return err
			}
		} else if !bytes.Equal(current, pending) {
			return ErrActivationConflict
		}
		if err := records.compare(ctx, key, nil, history); err != nil {
			return err
		}
		if err := records.prepare(ctx); err != nil {
			return err
		}
		// Replacing the barrier preserves a conditional-write contract on services
		// that do not implement conditional deletion. No object is deleted here.
		if err := records.compare(ctx, blobImportKey, claim, active); err != nil {
			return err
		}
	}
	if err := records.compare(ctx, blobActivationCurrent, pending, active); err != nil {
		return err
	}
	return verifyBlobActivation(ctx, records, key, history, active)
}

func verifyBlobActivation(ctx context.Context, records activationRecords, key string, history, active []byte) error {
	for name, expected := range map[string][]byte{key: history, blobImportKey: active, blobActivationCurrent: active} {
		current, err := records.read(ctx, name)
		if err != nil || !bytes.Equal(current, expected) {
			return errors.Join(ErrActivationConflict, err)
		}
	}
	if err := checkAdoptionClosure(ctx, records); err != nil {
		return errors.Join(ErrActivationConflict, err)
	}
	return records.confirm(ctx)
}

// activeReceipt parses a canonical receipt of a completed native activation.
func activeReceipt(value []byte) (blobActivationReceipt, bool) {
	var receipt blobActivationReceipt
	if len(value) > blobActivationLimit || json.Unmarshal(value, &receipt) != nil || receipt.Version != 1 || receipt.Phase != "active" ||
		!activationDigest(receipt.ClaimSHA256) || !activationDigest(receipt.DecisionSHA256) {
		return receipt, false
	}
	canonical, _ := json.Marshal(receipt)
	return receipt, bytes.Equal(value, canonical)
}

// checkActivationBarrier permits only an ordinary store or a completed native
// activation. Portable historical receipts never establish current authority.
// A pending populated claim closure refuses both states.
func checkActivationBarrier(ctx context.Context, records activationRecords) error {
	if err := checkAdoptionClosure(ctx, records); err != nil {
		return err
	}
	barrier, err := records.read(ctx, blobImportKey)
	if errors.Is(err, ErrNotFound) {
		_, err := records.read(ctx, blobActivationCurrent)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return errors.Join(ErrImportRestricted, err)
	}
	if err != nil {
		return errors.Join(ErrImportRestricted, err)
	}
	receipt, ok := activeReceipt(barrier)
	if !ok {
		return ErrImportRestricted
	}
	current, err := records.read(ctx, blobActivationCurrent)
	if err != nil || !bytes.Equal(current, barrier) {
		return errors.Join(ErrImportRestricted, err)
	}
	receipt.Phase = ""
	history, _ := json.Marshal(receipt)
	recorded, err := records.read(ctx, blobActivationPrefix+receipt.ClaimSHA256)
	if err != nil || !bytes.Equal(recorded, history) {
		return errors.Join(ErrImportRestricted, err)
	}
	return ctx.Err()
}
