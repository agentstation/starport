package recovery

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/agentstation/starmap/pkg/productfiles"
)

const fileTreeInspectionMaxBytes = 32 << 10

type fileTreeInspectionRecord struct {
	Version      int            `json:"version"`
	BackupSHA256 string         `json:"backup_sha256"`
	Tree         FileTreeResult `json:"tree"`
}

type verifiedFileTreeState struct {
	body   []byte
	record fileTreeInspectionRecord
}

// VerifiedFileTree retains passive native checks of an original publication.
// It grants no role selection, durability, permission, preparation, or activation authority.
type VerifiedFileTree struct{ state *verifiedFileTreeState }

// Format excludes canonical paths and private native identities from diagnostics.
func (VerifiedFileTree) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<private verified canonical file tree>"))
}

// InspectPublishedFileTree checks an original successful publication without changing files.
// The caller must retain the actual owner result and fence every target writer.
// Missing original identities require refusal. Inspection cannot recreate those identities.
func (s *RestoreSource) InspectPublishedFileTree(ctx context.Context, request FileTreeRequest, original FileTreeResult, validate FileTreeValidator) (*VerifiedFileTree, error) {
	if ctx == nil || validate == nil {
		return nil, ErrConflict
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	files, digest, err := s.prepareFileTree(request)
	if err != nil {
		return nil, err
	}
	if original.Destination != request.Destination || original.SelectionSHA256 != digest || original.Files != len(files) || original.Published == original.Reused ||
		original.DirectoryIdentity == "" || original.ParentIdentity == "" || len(original.DirectoryIdentity) > 512 || len(original.ParentIdentity) > 512 {
		return nil, ErrConflict
	}
	if err := inspectOriginalPublishedTree(ctx, request.Destination, original, files, validate); err != nil {
		return nil, err
	}
	record := fileTreeInspectionRecord{Version: 1, BackupSHA256: s.request.ManifestSHA256, Tree: original}
	body, err := json.Marshal(record, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	if len(body) > fileTreeInspectionMaxBytes {
		return nil, ErrConflict
	}
	return &VerifiedFileTree{state: &verifiedFileTreeState{body: body, record: record}}, nil
}

// Record exports bounded evidence for the coordinator's immutable activation record.
// It contains no selected file contents and grants no mutation authority.
func (p *VerifiedFileTree) Record() ([]byte, error) {
	if p == nil || p.state == nil {
		return nil, ErrConflict
	}
	encoded, err := json.Marshal(p.state.record, json.Deterministic(true))
	if err != nil || len(encoded) > fileTreeInspectionMaxBytes || !bytes.Equal(encoded, p.state.body) {
		return nil, errors.Join(ErrConflict, err)
	}
	return bytes.Clone(p.state.body), nil
}

// Digest identifies the retained record. The coordinator must seal it before component release.
// A supplied digest does not replace the native owner checks.
func (p *VerifiedFileTree) Digest() string {
	if p == nil || p.state == nil {
		return ""
	}
	return historySHA256(p.state.body)
}

// InspectRetainedFileTree checks the original sealed record after a component release.
// expectedRecordSHA256 must come from the coordinator's immutable activation decision.
// The owner checks exact bytes and identities without publication, sync, repair, or time renewal.
func (s *RestoreSource) InspectRetainedFileTree(ctx context.Context, request FileTreeRequest, record []byte, expectedRecordSHA256 string, validate FileTreeValidator) (*VerifiedFileTree, error) {
	if s == nil || len(record) == 0 || len(record) > fileTreeInspectionMaxBytes || !historyDigest(expectedRecordSHA256) || historySHA256(record) != expectedRecordSHA256 {
		return nil, ErrConflict
	}
	var retained fileTreeInspectionRecord
	if err := json.Unmarshal(record, &retained, json.RejectUnknownMembers(true)); err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(retained, json.Deterministic(true))
	if err != nil || !bytes.Equal(canonical, record) || retained.Version != 1 || retained.BackupSHA256 != s.request.ManifestSHA256 {
		return nil, errors.Join(ErrConflict, err)
	}
	checked, err := s.InspectPublishedFileTree(ctx, request, retained.Tree, validate)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(checked.state.body, record) {
		return nil, ErrConflict
	}
	return checked, nil
}

// Check rereads the original record and target. It retains the same historical evidence.
// App-owned role selection and semantic validators must remain unchanged.
func (p *VerifiedFileTree) Check(ctx context.Context, source *RestoreSource, request FileTreeRequest, validate FileTreeValidator) error {
	record, err := p.Record()
	if err != nil {
		return err
	}
	_, err = source.InspectRetainedFileTree(ctx, request, record, p.Digest(), validate)
	return err
}

func inspectOriginalPublishedTree(ctx context.Context, destination string, original FileTreeResult, files []restoreTreeFile, validate FileTreeValidator) (resultErr error) {
	parent, err := productfiles.ExistingDirectory(filepath.Dir(destination))
	if err != nil {
		return err
	}
	if err := checkFileTreeDirectoryIdentity(parent, original.ParentIdentity); err != nil {
		return err
	}
	if err := parent.CheckNoPendingPublications(ctx); err != nil {
		return err
	}
	directory, err := productfiles.ExistingDirectory(destination)
	if err != nil {
		return err
	}
	if err := checkFileTreeDirectoryIdentity(directory, original.DirectoryIdentity); err != nil {
		return err
	}
	if err := directory.CheckNoPendingPublications(ctx); err != nil {
		return err
	}
	root, err := directory.Open()
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	if err := checkRestoredTree(ctx, root, files); err != nil {
		return err
	}
	if err := validate(ctx, destination); err != nil {
		return err
	}
	if err := checkRestoredTree(ctx, root, files); err != nil {
		return err
	}
	if err := directory.CheckNoPendingPublications(ctx); err != nil {
		return err
	}
	if err := parent.CheckNoPendingPublications(ctx); err != nil {
		return err
	}
	if err := checkFileTreeDirectoryIdentity(directory, original.DirectoryIdentity); err != nil {
		return err
	}
	if err := checkFileTreeDirectoryIdentity(parent, original.ParentIdentity); err != nil {
		return err
	}
	return ctx.Err()
}
