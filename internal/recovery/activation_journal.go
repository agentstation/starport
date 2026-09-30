package recovery

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/agentstation/starmap/pkg/productfiles"
)

// ActivationDecisionMaxBytes bounds the complete private application decision.
const ActivationDecisionMaxBytes = 32 << 20
const activationDecisionFile = "decision.json"

// ActivationPhase fixes the native release order. SQL remains closed through KV release.
type ActivationPhase string

// Native activation phases have one fixed release order.
const (
	ActivationBlobs ActivationPhase = "blobs"
	ActivationKV    ActivationPhase = "kv"
	ActivationSQL   ActivationPhase = "sql"
)

var activationPhases = []ActivationPhase{ActivationBlobs, ActivationKV, ActivationSQL}

type activationPhaseRecord struct {
	Version        int             `json:"version"`
	DecisionSHA256 string          `json:"decision_sha256"`
	Phase          ActivationPhase `json:"phase"`
	PreviousSHA256 string          `json:"previous_sha256"`
}

// ActivationJournal binds immutable application evidence and native completion phases.
// It grants no component activation or admission authority. The application checks each native receipt.
type ActivationJournal struct{ *activationJournalState }

type activationJournalState struct {
	directory  *productfiles.Directory
	path       string
	body       []byte
	digest     string
	phases     []activationPhaseRecord
	completing ActivationPhase
}

// Format omits private application evidence and native identities.
func (ActivationJournal) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<private activation journal>"))
}

// SealActivationJournal retains complete application evidence before the first native release.
// Exact retries retain the original bytes. The directory must already exist.
func SealActivationJournal(ctx context.Context, path string, body []byte) (*ActivationJournal, error) {
	if ctx == nil || len(body) == 0 || len(body) > ActivationDecisionMaxBytes {
		return nil, ErrConflict
	}
	var value jsontext.Value
	if json.Unmarshal(body, &value) != nil {
		return nil, ErrConflict
	}
	directory, err := productfiles.ExistingDirectory(path)
	if err != nil {
		return nil, err
	}
	if err := directory.RecoverPublications(ctx); err != nil {
		return nil, err
	}
	previous, err := directory.ReadFile(activationDecisionFile, ActivationDecisionMaxBytes)
	if errors.Is(err, os.ErrNotExist) {
		previous = nil
	} else if err != nil {
		return nil, err
	}
	if previous != nil && !bytes.Equal(previous, body) {
		return nil, ErrConflict
	}
	if err := directory.CompareAndPublish(ctx, activationDecisionFile, previous, body); err != nil {
		return nil, err
	}
	return InspectActivationJournal(ctx, path, historySHA256(body))
}

// InspectActivationJournal passively verifies original evidence and the complete phase prefix.
// It never recovers publication journals or invents missing phases.
func InspectActivationJournal(ctx context.Context, path, expected string) (*ActivationJournal, error) {
	return inspectActivationJournal(ctx, path, expected, "")
}

// InspectActivationPhaseCompletion checks one exact pending phase write without cleanup.
// It grants no native completion or current permission. The host must check native receipts separately.
func InspectActivationPhaseCompletion(ctx context.Context, path, expected string, phase ActivationPhase) (*ActivationJournal, error) {
	if activationPhaseIndex(phase) < 0 {
		return nil, ErrConflict
	}
	return inspectActivationJournal(ctx, path, expected, phase)
}

func inspectActivationJournal(ctx context.Context, path, expected string, completing ActivationPhase) (*ActivationJournal, error) {
	if ctx == nil || !historyDigest(expected) {
		return nil, ErrConflict
	}
	directory, err := productfiles.ExistingDirectory(path)
	if err != nil {
		return nil, err
	}
	if completing == "" {
		if err := directory.CheckNoPendingPublications(ctx); err != nil {
			return nil, err
		}
	}
	body, err := directory.ReadFile(activationDecisionFile, ActivationDecisionMaxBytes)
	if err != nil || len(body) == 0 || historySHA256(body) != expected {
		return nil, errors.Join(ErrConflict, err)
	}
	journal := &ActivationJournal{activationJournalState: &activationJournalState{directory: directory, path: path, body: body, digest: expected, completing: completing}}
	previous := expected
	missing := false
	for _, phase := range activationPhases {
		data, err := directory.ReadFile(string(phase)+".json", 4096)
		if errors.Is(err, os.ErrNotExist) {
			missing = true
			continue
		}
		if err != nil || missing {
			return nil, errors.Join(ErrConflict, err)
		}
		record := activationPhaseRecord{1, expected, phase, previous}
		canonical, _ := json.Marshal(record, json.Deterministic(true))
		if !bytes.Equal(canonical, data) {
			return nil, ErrConflict
		}
		journal.phases = append(journal.phases, record)
		previous = historySHA256(data)
	}
	allowedStages := map[string]bool{}
	if completing != "" {
		index := activationPhaseIndex(completing)
		if index > len(journal.phases) {
			return nil, ErrConflict
		}
		phaseBody, err := journal.phaseBody(completing)
		if err != nil {
			return nil, err
		}
		stages, err := directory.InspectPublication(ctx, string(completing)+".json", nil, phaseBody)
		if err != nil {
			return nil, err
		}
		for _, stage := range stages {
			allowedStages[stage] = true
		}
	}
	root, err := directory.Open()
	if err != nil {
		return nil, err
	}
	handle, err := root.Open(".")
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	limit := 8 + len(allowedStages)
	entries, readErr := handle.ReadDir(limit)
	if errors.Is(readErr, io.EOF) {
		readErr = nil
	}
	if len(entries) >= limit {
		readErr = ErrConflict
	}
	closeErr := errors.Join(handle.Close(), root.Close())
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, err
	}
	for _, entry := range entries {
		switch entry.Name() {
		case activationDecisionFile, "blobs.json", "kv.json", "sql.json", ".record-publications", "preparation.json", "operator-inputs.json":
		default:
			if !allowedStages[entry.Name()] {
				return nil, ErrConflict
			}
		}
	}
	return journal, nil
}

// Record returns a detached copy of the original sealed application decision.
func (j *ActivationJournal) Record() ([]byte, error) {
	if j == nil || j.activationJournalState == nil || j.directory == nil || len(j.body) == 0 || historySHA256(j.body) != j.digest {
		return nil, ErrConflict
	}
	return bytes.Clone(j.body), nil
}

// Digest identifies original sealed evidence. It is not a release capability.
func (j *ActivationJournal) Digest() string {
	if j == nil || j.activationJournalState == nil {
		return ""
	}
	return j.digest
}

// CompletedPhases reports the verified contiguous phase prefix.
func (j *ActivationJournal) CompletedPhases() int {
	if j == nil || j.activationJournalState == nil {
		return 0
	}
	return len(j.phases)
}

// CompletionPhase identifies the exact write selected by passive completion inspection.
// Its value grants no permission to release a native component.
func (j *ActivationJournal) CompletionPhase() ActivationPhase {
	if j == nil || j.activationJournalState == nil {
		return ""
	}
	return j.completing
}

// PublishPhase retains one original native completion after the application checks its receipt.
// It requires the next phase or an exact earlier retry. It cannot bypass a missing predecessor.
func (j *ActivationJournal) PublishPhase(ctx context.Context, phase ActivationPhase) error {
	return j.CompletePhase(ctx, phase, func(context.Context) error { return nil })
}

// CompletePhase finishes only the exact original phase write after the host checks its native receipt.
// It never recovers unrelated publications or changes original application evidence.
func (j *ActivationJournal) CompletePhase(ctx context.Context, phase ActivationPhase, check func(context.Context) error) error {
	if j == nil || j.activationJournalState == nil || ctx == nil || check == nil {
		return ErrConflict
	}
	checked, err := InspectActivationPhaseCompletion(ctx, j.path, j.digest, phase)
	if err != nil {
		return err
	}
	body, err := checked.phaseBody(phase)
	if err != nil {
		return err
	}
	if err := checked.directory.CompletePublication(ctx, string(phase)+".json", nil, body, func(ctx context.Context) error {
		actual, err := checked.directory.ReadFile(activationDecisionFile, ActivationDecisionMaxBytes)
		if err != nil || !bytes.Equal(actual, j.body) || historySHA256(actual) != j.digest {
			return errors.Join(ErrConflict, err)
		}
		return check(ctx)
	}); err != nil {
		return err
	}
	checked, err = InspectActivationJournal(ctx, j.path, j.digest)
	if err != nil {
		return err
	}
	j.phases = checked.phases
	j.completing = ""
	return nil
}

func activationPhaseIndex(phase ActivationPhase) int {
	for index, selected := range activationPhases {
		if phase == selected {
			return index
		}
	}
	return -1
}

func (j *ActivationJournal) phaseBody(phase ActivationPhase) ([]byte, error) {
	if j == nil || j.activationJournalState == nil {
		return nil, ErrConflict
	}
	index := activationPhaseIndex(phase)
	if index < 0 || index > len(j.phases) {
		return nil, ErrConflict
	}
	previous := j.digest
	if index > 0 {
		body, err := json.Marshal(j.phases[index-1], json.Deterministic(true))
		if err != nil {
			return nil, err
		}
		previous = historySHA256(body)
	}
	return json.Marshal(activationPhaseRecord{1, j.digest, phase, previous}, json.Deterministic(true))
}
