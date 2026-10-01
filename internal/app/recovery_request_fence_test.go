package app

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/recovery"
	"github.com/agentstation/starport/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestRecoveryRequestPathRefusesFileAdmissionWithoutObservation(t *testing.T) {
	binary := populatedBinary(t)
	f := populatedAdoptionFixture(t)
	f.private = filepath.Join(t.TempDir(), "private")
	require.NoError(t, os.Mkdir(f.private, 0700))
	measurementConfigureListener(t, f)

	store, err := storage.OpenValkey(f.cfg.RuntimeStorage().Valkey)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	accounts, err := account.Open(store)
	require.NoError(t, err)
	keys, err := apikey.Open(store)
	require.NoError(t, err)
	issuer, err := apikey.NewIssuer(keys, apikey.WithAccountChecker(accounts))
	require.NoError(t, err)
	issued, err := issuer.Issue(t.Context(), apikey.IssueRequest{
		Name: "request-fence", AccountID: populatedAccount, Scopes: []string{"files:write"},
	})
	require.NoError(t, err)
	require.Nil(t, issued.APIKey.Limits, "the probe cannot force a budget authority observation")
	owner, err := accounts.GetByID(t.Context(), populatedAccount)
	require.NoError(t, err)
	require.Nil(t, owner.Account.Limits)

	measurementReadiness(t, binary, f, f.private, &measurementRun{})
	base := fmt.Sprintf("http://127.0.0.1:%d", f.cfg.Server.Port)
	client := &http.Client{Timeout: 10 * time.Second}
	upload := func(name string) (int, []byte) {
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		require.NoError(t, form.WriteField("purpose", "user_data"))
		part, err := form.CreateFormFile("file", name+".txt")
		require.NoError(t, err)
		_, err = io.WriteString(part, "request-path admission probe")
		require.NoError(t, err)
		require.NoError(t, form.Close())
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, base+"/v1/files", &body)
		require.NoError(t, err)
		request.Header.Set("Authorization", "Bearer "+issued.Secret)
		request.Header.Set("Content-Type", form.FormDataContentType())
		response, err := client.Do(request)
		require.NoError(t, err, "HTTP probe failed")
		defer func() { require.NoError(t, response.Body.Close()) }()
		raw, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		operatorPrivateJSON(t, f.private, name+"-response.json", map[string][]byte{"body": raw})
		return response.StatusCode, raw
	}

	status, body := upload("before-close")
	require.Equal(t, http.StatusOK, status, "admission must succeed before closure")
	var stored struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(body, &stored))
	require.NotEmpty(t, stored.ID)
	output, err := populatedBinaryRun(t, binary, f, "close", []string{"starport", "backup", "close", operatorJSONFlag})
	require.NoError(t, err, "shipping close failed, private output retained")
	var closed recovery.Record
	require.NoError(t, json.Unmarshal(output, &closed))
	require.False(t, closed.Open)
	require.Equal(t, f.prior.Epoch+1, closed.Epoch)
	require.NoError(t, store.Ping(t.Context()), "the old primary remains reachable")

	// The same gateway receives the next requests without an observe command,
	// an authority check from the test, a readiness poll, or a restart. Its own
	// observation worker must withdraw admission within the observation bound.
	bound := recoveryObservationInterval + recoveryObservationTimeout
	deadline := time.Now().Add(5 * bound)
	probes := 0
	for {
		probes++
		status, body = upload(fmt.Sprintf("after-close-%d", probes))
		if status != http.StatusOK {
			break
		}
		require.Less(t, time.Now(), deadline, "closed deployment must refuse HTTP admission without forced observation; still admitted after %d probes", probes)
		time.Sleep(bound / 10)
	}
	require.Equal(t, http.StatusServiceUnavailable, status, "the first refusal after closure must withdraw admission")
	require.JSONEq(t, `{"error":{"message":"Authorization is temporarily unavailable","type":"server_error"}}`, string(body))
	status, body = upload("after-refusal")
	require.Equal(t, http.StatusServiceUnavailable, status, "admission stays withdrawn after the first refusal")
	require.JSONEq(t, `{"error":{"message":"Authorization is temporarily unavailable","type":"server_error"}}`, string(body))
}
