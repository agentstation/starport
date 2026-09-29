package jobs

import (
	"encoding/json/v2"
	"fmt"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/routing"
	"github.com/stretchr/testify/require"
)

func TestVideoJobReplayPrivateFormatting(t *testing.T) {
	job, err := New("private-job", "private-account", "provider", "provider/model", routing.OperationVideosGenerations, time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, job.AdoptProviderJob("private-provider-id"))
	_, evidence := videoReplayEvidence(t, job)
	intent := CorrectionIntent{Account: "private-account", JobID: "private-job"}
	correction, err := NewRecoveryJobCorrection(intent, false, "")
	require.NoError(t, err)
	for _, value := range []any{evidence, &evidence, correction, &correction} {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%d", "%q", "%x", "%p"} {
			formatted := fmt.Sprintf(verb, value)
			require.NotContains(t, formatted, "private-account")
			require.NotContains(t, formatted, "private-job")
			require.NotContains(t, formatted, "private-provider-id")
			require.NotContains(t, formatted, "[123")
			if verb != "%p" {
				require.Contains(t, formatted, "<private job")
			}
		}
	}
	data, err := json.Marshal(correction)
	require.NoError(t, err)
	intent.Account = "changed-after-construction"
	again, err := json.Marshal(correction)
	require.NoError(t, err)
	require.Equal(t, data, again)
	var roundtrip RecoveryJobCorrection
	require.NoError(t, json.Unmarshal(data, &roundtrip))
	again, err = json.Marshal(roundtrip)
	require.NoError(t, err)
	require.JSONEq(t, string(data), string(again))
	require.Contains(t, string(data), `"intent"`)
	require.Contains(t, string(data), `"applied"`)
	require.NotContains(t, string(data), `"state"`)
}

func TestVideoJobReplayZeroPrivateEvidenceRefuses(t *testing.T) {
	var job RecoveryJob
	_, err := job.MarshalJSON()
	require.Error(t, err)
	var correction RecoveryJobCorrection
	_, err = correction.MarshalJSON()
	require.Error(t, err)
	_, err = PrepareJobCorrectionReplay(t.Context(), videoReplayRecords{}, job, []RecoveryJobCorrection{correction})
	require.Error(t, err)
}
