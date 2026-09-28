package app

import (
	"encoding/json/v2"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/limits/reservation"
	"github.com/stretchr/testify/require"
)

func TestProductionAdministratorVideoReconciliation(t *testing.T) {
	for _, disposition := range []string{"no_charge", "usage"} {
		t.Run(disposition, func(t *testing.T) {
			fixture := newPerformanceFixtureForProviders(t, 0, nil, true,
				&limits.Limits{Spend: &limits.Budget{Limit: 375000000, Interval: limits.IntervalDay}},
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					http.Error(w, "provider response unavailable", http.StatusBadGateway)
				}),
				[]performanceProvider{{catalogs.ProviderIDDeepInfra, "DEEPINFRA_TOKEN", "Authorization", "Bearer sk-test-key", []string{"/inference/Wan-AI/Wan2.2-T2V-A14B"}}},
				func(key *apikey.APIKey) { key.Scopes = []string{"*"} })
			call := func(method, path, payload string) (int, []byte) {
				t.Helper()
				req, err := http.NewRequestWithContext(t.Context(), method, fixture.gateway.URL+path, strings.NewReader(payload))
				require.NoError(t, err)
				req.Header.Set("Authorization", "Bearer "+performanceGatewayKey)
				req.Header.Set("Content-Type", "application/json")
				resp, err := fixture.client.Do(req)
				require.NoError(t, err)
				data, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				require.NoError(t, resp.Body.Close())
				select {
				case <-fixture.handlers:
				default:
				}
				return resp.StatusCode, data
			}
			code, body := call(http.MethodPost, "/v1/videos", `{"model":"deepinfra/Wan-AI/Wan2.2-T2V-A14B","prompt":"landscape"}`)
			require.Equal(t, http.StatusOK, code, string(body))
			var submitted struct {
				ID string `json:"id"`
			}
			require.NoError(t, json.Unmarshal(body, &submitted))
			// Wait for the sole provider attempt to end before supplying external evidence.
			var attempt reservation.Record
			require.Eventually(t, func() bool {
				keys, err := fixture.application.store.ScanWithPrefix(t.Context(), "budget:v1:attempt:", 10)
				if err != nil || len(keys) != 1 {
					return false
				}
				data, err := fixture.application.store.Get(t.Context(), keys[0])
				return err == nil && json.Unmarshal(data, &attempt) == nil && attempt.State == reservation.Uncertain
			}, 5*time.Second, 10*time.Millisecond)
			require.NoError(t, fixture.application.jobs.Close(t.Context()))
			path := "/api/v1/admin/accounts/" + attempt.Attempt.AccountID + "/videos/" + submitted.ID + "/reconciliation"
			code, body = call(http.MethodGet, path, "")
			require.Equal(t, http.StatusOK, code, string(body))
			var view jobs.ReconciliationView
			require.NoError(t, json.Unmarshal(body, &view))
			require.Equal(t, "evidence_required", view.Status)
			require.Equal(t, attempt.Attempt.ID, view.ReservationID)
			request := jobs.ReconciliationRequest{DecisionID: "case-42", Binding: view.Binding, Disposition: disposition, EvidenceReference: "private-provider-invoice:case-42", Reason: "Provider usage reviewed"}
			// The inspected valuation defines the exact unit vocabulary.
			if disposition == "usage" {
				request.Quantities = reservation.Quantities{view.Valuation.Components[0].Unit: 5}
			}
			encoded, err := json.Marshal(request)
			require.NoError(t, err)
			code, body = call(http.MethodPost, path, string(encoded))
			require.Equal(t, http.StatusOK, code, string(body))
			var accepted jobs.ReconciliationView
			require.NoError(t, json.Unmarshal(body, &accepted))
			require.Equal(t, "key:"+attempt.Attempt.KeyID, accepted.Decision.Actor)
			require.Equal(t, request, accepted.Decision.ReconciliationRequest)
			require.True(t, accepted.Accounted)
			code, body = call(http.MethodPost, path, string(encoded))
			require.Equal(t, http.StatusOK, code, string(body))
			var replay jobs.ReconciliationView
			require.NoError(t, json.Unmarshal(body, &replay))
			require.Equal(t, accepted, replay)
			keys, err := fixture.application.store.ScanWithPrefix(t.Context(), "budget:v1:attempt:", 10)
			require.NoError(t, err)
			data, err := fixture.application.store.Get(t.Context(), keys[0])
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(data, &attempt))
			require.Equal(t, reservation.Settled, attempt.State)
			require.Equal(t, disposition == "no_charge", attempt.Evidence.NoCharge)
			expectedCost := int64(375000000)
			if disposition == "no_charge" {
				expectedCost = 0
			}
			require.Equal(t, expectedCost, *attempt.NanoUSD)
			for _, prefix := range []string{"/v1", "/api/v1"} {
				code, body = call(http.MethodGet, prefix+"/videos/"+submitted.ID, "")
				require.Equal(t, http.StatusOK, code, string(body))
				require.Contains(t, string(body), "administrator_resolved")
				require.NotContains(t, string(body), request.EvidenceReference)
				require.NotContains(t, string(body), request.Reason)
			}
			code, body = call(http.MethodGet, path, "")
			require.Equal(t, http.StatusOK, code, string(body))
			require.NoError(t, json.Unmarshal(body, &view))
			require.NotEmpty(t, view.CorrectionBinding)
			revised := jobs.ReconciliationRequest{DecisionID: "correction-42", Binding: view.CorrectionBinding, Disposition: "usage", Quantities: reservation.Quantities{view.Valuation.Components[0].Unit: 2}, EvidenceReference: "private-correction-invoice", Reason: "Revised provider invoice"}
			correctionBody, err := json.Marshal(revised)
			require.NoError(t, err)
			code, body = call(http.MethodPost, path+"/corrections", string(correctionBody))
			require.Equal(t, http.StatusOK, code, string(body))
			var corrected jobs.ReconciliationView
			require.NoError(t, json.Unmarshal(body, &corrected))
			require.Equal(t, accepted.Decision, corrected.Decision)
			require.Equal(t, "correction-42", corrected.AppliedCorrectionID)
			require.Equal(t, "correction-42", corrected.ReportedCorrectionID)
			require.True(t, corrected.Correction.Applied)
			code, body = call(http.MethodPost, path+"/corrections", string(correctionBody))
			require.Equal(t, http.StatusOK, code, string(body))
			revisedRecord, err := fixture.application.budget.ledger.Inspect(t.Context(), attempt.Attempt.ID)
			require.NoError(t, err)
			require.EqualValues(t, 150000000, *revisedRecord.NanoUSD)
			code, body = call(http.MethodGet, path+"/corrections/correction-42", "")
			require.Equal(t, http.StatusOK, code, string(body))
			require.Contains(t, string(body), "private-correction-invoice")
			code, body = call(http.MethodGet, "/v1/videos/"+submitted.ID, "")
			require.Equal(t, http.StatusOK, code, string(body))
			require.NotContains(t, string(body), "private-correction-invoice")
			require.NotContains(t, string(body), "Revised provider invoice")
			revised.Reason = "changed correction"
			changed, err := json.Marshal(revised)
			require.NoError(t, err)
			code, body = call(http.MethodPost, path+"/corrections", string(changed))
			require.Equal(t, http.StatusConflict, code, string(body))
			request.Reason = "changed evidence"
			encoded, err = json.Marshal(request)
			require.NoError(t, err)
			code, body = call(http.MethodPost, path, string(encoded))
			require.Equal(t, http.StatusConflict, code, string(body))
			require.EqualValues(t, 1, fixture.calls.Load(), "manual recovery cannot submit inference")
		})
	}
}

func TestLateNativeResponseBlocksDisputedBudget(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	fixture := newPerformanceFixtureForProviders(t, 0, nil, true,
		&limits.Limits{Spend: &limits.Budget{Limit: 375000000, Interval: limits.IntervalDay}},
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"request_id":"private-late-response","inference_status":{"output_length":5},"video_url":"data:video/mp4;base64,dmlkZW8="}`)
		}),
		[]performanceProvider{{catalogs.ProviderIDDeepInfra, "DEEPINFRA_TOKEN", "Authorization", "Bearer sk-test-key", []string{"/inference/Wan-AI/Wan2.2-T2V-A14B"}}}, nil)
	submit := func() (int, []byte) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, fixture.gateway.URL+"/v1/videos", strings.NewReader(`{"model":"deepinfra/Wan-AI/Wan2.2-T2V-A14B","prompt":"landscape"}`))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+performanceGatewayKey)
		req.Header.Set("Content-Type", "application/json")
		resp, err := fixture.client.Do(req)
		require.NoError(t, err)
		data, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		select {
		case <-fixture.handlers:
		default:
		}
		return resp.StatusCode, data
	}
	status, body := submit()
	require.Equal(t, http.StatusOK, status, string(body))
	var object struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(body, &object))
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("provider dispatch did not start")
	}
	service := fixture.application.jobs
	view, err := service.InspectReconciliation(t.Context(), "default", object.ID)
	require.NoError(t, err)
	decision := jobs.ReconciliationRequest{DecisionID: "manual", Binding: view.Binding, EvidenceReference: "provider-invoice", Reason: "Provider initially reported no charge", Disposition: "no_charge"}
	original, err := service.ReconcileAdministrator(t.Context(), "default", object.ID, "key:admin", decision)
	require.NoError(t, err)
	records, err := jobs.OpenRepository(fixture.application.store)
	require.NoError(t, err)
	inspected, err := records.Get(t.Context(), "default", object.ID)
	require.NoError(t, err)
	budgetBinding, err := fixture.application.budget.CorrectionBudgetBinding(t.Context(), inspected)
	require.NoError(t, err)
	pendingRequest := jobs.ReconciliationRequest{DecisionID: "before-late", Binding: inspected.CorrectionBinding(budgetBinding), EvidenceReference: "initial-invoice", Reason: "Initial invoice reviewed", Disposition: "no_charge"}
	pendingIntent, err := inspected.NewCorrection(pendingRequest, "key:admin", budgetBinding, time.Now())
	require.NoError(t, err)
	_, err = records.CreateCorrection(t.Context(), inspected, pendingIntent)
	require.NoError(t, err)
	close(release)
	released = true
	require.Eventually(t, func() bool {
		current, err := service.InspectReconciliation(t.Context(), "default", object.ID)
		return err == nil && current.Status == "correction_review_required"
	}, 5*time.Second, 10*time.Millisecond)
	current, err := service.InspectReconciliation(t.Context(), "default", object.ID)
	require.NoError(t, err)
	require.Equal(t, original.Decision, current.Decision)
	require.False(t, current.Correction.Applied)
	require.NotNil(t, current.LateProviderEvidence)
	record, err := fixture.application.budget.ledger.Inspect(t.Context(), view.ReservationID)
	require.NoError(t, err)
	require.Equal(t, "job:"+object.ID, record.DisputeID)
	require.True(t, record.Evidence.NoCharge)
	require.Zero(t, *record.NanoUSD)
	status, body = submit()
	require.Equal(t, http.StatusServiceUnavailable, status, string(body))
	view, err = service.InspectReconciliation(t.Context(), "default", object.ID)
	require.NoError(t, err)
	correction := jobs.ReconciliationRequest{DecisionID: "accept-late", Binding: view.CorrectionBinding, EvidenceReference: "reviewed-late-invoice", Reason: "Provider invoice confirms actual usage", Disposition: "usage", Quantities: current.LateProviderEvidence.Measurement.Quantities}
	resolved, err := service.CorrectAdministrator(t.Context(), "default", object.ID, "key:admin", correction)
	require.NoError(t, err)
	require.Equal(t, original.Decision, resolved.Decision)
	require.Equal(t, current.LateProviderEvidence, resolved.LateProviderEvidence)
	corrected, err := fixture.application.budget.ledger.Inspect(t.Context(), view.ReservationID)
	require.NoError(t, err)
	require.Empty(t, corrected.DisputeID)
	require.EqualValues(t, 375000000, *corrected.NanoUSD)
	_, err = service.CorrectAdministrator(t.Context(), "default", object.ID, "key:admin", correction)
	require.NoError(t, err)
	require.EqualValues(t, 1, fixture.calls.Load())
}
