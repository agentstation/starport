package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
)

// recoveryActivationTransition selects how the shared activation phases obtain native claims and accept history.
// A nil adoption selects the empty-target import. The shared phases treat each native claim as an opaque value.
type recoveryActivationTransition struct{ adoption *populatedAdoption }

// populatedAdoption holds the operator's prior approval and the retained control observation of one fenced deployment.
type populatedAdoption struct {
	prior    recovery.Record
	controls recovery.PopulatedControls
}

var importActivation = recoveryActivationTransition{}

func activationHistoryRequest(request RecoveryActivationRequest) recovery.HistoryPackageRequest {
	return recovery.HistoryPackageRequest{Directory: request.History.HistoryDirectory, ManifestSHA256: request.History.HistorySHA256, TargetSHA256: request.History.ExpectedTargetSHA256, Operation: request.History.Operation}
}

// identity derives the native claims that the shared phases release.
func (t recoveryActivationTransition) identity(ctx context.Context, source *recovery.RestoreSource, request RecoveryActivationRequest) (recovery.PreparedImportIdentity, error) {
	if t.adoption == nil {
		return source.ImportIdentity(request.Prepare.Operation)
	}
	history, err := source.VerifyHistoryPackage(ctx, activationHistoryRequest(request))
	if err != nil {
		return recovery.PreparedImportIdentity{}, err
	}
	return source.AdoptionIdentity(history, t.adoption.prior)
}

// accept moves the witness from the claimed record to the next closed epoch.
func (t recoveryActivationTransition) accept(ctx context.Context, n *recoveryActivationNative, source *recovery.RestoreSource, history *recovery.VerifiedHistory, request RecoveryActivationRequest) (*recovery.AcceptedHistory, error) {
	acceptance := recovery.HistoryAcceptanceRequest{Directory: request.History.JournalDirectory, Attestation: request.History.Attestation}
	if t.adoption == nil {
		return n.witness.AcceptImportedHistory(ctx, source, history, acceptance)
	}
	return n.witness.AcceptClosedAdoptionHistory(ctx, source, history, t.adoption.prior, acceptance)
}

// priorApproval selects the retained activation journal: zero for an import, the prior approval for an adoption.
func (t recoveryActivationTransition) priorApproval() recovery.Record {
	if t.adoption == nil {
		return recovery.Record{}
	}
	return t.adoption.prior
}

// canonicalFiles establishes the selected canonical role trees before history acceptance.
// An import restores the source into empty targets and publishes each tree. An adoption verifies each live tree in place.
func (t recoveryActivationTransition) canonicalFiles(ctx context.Context, cfg *config.Config, n *recoveryActivationNative, source *recovery.RestoreSource, request RecoveryActivationRequest) (*VerifiedCanonicalFiles, error) {
	if t.adoption != nil {
		return verifyCanonicalFilesInPlace(ctx, cfg, request.Prepare, source)
	}
	plan, err := planBackupFiles(ctx, cfg, source)
	if err != nil {
		return nil, err
	}
	selected, _, _, err := canonicalRecoverySelectionMode(ctx, cfg, source, plan, true)
	if err != nil {
		return nil, err
	}
	prepared, err := source.Prepare(ctx, recovery.BundleTargets{KV: n.kvTransfer, SQL: n.db, Blobs: n.blobs, FilesDirectory: request.Prepare.FilesDirectory}, request.Prepare.Operation)
	if err != nil {
		return nil, err
	}
	var originals []recovery.PublishFilesResult
	// Publication operates before any release. The role owner derives exact selected source files.
	for _, role := range selected {
		if _, err := productfiles.NewDirectory(filepath.Dir(role.tree.Destination)); err != nil {
			return nil, err
		}
		tree, err := source.PublishFileTree(ctx, role.tree, canonicalFileOwnerValidator(cfg, role.role))
		published := recovery.PublishFilesResult{Preparation: recovery.PrepareResult{Prepared: prepared, FilesDirectory: request.Prepare.FilesDirectory}, Role: role.role, Tree: tree}
		if err != nil {
			return nil, err
		}
		originals = append(originals, published)
	}
	return verifyCanonicalFilesWithSource(ctx, cfg, request.Prepare, originals, true, source)
}

// claim places the populated claims from the retained observation. An import has no claim step here.
// The live canonical trees must equal C before the first native change.
// A witness past the adoption-prepared record shows an accepted claim. Acceptance then checks the exact retry.
func (t recoveryActivationTransition) claim(ctx context.Context, cfg *config.Config, n *recoveryActivationNative, source *recovery.RestoreSource, request RecoveryActivationRequest) error {
	if t.adoption == nil {
		return nil
	}
	if _, err := verifyCanonicalFilesInPlace(ctx, cfg, request.Prepare, source); err != nil {
		return err
	}
	history, err := source.VerifyHistoryPackage(ctx, activationHistoryRequest(request))
	if err != nil {
		return err
	}
	identity, err := source.AdoptionIdentity(history, t.adoption.prior)
	if err != nil {
		return err
	}
	current, err := n.witness.Current(ctx, identity.Boundary.DeploymentID)
	if err != nil {
		return err
	}
	if current != source.CapturedBoundary() && current != identity.Boundary {
		return nil
	}
	// A retry after the first claim may find the owner record that its own topology preparation wrote.
	if current == source.CapturedBoundary() {
		if err := checkPopulatedCatalogDirectory(ctx, cfg); err != nil {
			return err
		}
	}
	targets, err := n.populatedTargets()
	if err != nil {
		return err
	}
	_, err = source.ClaimPopulated(ctx, history, targets, recovery.PopulatedClaimRequest{PriorApproval: t.adoption.prior, Controls: t.adoption.controls, ScratchDirectory: request.History.ScratchDirectory})
	return err
}

// verifyCanonicalFilesInPlace checks each selected live canonical role tree against C.
// An absent destination refuses, so this check publishes no file.
func verifyCanonicalFilesInPlace(ctx context.Context, cfg *config.Config, request recovery.PrepareRequest, source *recovery.RestoreSource) (*VerifiedCanonicalFiles, error) {
	source, selected, record, err := configuredCanonicalFilesWithSource(ctx, cfg, request, true, source)
	if err != nil {
		return nil, err
	}
	for _, role := range selected {
		if _, err := os.Lstat(role.tree.Destination); err != nil {
			return nil, errors.Join(recovery.ErrConflict, err)
		}
		validate := canonicalFileOwnerValidator(cfg, role.role)
		tree, err := source.PublishFileTree(ctx, role.tree, validate)
		if err != nil {
			return nil, err
		}
		if !tree.Reused {
			return nil, recovery.ErrConflict
		}
		if err := appendCanonicalPublication(ctx, source, &record, role, tree, validate); err != nil {
			return nil, err
		}
	}
	return sealCanonicalFiles(ctx, cfg, request, record, source)
}
