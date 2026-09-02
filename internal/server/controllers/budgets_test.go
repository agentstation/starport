package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/storage"
	"github.com/agentstation/starport/internal/usage"
)

// budgetEntryKeys are what every budget meter reports, on a key, an
// account, or a team alike: the ceiling, the interval, the window's
// consumption and remainder, and the window's two edges.
var budgetEntryKeys = []string{
	fieldLimit, fieldInterval, fieldBudgetUsed, fieldBudgetRemaining,
	fieldBudgetWindowStart, fieldBudgetWindowEnd,
}

func newBudgetTestUsage(t *testing.T) usage.Repository {
	t.Helper()
	store := storage.NewMockStore()
	t.Cleanup(func() { _ = store.Close() })
	records, err := usage.Open(store, usage.Options{})
	require.NoError(t, err)
	return records
}

// budgetTestSpend records one priced request against the named holders so
// the meter under test has something to read.
func budgetTestSpend(t *testing.T, records usage.Repository, accountID, teamID string, nanoUSD int64) {
	t.Helper()
	require.NoError(t, records.Put(context.Background(), usage.Record{
		RequestID:      "req-" + accountID + "-" + teamID,
		KeyID:          "key-1",
		AccountID:      accountID,
		TeamID:         teamID,
		Timestamp:      time.Now().UTC(),
		Protocol:       "openrouter",
		Operation:      usage.OperationChat,
		ModelRequested: "m",
		ModelUsed:      "m",
		Provider:       "p",
		Status:         usage.StatusOK,
		StatusCode:     http.StatusOK,
		Tokens:         usage.Tokens{Input: 10, Output: 5, Total: 15},
		Attempts:       1,
		Cost:           &usage.Cost{NanoUSD: nanoUSD, Currency: "USD"},
	}))
}

// TestTeamReadIncludesBudgetUsage holds a team read to the meter shape: a
// team with a budget reports what its keys spent in the current window and
// what is left, and a team without one reports no budgets block at all.
func TestTeamReadIncludesBudgetUsage(t *testing.T) {
	controller, _ := newMembersTestController(t)
	controller.usage = newBudgetTestUsage(t)
	router := membersTestRouter(controller)

	created := membersTestCall(router, http.MethodPost, "/teams",
		`{"name":"Platform","budget":{"limit":10000000000,"interval":"month"}}`)
	require.Equal(t, http.StatusCreated, created.Code)
	var team struct {
		ID       string                    `json:"id"`
		Revision uint64                    `json:"revision"`
		Budgets  map[string]map[string]any `json:"budgets"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &team))
	require.NotEmpty(t, team.ID)
	assert.NotZero(t, team.Revision, "a team read names the revision a later update may state")

	budgetTestSpend(t, controller.usage, "acct-1", team.ID, 8_000_000_000)

	listed := membersTestCall(router, http.MethodGet, "/teams", "")
	require.Equal(t, http.StatusOK, listed.Code)
	var page struct {
		Teams []struct {
			ID      string                    `json:"id"`
			Budgets map[string]map[string]any `json:"budgets"`
		} `json:"teams"`
	}
	require.NoError(t, json.Unmarshal(listed.Body.Bytes(), &page))
	require.Len(t, page.Teams, 1)
	spend := page.Teams[0].Budgets[fieldSpend]
	require.NotNil(t, spend, "a budgeted team reports its spend meter")
	for _, key := range budgetEntryKeys {
		assert.Contains(t, spend, key)
	}
	assert.EqualValues(t, 10_000_000_000, spend[fieldLimit])
	assert.Equal(t, usage.IntervalMonth, spend[fieldInterval])
	assert.EqualValues(t, 8_000_000_000, spend[fieldBudgetUsed])
	assert.EqualValues(t, 2_000_000_000, spend[fieldBudgetRemaining])
	start, err := time.Parse(time.RFC3339, spend[fieldBudgetWindowStart].(string))
	require.NoError(t, err)
	end, err := time.Parse(time.RFC3339, spend[fieldBudgetWindowEnd].(string))
	require.NoError(t, err)
	assert.True(t, end.After(start), "the window ends after it starts")

	// An unmetered team carries no budgets block rather than an empty one.
	plain := membersTestCall(router, http.MethodPost, "/teams", `{"name":"Research"}`)
	require.Equal(t, http.StatusCreated, plain.Code)
	var unmetered map[string]any
	require.NoError(t, json.Unmarshal(plain.Body.Bytes(), &unmetered))
	_, present := unmetered["budgets"]
	assert.False(t, present)
}

// TestTeamBudgetWithoutUsageStoreReadsUnavailable states the answer a
// deployment with no usage store gives: the ceiling still travels, and the
// consumption is named as unavailable rather than reported as zero.
func TestTeamBudgetWithoutUsageStoreReadsUnavailable(t *testing.T) {
	controller, _ := newMembersTestController(t)
	router := membersTestRouter(controller)

	created := membersTestCall(router, http.MethodPost, "/teams",
		`{"name":"Platform","budget":{"limit":5000000000,"interval":"day"}}`)
	require.Equal(t, http.StatusCreated, created.Code)
	var team struct {
		Budgets map[string]map[string]any `json:"budgets"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &team))
	spend := team.Budgets[fieldSpend]
	require.NotNil(t, spend)
	assert.Equal(t, systemInfoUnavailable, spend[fieldError])
	assert.EqualValues(t, 5_000_000_000, spend[fieldLimit])
	assert.NotContains(t, spend, fieldBudgetUsed)
}

// TestTeamUpdateRejectsStaleRevision holds the update to the revision it
// names: a stale one is refused before anything is written, a current one
// lands, and an absent one keeps the unconditional update.
func TestTeamUpdateRejectsStaleRevision(t *testing.T) {
	controller, repositories := newMembersTestController(t)
	router := membersTestRouter(controller)

	created := membersTestCall(router, http.MethodPost, "/teams", `{"name":"Platform"}`)
	require.Equal(t, http.StatusCreated, created.Code)
	var team struct {
		ID       string `json:"id"`
		Revision uint64 `json:"revision"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &team))

	// Another operator renames the team between this one's read and write.
	record, err := repositories.Teams.GetByID(context.Background(), team.ID)
	require.NoError(t, err)
	renamed := record.Team
	renamed.Name = "Platform Engineering"
	_, err = repositories.Teams.Update(context.Background(), renamed, record.Revision)
	require.NoError(t, err)

	stale := membersTestCall(router, http.MethodPut, "/teams/"+team.ID,
		`{"name":"Platform","budget":{"limit":1000000000,"interval":"week"},"revision":`+
			jsonUint(team.Revision)+`}`)
	require.Equal(t, http.StatusConflict, stale.Code)
	assert.Contains(t, stale.Body.String(), "Team changed since it was read")

	// The rename survived: the stale write touched nothing.
	current, err := repositories.Teams.GetByID(context.Background(), team.ID)
	require.NoError(t, err)
	assert.Equal(t, "Platform Engineering", current.Team.Name)
	assert.Nil(t, current.Team.Budget)

	fresh := membersTestCall(router, http.MethodPut, "/teams/"+team.ID,
		`{"name":"Platform Engineering","budget":{"limit":1000000000,"interval":"week"},"revision":`+
			jsonUint(current.Revision)+`}`)
	require.Equal(t, http.StatusOK, fresh.Code)
	var updated struct {
		Revision uint64 `json:"revision"`
	}
	require.NoError(t, json.Unmarshal(fresh.Body.Bytes(), &updated))
	assert.Greater(t, updated.Revision, current.Revision)

	// No revision keeps the unconditional update: the body states the team whole.
	blind := membersTestCall(router, http.MethodPut, "/teams/"+team.ID, `{"name":"Platform"}`)
	require.Equal(t, http.StatusOK, blind.Code)
	final, err := repositories.Teams.GetByID(context.Background(), team.ID)
	require.NoError(t, err)
	assert.Equal(t, "Platform", final.Team.Name)
	assert.Nil(t, final.Team.Budget, "an omitted budget still clears it")
}

func jsonUint(value uint64) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

// TestAccountReadIncludesBudgetUsage holds an account read to the same meter
// shape a team reports, summed over every key the account holds.
func TestAccountReadIncludesBudgetUsage(t *testing.T) {
	controller, _, _ := newAccountsTestController(t)
	controller.usage = newBudgetTestUsage(t)
	router := accountsTestRouter(controller)

	created := accountsTestCall(router, http.MethodPost, "/accounts",
		`{"id":"acme","name":"Acme","limits":{"spend":{"limit":4000000000,"interval":"day"},"tokens":{"limit":1000,"interval":"month"}}}`)
	require.Equal(t, http.StatusCreated, created.Code)

	budgetTestSpend(t, controller.usage, "acme", "", 1_000_000_000)

	fetched := accountsTestCall(router, http.MethodGet, "/accounts/acme", "")
	require.Equal(t, http.StatusOK, fetched.Code)
	var body struct {
		account.Account
		Budgets map[string]map[string]any `json:"budgets"`
	}
	require.NoError(t, json.Unmarshal(fetched.Body.Bytes(), &body))
	assert.Equal(t, "acme", body.ID)

	spend := body.Budgets[fieldSpend]
	require.NotNil(t, spend)
	for _, key := range budgetEntryKeys {
		assert.Contains(t, spend, key)
	}
	assert.EqualValues(t, 1_000_000_000, spend[fieldBudgetUsed])
	assert.EqualValues(t, 3_000_000_000, spend[fieldBudgetRemaining])

	tokens := body.Budgets[fieldTokens]
	require.NotNil(t, tokens)
	assert.EqualValues(t, 15, tokens[fieldBudgetUsed])
	assert.EqualValues(t, 985, tokens[fieldBudgetRemaining])

	// The listing carries the same meter, so the table can draw it without
	// a detail read per row.
	listed := accountsTestCall(router, http.MethodGet, "/accounts", "")
	require.Equal(t, http.StatusOK, listed.Code)
	var page struct {
		Accounts []struct {
			ID      string                    `json:"id"`
			Budgets map[string]map[string]any `json:"budgets"`
		} `json:"accounts"`
	}
	require.NoError(t, json.Unmarshal(listed.Body.Bytes(), &page))
	for _, item := range page.Accounts {
		if item.ID == "acme" {
			assert.EqualValues(t, 1_000_000_000, item.Budgets[fieldSpend][fieldBudgetUsed])
			return
		}
	}
	t.Fatal("the listing did not include the acme account")
}
