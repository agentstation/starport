package recovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/agentstation/starmap/pkg/productfiles"
)

const historyReplayComplete = "replay_complete"

const historyManifestMaxBytes = 1 << 20
const historyPayloadMaxBytes = 8 << 20
const historyAggregateMaxBytes = 64 << 20
const historyMaxEntries = 4096

// HistoryPackageRequest binds private operator evidence to an independently retained digest.
// The coordinator derives TargetSHA256 from its explicitly selected target identities.
// This binding does not prove physical server continuity or external writer fencing.
type HistoryPackageRequest struct {
	Directory      string
	ManifestSHA256 string
	TargetSHA256   string
	Operation      RestoreOperation
}

type historyManifest struct {
	Version        int               `json:"version"`
	BackupSHA256   string            `json:"backup_sha256"`
	DeploymentID   string            `json:"deployment_id"`
	Operation      RestoreOperation  `json:"operation"`
	TargetSHA256   string            `json:"target_sha256"`
	PreparedSHA256 string            `json:"prepared_sha256"`
	Mode           string            `json:"mode"`
	Disposition    string            `json:"disposition"`
	Interval       historyInterval   `json:"interval"`
	HighestEpoch   EpochEvidence     `json:"highest_epoch"`
	Evidence       []historyEvidence `json:"evidence_sources"`
	Steps          []historyStep     `json:"steps"`
	Assets         []historyAsset    `json:"assets,omitempty"`
}

type historyInterval struct {
	Through      time.Time `json:"through_utc"`
	EndReference string    `json:"end_reference"`
}

type historyEvidence struct {
	ID        string `json:"id"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	Reference string `json:"reference"`
}

type historyStep struct {
	Ordinal  int      `json:"ordinal"`
	Kind     string   `json:"kind"`
	Path     string   `json:"path"`
	Size     int      `json:"size"`
	SHA256   string   `json:"sha256"`
	Evidence []string `json:"evidence_source_ids"`
}

type verifiedHistoryState struct {
	manifest historyManifest
	digest   string
	payloads [][]byte
	assets   *productfiles.Directory
}

// VerifiedHistory retains an immutable private copy of a bound history package.
// It proves byte integrity and structural bounds, not domain validity or complete interval coverage.
// It grants no mutation, replay, or activation capability.
type VerifiedHistory struct{ state *verifiedHistoryState }

// Format excludes operator references and private payloads from diagnostics.
func (VerifiedHistory) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<private recovery history>"))
}

// Digest identifies the exact verified manifest bytes.
func (v *VerifiedHistory) Digest() string {
	if v == nil || v.state == nil {
		return ""
	}
	return v.state.digest
}

// StepCount reports declared steps without exposing their private payloads.
func (v *VerifiedHistory) StepCount() int {
	if v == nil || v.state == nil {
		return 0
	}
	return len(v.state.manifest.Steps)
}

// VerifyHistoryPackage copies bounded private evidence without accessing any target store.
// Owner decoders, explicit operator acceptance, and native replay guards remain required.
func (s *RestoreSource) VerifyHistoryPackage(ctx context.Context, request HistoryPackageRequest) (*VerifiedHistory, error) {
	if ctx == nil || s == nil || s.manifest.Format != bundleFormat {
		return nil, ErrConflict
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(request.Directory) || filepath.Clean(request.Directory) != request.Directory || !historyDigest(request.ManifestSHA256) || !historyDigest(request.TargetSHA256) || request.Operation.Validate() != nil || !historyReference(request.Operation.FencingEvidence, 2048) {
		return nil, ErrConflict
	}
	identity, err := s.ImportIdentity(request.Operation)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(identity, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	directory, err := productfiles.ExistingDirectory(request.Directory)
	if err != nil {
		return nil, err
	}
	body, err := directory.ReadFile("history.json", historyManifestMaxBytes)
	if err != nil {
		return nil, err
	}
	if historySHA256(body) != request.ManifestSHA256 {
		return nil, errors.New("recovery history differs from its independently retained digest")
	}
	var manifest historyManifest
	if err := json.Unmarshal(body, &manifest, json.RejectUnknownMembers(true)); err != nil {
		return nil, errors.New("recovery history manifest has invalid or unsupported fields")
	}
	if err := manifest.validate(s, request, historySHA256(encoded)); err != nil {
		return nil, err
	}
	state := &verifiedHistoryState{manifest: manifest, digest: request.ManifestSHA256}
	if err := verifyHistoryAssets(ctx, directory, state); err != nil {
		return nil, err
	}
	if len(manifest.Steps) != 0 {
		payloads, err := directory.ExistingChild("payloads")
		if err != nil {
			return nil, err
		}
		for _, step := range manifest.Steps {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			data, err := payloads.ReadFile(filepath.Base(step.Path), int64(step.Size))
			if err != nil {
				return nil, err
			}
			var value jsontext.Value
			if len(data) != step.Size || historySHA256(data) != step.SHA256 || json.Unmarshal(data, &value) != nil {
				return nil, fmt.Errorf("recovery history payload %d differs from its declared bytes or JSON format", step.Ordinal)
			}
			state.payloads = append(state.payloads, data)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &VerifiedHistory{state: state}, nil
}

func (m historyManifest) validate(source *RestoreSource, request HistoryPackageRequest, preparedSHA256 string) error {
	if m.Version != 1 || m.BackupSHA256 != source.request.ManifestSHA256 || m.DeploymentID != source.manifest.Request.Boundary.DeploymentID || m.Operation != request.Operation || m.TargetSHA256 != request.TargetSHA256 || m.PreparedSHA256 != preparedSHA256 {
		return ErrConflict
	}
	if m.Mode != "planned_migration" && m.Mode != "disaster_recovery" || m.Disposition != historyReplayComplete && m.Disposition != "remain_restricted" {
		return ErrConflict
	}
	if m.Interval.Through.IsZero() || m.Interval.Through.Before(source.manifest.FinishedAt) || !historyReference(m.Interval.EndReference, 2048) || len(m.Evidence) == 0 || len(m.Evidence) > historyMaxEntries || len(m.Steps) > historyMaxEntries {
		return ErrConflict
	}
	if m.HighestEpoch.HighestEpoch < source.manifest.Request.Boundary.Epoch || m.HighestEpoch.HighestEpoch >= math.MaxInt64-1 || !historyDigest(m.HighestEpoch.SourceSHA256) || !historyReference(m.HighestEpoch.Reference, 4096) || !historyReference(m.HighestEpoch.Operator, 4096) {
		return ErrConflict
	}
	evidence, err := m.evidenceIndex()
	if err != nil {
		return err
	}
	if err := m.validateAssets(evidence); err != nil {
		return err
	}
	return m.validateSteps(evidence)
}

func (m historyManifest) evidenceIndex() (map[string]bool, error) {
	evidence := make(map[string]bool, len(m.Evidence))
	epochSource := false
	for _, item := range m.Evidence {
		if !historyReference(item.ID, 128) || evidence[item.ID] || !historyDigest(item.SHA256) || item.Size < 0 || !historyReference(item.Reference, 2048) {
			return nil, ErrConflict
		}
		evidence[item.ID] = true
		epochSource = epochSource || item.SHA256 == m.HighestEpoch.SourceSHA256
	}
	if !epochSource {
		return nil, errors.New("recovery epoch evidence is absent from the declared sources")
	}
	return evidence, nil
}

func (m historyManifest) validateSteps(evidence map[string]bool) error {
	total := 0
	for i, step := range m.Steps {
		if step.Ordinal != i+1 || step.Path != fmt.Sprintf("payloads/%06d.json", i+1) || !historyStepKind(step.Kind) || step.Size <= 0 || step.Size > historyPayloadMaxBytes || !historyDigest(step.SHA256) || len(step.Evidence) == 0 || len(step.Evidence) > historyMaxEntries {
			return ErrConflict
		}
		if step.Size > historyAggregateMaxBytes-total {
			return errors.New("recovery history payloads exceed their aggregate byte limit")
		}
		total += step.Size
		seen := make(map[string]bool, len(step.Evidence))
		for _, id := range step.Evidence {
			if !evidence[id] || seen[id] {
				return ErrConflict
			}
			seen[id] = true
		}
	}
	return nil
}

func historyStepKind(kind string) bool {
	switch kind {
	case "kv_domain", "window_stage", "window_finalize", "slot_stage", "slot_finalize", "storedbytes_stage", "storedbytes_finalize", "blob_publication", "sql_identity", "kv_authorization_final", "sql_authorization_final":
		return true
	default:
		return false
	}
}

func historySHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func historyDigest(value string) bool {
	data, err := hex.DecodeString(value)
	return err == nil && len(data) == sha256.Size && value == hex.EncodeToString(data)
}

func historyReference(value string, limit int) bool {
	return len(value) <= limit && strings.TrimSpace(value) != "" && !strings.ContainsFunc(value, unicode.IsControl)
}
