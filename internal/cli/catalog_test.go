package cli

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/stretchr/testify/require"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
	"github.com/agentstation/starport/internal/config"
)

func TestCatalogPromoteBaselineCommandReportsReceipt(t *testing.T) {
	applied := runtimecatalog.PromotionReceipt{
		OperationID: "promote-2026-10", DeploymentID: "deployment", Status: runtimecatalog.PromotionApplied,
		Previous:      runtimecatalog.PromotionIdentity{GenerationID: "retained", Checksum: "sha256:old", Revision: 7},
		Promoted:      runtimecatalog.PromotionIdentity{GenerationID: "packaged", Checksum: "sha256:new", Revision: 8},
		InertRemovals: []catalogs.CatalogRemovalTarget{}, Actor: "operator",
		CreatedAt: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC),
	}
	refused := applied
	refused.Status = runtimecatalog.PromotionRefused
	refused.Promoted = refused.Previous
	refused.Refusal = "the fleet head is at revision 9, not the expected revision 7"
	pending := errors.New("baseline promotion is still pending. Request promote-2026-10 stays recorded until 2026-10-02T12:10:00Z")
	for _, mode := range []string{"json", "text", "refused", "fleet-only", "pending", "wait", "no-leader", "invalid-wait", "missing-operation", "invalid-operation", "extra-argument"} {
		t.Run(mode, func(t *testing.T) {
			deps, output, errOutput := testDependencies()
			calls := 0
			deps.PromoteCatalogBaseline = func(_ context.Context, cfg *config.Config, request runtimecatalog.PromotionRequest, wait time.Duration, recorded PromotionRecorded) (runtimecatalog.PromotionReceipt, error) {
				calls++
				require.NotNil(t, cfg)
				require.Equal(t, runtimecatalog.PromotionRequest{OperationID: "promote-2026-10", ExpectedRevision: 7}, request)
				expectedWait := 3 * time.Minute
				if mode == "wait" {
					expectedWait = 30 * time.Second
				}
				require.Equal(t, expectedWait, wait)
				leader := "gateway-a"
				if mode == "no-leader" {
					leader = ""
				}
				recorded(runtimecatalog.PromotionRecord{OperationID: request.OperationID, Status: runtimecatalog.PromotionPending,
					Expires: time.Date(2026, 10, 2, 12, 10, 0, 0, time.UTC)}, leader)
				switch mode {
				case "refused":
					return refused, nil
				case "fleet-only":
					return runtimecatalog.PromotionReceipt{}, runtimecatalog.ErrBaselinePromotionFleetOnly
				case "pending", "no-leader":
					return runtimecatalog.PromotionReceipt{}, pending
				}
				return applied, nil
			}
			args := []string{"starport", "catalog", "promote-baseline", "--operation-id", "promote-2026-10", "--expected-revision", "7", "--json"}
			switch mode {
			case "text":
				args = args[:len(args)-1]
			case "wait":
				args = append(args, "--wait", "30s")
			case "invalid-wait":
				args = append(args, "--wait", "0s")
			case "missing-operation":
				args = []string{"starport", "catalog", "promote-baseline", "--expected-revision", "7"}
			case "invalid-operation":
				args[4] = "promote 2026"
			case "extra-argument":
				args = append(args, "extra")
			}
			err := Run(t.Context(), args, deps)
			switch mode {
			case "json":
				require.NoError(t, err)
				require.Equal(t, 1, calls)
				var fields map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(output.Bytes(), &fields))
				require.ElementsMatch(t, []string{"operation_id", "deployment_id", "status", "previous", "promoted", "inert_removals", "refusal", "actor", "created_at"}, jsonFieldNames(fields))
				var receipt runtimecatalog.PromotionReceipt
				require.NoError(t, json.Unmarshal(output.Bytes(), &receipt))
				require.Equal(t, applied, receipt)
				require.JSONEq(t, `[]`, string(fields["inert_removals"]))
				require.Contains(t, errOutput.String(), "Recorded promotion request promote-2026-10. Gateway gateway-a leads the fleet and executes the request at its next lease renewal. Waiting up to 3m0s.",
					"progress goes to the error stream, so the JSON receipt stays pure")
			case "wait":
				require.NoError(t, err)
				require.Contains(t, errOutput.String(), "Waiting up to 30s.")
			case "text":
				require.NoError(t, err)
				require.Contains(t, output.String(), "Baseline promotion promote-2026-10 applied for deployment deployment.")
				require.Contains(t, output.String(), "Previous: generation retained, checksum sha256:old, revision 7")
				require.Contains(t, output.String(), "Promoted: generation packaged, checksum sha256:new, revision 8")
			case "refused":
				require.ErrorIs(t, err, ErrBaselinePromotionRefused)
				require.Equal(t, ExitCodeRuntime, ExitCode(err))
				var receipt runtimecatalog.PromotionReceipt
				require.NoError(t, json.Unmarshal(output.Bytes(), &receipt), "a refusal still prints its receipt")
				require.Equal(t, refused, receipt)
			case "fleet-only":
				require.ErrorIs(t, err, runtimecatalog.ErrBaselinePromotionFleetOnly)
				require.Equal(t, ExitCodeRuntime, ExitCode(err))
				require.Contains(t, err.Error(), "fleet-only")
				require.Empty(t, output.String())
			case "pending":
				require.ErrorIs(t, err, pending)
				require.Equal(t, ExitCodeRuntime, ExitCode(err), "a wait timeout exits 1")
				require.Empty(t, output.String(), "a pending request prints no receipt")
			case "no-leader":
				require.ErrorIs(t, err, pending)
				require.Contains(t, errOutput.String(), "No gateway leads the fleet. Start a gateway with the new binary. The request stays recorded until 2026-10-02T12:10:00Z.")
			default:
				require.Error(t, err)
				require.Zero(t, calls)
				require.Equal(t, ExitCodeUsage, ExitCode(err))
			}
		})
	}
}

func TestCatalogBaselineStatusCommandReportsBaselines(t *testing.T) {
	deps, output, _ := testDependencies()
	deps.CatalogBaselineStatus = func(_ context.Context, cfg *config.Config) (runtimecatalog.BaselineReport, error) {
		require.NotNil(t, cfg)
		return runtimecatalog.BaselineReport{
			DeploymentID: "deployment", HeadRevision: 7, Promotable: true,
			Packaged: runtimecatalog.BaselineIdentity{GenerationID: "packaged", Checksum: "sha256:new"},
			Retained: runtimecatalog.BaselineIdentity{GenerationID: "retained", Checksum: "sha256:old"},
		}, nil
	}
	require.NoError(t, Run(t.Context(), []string{"starport", "catalog", "baseline-status"}, deps))
	require.Contains(t, output.String(), "Deployment deployment at fleet head revision 7.")
	require.Contains(t, output.String(), "Packaged: generation packaged, checksum sha256:new")
	require.Contains(t, output.String(), "Retained: generation retained, checksum sha256:old")
	require.Contains(t, output.String(), "Promotable: true")

	output.Reset()
	require.NoError(t, Run(t.Context(), []string{"starport", "catalog", "baseline-status", "--json"}, deps))
	require.JSONEq(t, `{"deployment_id":"deployment","head_revision":7,"promotable":true,
		"packaged":{"generation_id":"packaged","checksum":"sha256:new"},
		"retained":{"generation_id":"retained","checksum":"sha256:old"}}`, output.String())

	output.Reset()
	deps.CatalogBaselineStatus = nil
	err := Run(t.Context(), []string{"starport", "catalog", "baseline-status"}, deps)
	require.Equal(t, ExitCodeRuntime, ExitCode(err))
}

func jsonFieldNames(fields map[string]json.RawMessage) []string {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	return names
}
