package blob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/agentstation/starmap/pkg/productfiles"
)

const (
	replayPreparing    = "preparing"
	replayComplete     = "complete"
	publicationAbsent  = "absent"
	publicationLive    = "live"
	publicationRetired = "retired"
)

const blobReplayCurrent = ".starport/replay-current"
const blobReplayPrefix = ".starport/replay-"

// ImportPublicationMaxBytes bounds one independently retained recovery asset.
const ImportPublicationMaxBytes int64 = 1 << 30

// ImportReplayPosition identifies the exact completed local blob replay chain.
type ImportReplayPosition struct {
	Sequence      int64  `json:"sequence"`
	ReceiptSHA256 string `json:"receipt_sha256"`
}

// ImportPublicationStep binds one retained publication or retirement to independent evidence.
type ImportPublicationStep struct {
	Sequence       int64            `json:"sequence"`
	PreviousSHA256 string           `json:"previous_sha256"`
	EvidenceSHA256 string           `json:"evidence_sha256"`
	Key            string           `json:"key"`
	Expected       PublicationState `json:"expected"`
	Next           PublicationState `json:"next"`
}

// ImportPublicationReplayer changes retained bytes only under an exact import barrier.
// Every writer must remain fenced. A failed call can leave durable pending work.
type ImportPublicationReplayer interface {
	ReplayPublication(context.Context, string, Snapshot, ImportPublicationStep, io.Reader, string) (string, error)
}

// ImportReplayInspector binds captures to the exact completed blob replay cursor.
type ImportReplayInspector interface {
	CheckImportPosition(context.Context, string, Snapshot, ImportReplayPosition) error
	SnapshotImportAt(context.Context, string, string, Snapshot, ImportReplayPosition) (Snapshot, error)
}

// ImportReplayActivator releases only the exact inspected completed blob chain.
type ImportReplayActivator interface {
	ActivateImportAt(context.Context, string, Snapshot, ImportReplayPosition, string) error
}

type blobReplayReceipt struct {
	Version        int    `json:"version"`
	ClaimSHA256    string `json:"claim_sha256"`
	Sequence       int64  `json:"sequence"`
	PreviousSHA256 string `json:"previous_sha256"`
	EvidenceSHA256 string `json:"evidence_sha256"`
	MutationSHA256 string `json:"mutation_sha256"`
}
type blobReplayCursor struct {
	Receipt blobReplayReceipt `json:"receipt"`
	Phase   string            `json:"phase"`
}

func blobDigest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func (r blobReplayReceipt) key() string {
	return blobReplayPrefix + blobDigest(fmt.Appendf(nil, "%s:%d", r.ClaimSHA256, r.Sequence))
}
func (r blobReplayReceipt) valid() bool {
	return r.Version == 1 && activationDigest(r.ClaimSHA256) && r.Sequence > 0 && (r.Sequence == 1 && r.PreviousSHA256 == "" || r.Sequence > 1 && activationDigest(r.PreviousSHA256)) && activationDigest(r.EvidenceSHA256) && activationDigest(r.MutationSHA256)
}
func validPublicationState(s PublicationState) bool {
	switch s.Kind {
	case publicationAbsent, publicationRetired:
		return s.Size == 0 && s.SHA256 == ""
	case publicationLive:
		return s.Size >= 0 && s.Size <= ImportPublicationMaxBytes && activationDigest(s.SHA256)
	}
	return false
}
func validPublicationTransition(key string, expected, next PublicationState) bool {
	return ValidateKey(key) == nil && validPublicationState(expected) && validPublicationState(next) && next.Kind != publicationAbsent &&
		(expected.Kind != publicationRetired || next.Kind == publicationRetired) &&
		(expected.Kind != publicationLive || next.Kind != publicationLive || expected == next)
}

func makeReplayReceipt(claim []byte, step ImportPublicationStep) (blobReplayReceipt, []byte, error) {
	if !validPublicationTransition(step.Key, step.Expected, step.Next) {
		return blobReplayReceipt{}, nil, ErrPublicationExists
	}
	mutation, err := json.Marshal(struct {
		Key            string
		Expected, Next PublicationState
	}{step.Key, step.Expected, step.Next})
	if err != nil {
		return blobReplayReceipt{}, nil, err
	}
	receipt := blobReplayReceipt{1, blobDigest(claim), step.Sequence, step.PreviousSHA256, step.EvidenceSHA256, blobDigest(mutation)}
	if !receipt.valid() {
		return receipt, nil, ErrActivationConflict
	}
	data, err := json.Marshal(receipt)
	return receipt, data, err
}
func readReplayCursor(ctx context.Context, records activationRecords, claim []byte) (blobReplayCursor, []byte, error) {
	data, err := records.read(ctx, blobReplayCurrent)
	if errors.Is(err, ErrNotFound) {
		return blobReplayCursor{}, nil, nil
	}
	if err != nil {
		return blobReplayCursor{}, nil, err
	}
	var current blobReplayCursor
	if json.Unmarshal(data, &current, json.RejectUnknownMembers(true)) != nil || !current.Receipt.valid() || current.Receipt.ClaimSHA256 != blobDigest(claim) || current.Phase != replayPreparing && current.Phase != replayComplete {
		return current, nil, ErrActivationConflict
	}
	canonical, _ := json.Marshal(current)
	if !bytes.Equal(canonical, data) {
		return current, nil, ErrActivationConflict
	}
	if current.Phase == replayComplete {
		receipt, err := records.read(ctx, current.Receipt.key())
		expected, _ := json.Marshal(current.Receipt)
		if err != nil || !bytes.Equal(receipt, expected) {
			return current, nil, errors.Join(ErrActivationConflict, err)
		}
	}
	return current, data, nil
}
func checkReplayPosition(ctx context.Context, records activationRecords, claim []byte, expected ImportReplayPosition) error {
	cursor, _, err := readReplayCursor(ctx, records, claim)
	if err != nil {
		return err
	}
	if expected.Sequence == 0 && expected.ReceiptSHA256 == "" && cursor.Phase == "" {
		return nil
	}
	body, _ := json.Marshal(cursor.Receipt)
	if expected.Sequence <= 0 || !activationDigest(expected.ReceiptSHA256) || cursor.Phase != replayComplete || cursor.Receipt.Sequence != expected.Sequence || blobDigest(body) != expected.ReceiptSHA256 {
		return ErrActivationConflict
	}
	return nil
}

type replayPublicationOwner interface {
	RecoveryPublicationReader
	replayWrite(context.Context, string, PublicationState, PublicationState, io.Reader) error
	replayConfirm(context.Context, string) error
}

func replayPublication(ctx context.Context, records activationRecords, owner replayPublicationOwner, operation string, original Snapshot, step ImportPublicationStep, input io.Reader, scratch string) (string, error) {
	if ctx == nil {
		return "", ErrImportRestricted
	}
	claim, err := makeBlobClaim(operation, original)
	if err != nil {
		return "", err
	}
	receipt, body, err := makeReplayReceipt(claim, step)
	if err != nil {
		return "", err
	}
	guard := func() error { return checkBlobImport(ctx, records, operation, original) }
	if err := guard(); err != nil {
		return "", err
	}
	cursor, prior, err := readReplayCursor(ctx, records, claim)
	if err != nil {
		return "", err
	}
	old, oldErr := records.read(ctx, receipt.key())
	if oldErr == nil {
		if !bytes.Equal(old, body) || cursor.Receipt.Sequence < receipt.Sequence {
			return "", ErrActivationConflict
		}
		if cursor.Phase == replayComplete {
			if err := guard(); err != nil {
				return "", err
			}
			return blobDigest(body), nil
		}
	}
	if oldErr != nil && !errors.Is(oldErr, ErrNotFound) {
		return "", oldErr
	}
	pending := blobReplayCursor{Receipt: receipt, Phase: replayPreparing}
	pendingBytes, _ := json.Marshal(pending)
	if err := checkReplaySuccessor(cursor, prior, receipt, pendingBytes); err != nil {
		return "", err
	}
	if cursor.Phase != replayPreparing {
		actual, err := owner.InspectPublication(ctx, step.Key)
		if err != nil || actual != step.Expected {
			return "", errors.Join(ErrPublicationExists, err)
		}
	}
	// Durable private staging verifies independent bytes before the cursor or retained identity changes.
	staged, cleanup, err := stageReplayPayload(ctx, input, step.Next, scratch)
	if err != nil {
		return "", err
	}
	defer cleanup()
	if cursor.Phase != replayPreparing {
		if err := records.compare(ctx, blobReplayCurrent, prior, pendingBytes); err != nil {
			return "", err
		}
	}
	if err := guard(); err != nil {
		return "", err
	}
	if err := replayRetainedBytes(ctx, owner, step, staged); err != nil {
		return "", err
	}
	if err := guard(); err != nil {
		return "", err
	}
	if err := owner.replayConfirm(ctx, step.Key); err != nil {
		return "", err
	}
	if err := records.compare(ctx, receipt.key(), nil, body); err != nil {
		return "", err
	}
	complete, _ := json.Marshal(blobReplayCursor{Receipt: receipt, Phase: replayComplete})
	if err := records.compare(ctx, blobReplayCurrent, pendingBytes, complete); err != nil {
		return "", err
	}
	if err := records.confirm(ctx); err != nil {
		return "", err
	}
	if err := guard(); err != nil {
		return "", err
	}
	return blobDigest(body), nil
}

func stageReplayPayload(ctx context.Context, input io.Reader, next PublicationState, scratch string) (*os.File, func(), error) {
	if next.Kind != publicationLive {
		if input != nil {
			return nil, nil, ErrCorruptPublication
		}
		return nil, func() {}, nil
	}
	if input == nil || !filepath.IsAbs(scratch) || filepath.Clean(scratch) != scratch {
		return nil, nil, ErrCorruptPublication
	}
	directory, err := productfiles.ExistingDirectory(scratch)
	if err != nil {
		return nil, nil, err
	}
	if _, err = directory.Identity(); err != nil {
		return nil, nil, err
	}
	file, err := os.CreateTemp(scratch, ".blob-replay-")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = file.Close(); _ = os.Remove(file.Name()) }
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(&contextReader{ctx: ctx, r: input}, next.Size+1))
	if err != nil || n != next.Size || hex.EncodeToString(hash.Sum(nil)) != next.SHA256 {
		cleanup()
		return nil, nil, errors.Join(ErrCorruptPublication, err)
	}
	if err = file.Sync(); err == nil {
		_, err = file.Seek(0, io.SeekStart)
	}
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return file, cleanup, nil
}

func validReplayAddress(address string) bool {
	digest, ok := strings.CutPrefix(address, blobReplayPrefix)
	return ok && activationDigest(digest)
}
func inspectReplayHistory(address string, size int64, input io.Reader) (io.Reader, bool, error) {
	if size <= 0 || size > 4096 {
		return nil, false, ErrActivationConflict
	}
	data, err := io.ReadAll(io.LimitReader(input, size+1))
	if err != nil {
		return nil, false, err
	}
	var receipt blobReplayReceipt
	if int64(len(data)) != size || json.Unmarshal(data, &receipt, json.RejectUnknownMembers(true)) != nil || !receipt.valid() || receipt.key() != address {
		return nil, false, ErrActivationConflict
	}
	canonical, _ := json.Marshal(receipt)
	if !bytes.Equal(data, canonical) {
		return nil, false, ErrActivationConflict
	}
	return bytes.NewReader(data), false, nil
}

func replayRetainedBytes(ctx context.Context, owner replayPublicationOwner, step ImportPublicationStep, staged io.Reader) error {
	actual, err := owner.InspectPublication(ctx, step.Key)
	if err != nil {
		return err
	}
	if actual != step.Next {
		if actual != step.Expected {
			return ErrPublicationExists
		}
		if err := owner.replayWrite(ctx, step.Key, step.Expected, step.Next, staged); err != nil {
			return err
		}
	}
	actual, err = owner.InspectPublication(ctx, step.Key)
	if err != nil || actual != step.Next {
		return errors.Join(ErrPublicationExists, err)
	}
	return nil
}

func checkReplaySuccessor(cursor blobReplayCursor, prior []byte, receipt blobReplayReceipt, pendingBytes []byte) error {
	if cursor.Phase == replayPreparing {
		if !bytes.Equal(prior, pendingBytes) {
			return ErrActivationConflict
		}
	} else {
		previousBody, _ := json.Marshal(cursor.Receipt)
		if cursor.Phase == "" && receipt.Sequence != 1 || cursor.Phase != "" && (receipt.Sequence != cursor.Receipt.Sequence+1 || receipt.PreviousSHA256 != blobDigest(previousBody)) {
			return ErrActivationConflict
		}
	}
	return nil
}
