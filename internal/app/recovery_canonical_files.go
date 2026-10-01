package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
)

const canonicalRecoveryMaxBytes = 8 << 20

// VerifiedCanonicalFiles retains checked application role selections and original publications.
// Its private state grants no permission, preparation, publication, or component release.
type VerifiedCanonicalFiles struct{ state *verifiedCanonicalFilesState }

type verifiedCanonicalFilesState struct{ body []byte }

type canonicalFilesRecord struct {
	Topology     bool                         `json:"topology,omitempty"`
	TargetInputs jsontext.Value               `json:"target_inputs,omitempty"`
	Version      int                          `json:"version"`
	Request      recovery.PrepareRequest      `json:"request"`
	Inputs       jsontext.Value               `json:"inputs"`
	Files        []recovery.FileDisposition   `json:"files"`
	Roles        []canonicalRoleDisposition   `json:"roles"`
	Publications []canonicalPublicationRecord `json:"publications"`
}

type canonicalPublicationRecord struct {
	Role   string         `json:"role"`
	Record jsontext.Value `json:"record"`
}

// Format excludes native identities, paths, and secret-derived input digests.
func (VerifiedCanonicalFiles) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<private verified canonical files>"))
}

// VerifyRecoveryCanonicalFiles checks original successful owner results before the first release.
// The caller fences all writers and supplies only publications that returned without error.
// File dispositions come from the verified backup and current owner selections.
func VerifyRecoveryCanonicalFiles(ctx context.Context, cfg *config.Config, request recovery.PrepareRequest, originals []recovery.PublishFilesResult) (*VerifiedCanonicalFiles, error) {
	return verifyCanonicalFilesMode(ctx, cfg, request, originals, false)
}

func verifyCanonicalFilesMode(ctx context.Context, cfg *config.Config, request recovery.PrepareRequest, originals []recovery.PublishFilesResult, topology bool) (*VerifiedCanonicalFiles, error) {
	return verifyCanonicalFilesWithSource(ctx, cfg, request, originals, topology, nil)
}

func verifyCanonicalFilesWithSource(ctx context.Context, cfg *config.Config, request recovery.PrepareRequest, originals []recovery.PublishFilesResult, topology bool, source *recovery.RestoreSource) (*VerifiedCanonicalFiles, error) {
	source, selected, record, err := configuredCanonicalFilesWithSource(ctx, cfg, request, topology, source)
	if err != nil {
		return nil, err
	}
	if len(originals) != len(selected) {
		return nil, recovery.ErrConflict
	}
	supplied := make(map[string]recovery.PublishFilesResult, len(originals))
	for _, original := range originals {
		prepared := original.Preparation.Prepared
		if _, duplicate := supplied[original.Role]; duplicate || prepared.Version != 1 || prepared.OperationID != request.Operation.ID || prepared.ManifestSHA256 != request.ManifestSHA256 || prepared.FencingEvidence != request.Operation.FencingEvidence || original.Preparation.FilesDirectory != request.FilesDirectory {
			return nil, recovery.ErrConflict
		}
		supplied[original.Role] = original
	}
	for _, role := range selected {
		original, exists := supplied[role.role]
		if !exists {
			return nil, recovery.ErrConflict
		}
		checked, err := source.InspectPublishedFileTree(ctx, role.tree, original.Tree, canonicalFileOwnerValidator(cfg, role.role))
		if err != nil {
			return nil, err
		}
		body, err := checked.Record()
		if err != nil {
			return nil, err
		}
		record.Publications = append(record.Publications, canonicalPublicationRecord{role.role, body})
	}
	body, err := json.Marshal(record, json.Deterministic(true))
	if err != nil || len(body) > canonicalRecoveryMaxBytes {
		return nil, recovery.ErrConflict
	}
	verified := &VerifiedCanonicalFiles{state: &verifiedCanonicalFilesState{bytes.Clone(body)}}
	if _, err := inspectCanonicalFilesWithSource(ctx, cfg, request, body, verified.Digest(), source); err != nil {
		return nil, err
	}
	return verified, nil
}

// Record exports bounded original evidence for the immutable activation decision.
// The record contains no selected file contents and grants no activation authority.
func (v *VerifiedCanonicalFiles) Record() ([]byte, error) {
	if v == nil || v.state == nil || len(v.state.body) == 0 || len(v.state.body) > canonicalRecoveryMaxBytes {
		return nil, recovery.ErrConflict
	}
	return bytes.Clone(v.state.body), nil
}

// Digest identifies checked evidence. A supplied digest cannot replace owner checks.
func (v *VerifiedCanonicalFiles) Digest() string {
	if v == nil || v.state == nil {
		return ""
	}
	digest := sha256.Sum256(v.state.body)
	return hex.EncodeToString(digest[:])
}

// Check passively rechecks original publications and the current target selections.
func (v *VerifiedCanonicalFiles) Check(ctx context.Context, cfg *config.Config, request recovery.PrepareRequest) error {
	body, err := v.Record()
	if err != nil {
		return err
	}
	_, err = InspectRecoveryCanonicalFiles(ctx, cfg, request, body, v.Digest())
	return err
}

// InspectRecoveryCanonicalFiles reopens sealed evidence after any component release.
// The digest comes from the immutable activation decision, not a diagnostic report.
// It never prepares, publishes, repairs, reads external credentials, or issues permission.
func InspectRecoveryCanonicalFiles(ctx context.Context, cfg *config.Config, request recovery.PrepareRequest, body []byte, sealedSHA256 string) (*VerifiedCanonicalFiles, error) {
	return inspectCanonicalFilesWithSource(ctx, cfg, request, body, sealedSHA256, nil)
}

func inspectCanonicalFilesWithSource(ctx context.Context, cfg *config.Config, request recovery.PrepareRequest, body []byte, sealedSHA256 string, source *recovery.RestoreSource) (*VerifiedCanonicalFiles, error) {
	if ctx == nil || cfg == nil || len(body) == 0 || len(body) > canonicalRecoveryMaxBytes {
		return nil, recovery.ErrConflict
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	digest := sha256.Sum256(body)
	if hex.EncodeToString(digest[:]) != sealedSHA256 {
		return nil, recovery.ErrConflict
	}
	var retained canonicalFilesRecord
	if err := json.Unmarshal(body, &retained, json.RejectUnknownMembers(true)); err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(retained, json.Deterministic(true))
	if err != nil || !bytes.Equal(canonical, body) || retained.Version != 1 || retained.Request != request {
		return nil, recovery.ErrConflict
	}
	source, selected, current, err := configuredCanonicalFilesWithSource(ctx, cfg, request, retained.Topology, source)
	if err != nil {
		return nil, err
	}
	if len(retained.Publications) != len(selected) {
		return nil, recovery.ErrConflict
	}
	for index, role := range selected {
		original := retained.Publications[index]
		if original.Role != role.role {
			return nil, recovery.ErrConflict
		}
		ownerDigest := sha256.Sum256(original.Record)
		checked, err := source.InspectRetainedFileTree(ctx, role.tree, original.Record, hex.EncodeToString(ownerDigest[:]), canonicalFileOwnerValidator(cfg, role.role))
		if err != nil {
			return nil, err
		}
		checkedBody, err := checked.Record()
		if err != nil {
			return nil, err
		}
		current.Publications = append(current.Publications, canonicalPublicationRecord{role.role, checkedBody})
	}
	inputs, err := cfg.InspectRecoverySelection(ctx)
	if err != nil || !bytes.Equal(inputs.PrivateEvidence(), current.Inputs) {
		return nil, recovery.ErrConflict
	}
	checked, err := json.Marshal(current, json.Deterministic(true))
	if err != nil || !bytes.Equal(checked, body) {
		return nil, recovery.ErrConflict
	}
	return &VerifiedCanonicalFiles{state: &verifiedCanonicalFilesState{bytes.Clone(body)}}, nil
}

func configuredCanonicalFilesWithSource(ctx context.Context, cfg *config.Config, request recovery.PrepareRequest, topology bool, source *recovery.RestoreSource) (*recovery.RestoreSource, []canonicalRoleSelection, canonicalFilesRecord, error) {
	var record canonicalFilesRecord
	if ctx == nil || cfg == nil {
		return nil, nil, record, recovery.ErrConflict
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, record, err
	}
	source, plan, err := canonicalFilesSource(ctx, cfg, request, source)
	if err != nil {
		return nil, nil, record, err
	}
	selected, files, roles, err := canonicalRecoverySelectionMode(ctx, cfg, source, plan, topology)
	if err != nil {
		return nil, nil, record, err
	}
	inputs, err := cfg.InspectRecoverySelection(ctx)
	if err != nil {
		return nil, nil, record, err
	}
	if topology {
		targetInputs, err := inspectTopologyTargetInputs(ctx, cfg)
		if err != nil {
			return nil, nil, record, err
		}
		record.TargetInputs = targetInputs
		record.Topology = true
	}
	record = canonicalFilesRecord{Topology: record.Topology, TargetInputs: record.TargetInputs, Version: 1, Request: request, Inputs: inputs.PrivateEvidence(), Files: files, Roles: roles, Publications: []canonicalPublicationRecord{}}
	return source, selected, record, nil
}

func canonicalFilesSource(ctx context.Context, cfg *config.Config, request recovery.PrepareRequest, source *recovery.RestoreSource) (*recovery.RestoreSource, []recovery.FileDisposition, error) {
	if source == nil {
		checked, plan, _, err := inspectBackupRestore(ctx, cfg, request)
		return checked, plan, err
	}
	if err := request.Validate(); err != nil {
		return nil, nil, err
	}
	if _, err := restoreTargetPaths(cfg, request); err != nil {
		return nil, nil, err
	}
	encryption, err := backupEncryption(cfg)
	if err != nil {
		return nil, nil, err
	}
	if err := source.CheckOriginalArtifacts(ctx, request.VerifyRequest, encryption); err != nil {
		return nil, nil, err
	}
	if source.DeploymentID() != cfg.EffectivePaths().DeploymentID {
		return nil, nil, recovery.ErrConflict
	}
	plan, err := planBackupFiles(ctx, cfg, source)
	return source, plan, err
}
