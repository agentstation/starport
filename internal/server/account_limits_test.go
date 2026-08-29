package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/server/requestctx"
	"github.com/agentstation/starport/internal/usage"
)

// An account limit and a key limit are two meters, not two candidate values for
// one meter. The account meter counts every key in the account, so the account
// cap has to bind on the account's own total; taking the smaller of the two
// numbers would let N keys each spend the key limit and overrun the account
// cap N-fold. These tests hold that separation at both enforcement points.

func accountLimitedRequest(apiKey *apikey.APIKey, account *account.Account) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	ctx := requestctx.WithAPIKeyID(req.Context(), apiKey.ID)
	ctx = requestctx.WithAPIKeyModel(ctx, apiKey)
	ctx = requestctx.WithAccountID(ctx, account.ID)
	ctx = requestctx.WithAccountRecord(ctx, account)
	return req.WithContext(ctx)
}

// TestAccountRequestLimitBindsBelowAKeyLimit covers the operator's ceiling on a
// generous key. The key is allowed a hundred requests a minute; the account it
// belongs to is allowed one. The account has to win, and the response has to
// say which meter refused so the operator can tell an account cap from a key
// cap without reading the configuration back.
func TestAccountRequestLimitBindsBelowAKeyLimit(t *testing.T) {
	server := &Server{
		cfg:        &Config{},
		rateLimits: mustOpenRateLimitRepository(t),
	}

	handler := server.rateLimit(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	account := &account.Account{
		ID:     "acme",
		Name:   "Acme",
		Active: true,
		Limits: &limits.Limits{
			Requests: &limits.RequestLimit{Limit: 1, WindowSeconds: 60},
		},
	}
	apiKey := &apikey.APIKey{
		ID:        "key-generous",
		Name:      "generous",
		AccountID: account.ID,
		Scopes:    []string{"*"},
		Active:    true,
		Limits: &limits.Limits{
			Requests: &limits.RequestLimit{Limit: 100, WindowSeconds: 60},
		},
	}

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, accountLimitedRequest(apiKey, account))
	require.Equal(t, http.StatusOK, first.Code)
	assert.Equal(t, "1", first.Header().Get("X-RateLimit-Limit"),
		"the account cap is the binding meter, so it owns the reported numbers")
	assert.Equal(t, "account", first.Header().Get("X-RateLimit-Scope"))

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, accountLimitedRequest(apiKey, account))
	require.Equal(t, http.StatusTooManyRequests, second.Code)
	assert.Equal(t, "account", second.Header().Get("X-RateLimit-Scope"))
	assert.Contains(t, second.Body.String(), "account")
}

// TestAAccountRequestLimitCountsEveryKeyInTheAccount is the case a stricter-of
// resolution gets wrong. Two keys, each well inside its own limit, together
// exceed the account's. The account meter is shared, so the second key's first
// request is refused.
func TestAAccountRequestLimitCountsEveryKeyInTheAccount(t *testing.T) {
	server := &Server{
		cfg:        &Config{},
		rateLimits: mustOpenRateLimitRepository(t),
	}

	handler := server.rateLimit(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	account := &account.Account{
		ID:     "acme",
		Name:   "Acme",
		Active: true,
		Limits: &limits.Limits{
			Requests: &limits.RequestLimit{Limit: 1, WindowSeconds: 60},
		},
	}
	keyLimits := &limits.Limits{Requests: &limits.RequestLimit{Limit: 10, WindowSeconds: 60}}
	first := &apikey.APIKey{
		ID: "key-one", Name: "one", AccountID: account.ID,
		Scopes: []string{"*"}, Active: true, Limits: keyLimits,
	}
	second := &apikey.APIKey{
		ID: "key-two", Name: "two", AccountID: account.ID,
		Scopes: []string{"*"}, Active: true, Limits: keyLimits,
	}

	allowed := httptest.NewRecorder()
	handler.ServeHTTP(allowed, accountLimitedRequest(first, account))
	require.Equal(t, http.StatusOK, allowed.Code)

	refused := httptest.NewRecorder()
	handler.ServeHTTP(refused, accountLimitedRequest(second, account))
	require.Equal(t, http.StatusTooManyRequests, refused.Code,
		"a second key spends the same account allowance the first key drew on")
}

// TestAKeyLimitStillBindsUnderAGenerousAccount is the mirror case. Both meters
// run, so a key cap below the account cap still refuses its own key without
// touching what the rest of the account may spend.
func TestAKeyLimitStillBindsUnderAGenerousAccount(t *testing.T) {
	server := &Server{
		cfg:        &Config{},
		rateLimits: mustOpenRateLimitRepository(t),
	}

	handler := server.rateLimit(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	account := &account.Account{
		ID: "acme", Name: "Acme", Active: true,
		Limits: &limits.Limits{Requests: &limits.RequestLimit{Limit: 100, WindowSeconds: 60}},
	}
	restricted := &apikey.APIKey{
		ID: "key-restricted", Name: "restricted", AccountID: account.ID,
		Scopes: []string{"*"}, Active: true,
		Limits: &limits.Limits{Requests: &limits.RequestLimit{Limit: 1, WindowSeconds: 60}},
	}
	sibling := &apikey.APIKey{
		ID: "key-sibling", Name: "sibling", AccountID: account.ID,
		Scopes: []string{"*"}, Active: true,
	}

	allowed := httptest.NewRecorder()
	handler.ServeHTTP(allowed, accountLimitedRequest(restricted, account))
	require.Equal(t, http.StatusOK, allowed.Code)
	assert.Equal(t, "key", allowed.Header().Get("X-RateLimit-Scope"),
		"the key cap has the least remaining, so it owns the reported numbers")

	refused := httptest.NewRecorder()
	handler.ServeHTTP(refused, accountLimitedRequest(restricted, account))
	require.Equal(t, http.StatusTooManyRequests, refused.Code)
	assert.Equal(t, "key", refused.Header().Get("X-RateLimit-Scope"))

	sibs := httptest.NewRecorder()
	handler.ServeHTTP(sibs, accountLimitedRequest(sibling, account))
	require.Equal(t, http.StatusOK, sibs.Code,
		"one key exhausting its own cap must not spend the account's")
}

// scopedUsageTotals serves a different total per aggregate scope so a test can
// put an account over its cap while every one of its keys stays under its own.
type scopedUsageTotals struct {
	byScope map[usage.Scope]usage.Totals
}

func (s scopedUsageTotals) Put(context.Context, usage.Record) error { return nil }

func (s scopedUsageTotals) List(context.Context, usage.Query) (usage.Page, error) {
	return usage.Page{}, nil
}

func (s scopedUsageTotals) Totals(_ context.Context, scope usage.Scope, _ string, _ time.Time) (usage.Totals, error) {
	return s.byScope[scope], nil
}

// TestAccountSpendBindsOnTheAccountTotal is the budget half of the same rule.
// The key has spent a tenth of its own allowance; the account it belongs to is
// over its cap because its other keys spent the rest. The request is refused
// on the account's total, and the failure names the account.
func TestAccountSpendBindsOnTheAccountTotal(t *testing.T) {
	account := &account.Account{
		ID: "acme", Name: "Acme", Active: true,
		Limits: &limits.Limits{
			Spend: &limits.Budget{Limit: 1_000_000_000, Interval: limits.IntervalMonth},
		},
	}
	apiKey := &apikey.APIKey{
		ID: "key-modest", Name: "modest", AccountID: account.ID,
		Scopes: []string{"*"}, Active: true,
		Limits: &limits.Limits{
			Spend: &limits.Budget{Limit: 1_000_000_000, Interval: limits.IntervalDay},
		},
	}

	server := &Server{
		cfg: &Config{},
		usage: scopedUsageTotals{byScope: map[usage.Scope]usage.Totals{
			usage.KeyScope(apiKey.ID):      {SpendNanoUSD: 100_000_000},
			usage.AccountScope(account.ID): {SpendNanoUSD: 4_000_000_000},
		}},
	}

	handler := server.enforceBudgets(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, accountLimitedRequest(apiKey, account))

	require.Equal(t, http.StatusPaymentRequired, rec.Code)
	assert.Contains(t, rec.Body.String(), "account")
	assert.Equal(t, "account", rec.Header().Get("X-Starport-Budget-Spend-Scope"))
	assert.Equal(t, "0", rec.Header().Get("X-Starport-Budget-Spend-Remaining"))
}

// TestAKeyBudgetStillBindsUnderAGenerousAccount holds the mirror case for
// spend: the account has room, the key does not, and the key's own meter
// refuses without consuming the account's remaining allowance.
func TestAKeyBudgetStillBindsUnderAGenerousAccount(t *testing.T) {
	account := &account.Account{
		ID: "acme", Name: "Acme", Active: true,
		Limits: &limits.Limits{
			Spend: &limits.Budget{Limit: 10_000_000_000, Interval: limits.IntervalMonth},
		},
	}
	apiKey := &apikey.APIKey{
		ID: "key-spent", Name: "spent", AccountID: account.ID,
		Scopes: []string{"*"}, Active: true,
		Limits: &limits.Limits{
			Spend: &limits.Budget{Limit: 1_000_000_000, Interval: limits.IntervalDay},
		},
	}

	server := &Server{
		cfg: &Config{},
		usage: scopedUsageTotals{byScope: map[usage.Scope]usage.Totals{
			usage.KeyScope(apiKey.ID):      {SpendNanoUSD: 2_000_000_000},
			usage.AccountScope(account.ID): {SpendNanoUSD: 2_000_000_000},
		}},
	}

	handler := server.enforceBudgets(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, accountLimitedRequest(apiKey, account))

	require.Equal(t, http.StatusPaymentRequired, rec.Code)
	assert.Equal(t, "key", rec.Header().Get("X-Starport-Budget-Spend-Scope"))
}
