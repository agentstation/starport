package recovery

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"path/filepath"

	"github.com/agentstation/starmap/pkg/productfiles"
	"github.com/agentstation/starport/internal/credentials"
)

// RetainedActivationHistoryRequest selects original history and an independently retained closed-final decision digest.
// Directory holds acceptance and replay journals. ScratchDirectory holds temporary verification copies.
// Attestation binds original operator evidence. Current fencing requires a separate owner check.
// A zero PriorApproval selects an import journal. Otherwise it selects the closed-adoption journal of that approval.
type RetainedActivationHistoryRequest struct {
	History          HistoryPackageRequest
	Directory        string
	DecisionSHA256   string
	ScratchDirectory string
	Attestation      HistoryAttestation
	PriorApproval    Record
}

type retainedActivationHistoryState struct {
	request RetainedActivationHistoryRequest
	source  VerifyRequest
	body    []byte
	record  closedFinalRecord
}

// RetainedActivationHistory verifies historical completion after native import barriers disappear.
// Its private state grants no replay, activation, permission, or current-admission capability.
type RetainedActivationHistory struct {
	state *retainedActivationHistoryState
}

// Format excludes private evidence and operator references from diagnostics.
func (RetainedActivationHistory) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<private retained activation history>"))
}

// Report returns historical diagnostics with every current activation requirement still explicit.
// Native phase receipts, current permission, settings, files, and fencing require separate checks.
func (h *RetainedActivationHistory) Report() ClosedFinalReport {
	result := ClosedFinalReport{Restricted: true, RequiresSettings: true, RequiresCanonicalFiles: true,
		RequiresCatalogSelection: true, RequiresTransportTrust: true, RequiresAdministratorCredentials: true}
	if h != nil && h.state != nil {
		result.DecisionSHA256 = historySHA256(h.state.body)
		result.Replay = h.state.record.Replay
		result.Graph = h.state.record.Graph
	}
	return result
}

// OpenRetainedActivationHistory checks original backup, acceptance, replay images, and the fixed final graph.
// It reads retained bytes and temporary copies without accessing native targets or continuing recovery journals.
// Success preserves original validation times and grants no current admission permission.
func OpenRetainedActivationHistory(ctx context.Context, source *RestoreSource, request RetainedActivationHistoryRequest, encryption *credentials.EncryptionService, inspectors ...CapturedKVInspector) (*RetainedActivationHistory, error) {
	if err := validateRetainedActivationRequest(ctx, source, request, encryption); err != nil {
		return nil, err
	}
	backupDirectory, err := productfiles.ExistingDirectory(source.request.Directory)
	if err != nil {
		return nil, err
	}
	if _, err := backupDirectory.ReadFile(bundleManifestFile, bundleMaxManifestBytes); err != nil {
		return nil, err
	}
	verifiedRequest := source.request
	verifiedRequest.ScratchDirectory = request.ScratchDirectory
	verifiedSource, err := InspectRestoreSource(ctx, verifiedRequest, encryption, inspectors...)
	if err != nil {
		return nil, err
	}
	original, err := json.Marshal(source.manifest, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	actual, err := json.Marshal(verifiedSource.manifest, json.Deterministic(true))
	if err != nil || !bytes.Equal(original, actual) {
		return nil, ErrConflict
	}
	runner, journal, err := openRetainedActivationRunner(ctx, verifiedSource, request, encryption)
	if err != nil {
		return nil, err
	}
	if err := checkRetainedActivationImages(ctx, runner); err != nil {
		return nil, err
	}
	record, body, err := readRetainedActivationFinal(ctx, runner, journal, request.DecisionSHA256, inspectors)
	if err != nil {
		return nil, err
	}
	return &RetainedActivationHistory{state: &retainedActivationHistoryState{
		request: request, source: source.request, body: bytes.Clone(body), record: record}}, nil
}

// Check rereads the exact retained evidence without resuming replay or changing original validation times.
// Missing records, changed request scope, and corrupt images refuse this historical verification.
func (h *RetainedActivationHistory) Check(ctx context.Context, source *RestoreSource, request RetainedActivationHistoryRequest, encryption *credentials.EncryptionService, inspectors ...CapturedKVInspector) error {
	if h == nil || h.state == nil || source == nil || h.state.request != request || h.state.source != source.request {
		return ErrConflict
	}
	canonical, err := json.Marshal(h.state.record, json.Deterministic(true))
	if err != nil || !bytes.Equal(canonical, h.state.body) {
		return ErrConflict
	}
	checked, err := OpenRetainedActivationHistory(ctx, source, request, encryption, inspectors...)
	if err != nil {
		return err
	}
	if !bytes.Equal(checked.state.body, h.state.body) {
		return ErrConflict
	}
	return ctx.Err()
}

func validateRetainedActivationRequest(ctx context.Context, source *RestoreSource, request RetainedActivationHistoryRequest, encryption *credentials.EncryptionService) error {
	if ctx == nil || source == nil || encryption == nil || source.manifest.Format != bundleFormat || !historyDigest(request.DecisionSHA256) {
		return ErrConflict
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, path := range []string{source.request.Directory, request.History.Directory, request.Directory, request.ScratchDirectory} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return ErrConflict
		}
		directory, err := productfiles.ExistingDirectory(path)
		if err != nil {
			return err
		}
		if err := directory.CheckNoPendingPublications(ctx); err != nil {
			return err
		}
	}
	return checkRestorePathPairs([][2]string{{source.request.Directory, request.ScratchDirectory},
		{request.History.Directory, request.ScratchDirectory}, {request.Directory, request.ScratchDirectory}})
}
