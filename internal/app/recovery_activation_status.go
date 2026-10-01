package app

import (
	"context"
	"errors"
	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/recovery"
)

// InspectRecoveryActivation reports sealed historical completion and current permission.
// It never prepares state, repairs records, publishes phases, releases imports, or renews permission.
func InspectRecoveryActivation(ctx context.Context, cfg *config.Config, request RecoveryActivationRequest) (result RecoveryActivationResult, resultErr error) {
	if ctx == nil || cfg == nil || request.Validate() != nil || request.ExpectedDecisionSHA256 == "" {
		return result, recovery.ErrConflict
	}
	if _, bounded := ctx.Deadline(); !bounded {
		return result, recovery.ErrConflict
	}
	encryption, err := backupEncryption(cfg)
	if err != nil {
		return result, err
	}
	source, err := recovery.InspectRestoreSource(ctx, request.Prepare.VerifyRequest, encryption, catalog.InspectCapturedCatalog)
	if err != nil {
		return result, err
	}
	n, err := openRecoveryActivationNative(ctx, cfg, request)
	if err != nil {
		return result, err
	}
	defer func() { resultErr = errors.Join(resultErr, n.close()) }()
	directory, err := productfiles.ExistingDirectory(request.ActivationDirectory)
	if err != nil {
		return result, err
	}
	body, err := directory.ReadFile("decision.json", recovery.ActivationDecisionMaxBytes)
	if err != nil {
		return result, err
	}
	digest := canonicalRecordSHA256(body)
	if digest != request.ExpectedDecisionSHA256 {
		return result, recovery.ErrConflict
	}
	sealed, err := openSealedActivation(ctx, cfg, request, n, source, body, digest)
	if err != nil {
		return result, err
	}
	complete, err := sealed.nativeCompletion(ctx, n)
	result = RecoveryActivationResult{DecisionSHA256: digest, Restricted: true, NextAction: "retry activation with the original decision"}
	if err != nil {
		return result, err
	}
	for _, done := range complete {
		if !done {
			break
		}
		result.CompletedPhases++
	}
	result.HistoricallyComplete = result.CompletedPhases == 3
	if result.HistoricallyComplete {
		result.CurrentAdmissionValid = sealed.currentAdmission(ctx, cfg, n) == nil
		result.Restricted = !result.CurrentAdmissionValid
		if result.CurrentAdmissionValid {
			result.NextAction = "start a fresh gateway and verify readiness"
		} else {
			result.NextAction = "inspect current catalog permission and deployment approval"
		}
	}
	return result, nil
}
