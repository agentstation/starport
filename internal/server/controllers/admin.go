package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/events"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/providers/keyring"
	"github.com/agentstation/starport/internal/server/dto"
	"github.com/agentstation/starport/internal/usage"
)

const systemInfoUnavailable = "unavailable"

// AdminController handles administrative endpoints
type AdminController struct {
	apiKeys      apikey.Repository
	accounts     account.Repository
	issuer       *apikey.Issuer
	usageRecords usage.Repository
	fileBackend  string
	audit        AuditRecorder
	events       EventEmitter
}

// AdminOption adjusts what the admin surface reports.
type AdminOption func(*AdminController)

// WithFileStorage names the blob backend the deployment writes stored files
// to. An operator reads it on the settings view to confirm where the bytes
// land, which is the one fact about file storage that no route reveals.
func WithFileStorage(backend string) AdminOption {
	return func(c *AdminController) { c.fileBackend = backend }
}

// NewAdminController creates a new admin controller. The account repository
// lets the issuer refuse a key that names an account that does not exist.
func NewAdminController(
	apiKeys apikey.Repository,
	accounts account.Repository,
	usageRecords usage.Repository,
	options ...AdminOption,
) *AdminController {
	issuerOptions := []apikey.IssuerOption{}
	if accounts != nil {
		issuerOptions = append(issuerOptions, apikey.WithAccountChecker(accounts))
	}
	issuer, _ := apikey.NewIssuer(apiKeys, issuerOptions...)
	controller := &AdminController{
		apiKeys:      apiKeys,
		accounts:     accounts,
		issuer:       issuer,
		usageRecords: usageRecords,
	}
	for _, option := range options {
		option(controller)
	}
	return controller
}

// Key list pagination bounds.
const (
	keyListDefaultLimit = 100
	keyListMaxLimit     = 1000
)

// ListKeys handles GET /api/v1/admin/keys
func (h *AdminController) ListKeys(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit, err := positiveQueryInt(r, "limit", keyListDefaultLimit, keyListMaxLimit)
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, err.Error())
		return
	}
	offset, err := positiveQueryInt(r, "offset", 0, math.MaxInt)
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, err.Error())
		return
	}

	// One extra record proves or disproves a following page.
	records, err := h.apiKeys.List(ctx, limit+1, offset)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list API keys")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to list API keys")
		return
	}
	hasMore := len(records) > limit
	if hasMore {
		records = records[:limit]
	}

	apiKeys := make([]apikey.APIKey, 0, len(records))
	for _, record := range records {
		apiKey := record.APIKey
		apiKey.Hash = ""
		// The listing reports the account the key actually runs under, so a
		// caller never has to know that an unset value means the canonical one.
		apiKey.AccountID = apiKey.EffectiveAccountID()
		apiKeys = append(apiKeys, apiKey)
	}

	response := map[string]any{
		"keys":                  apiKeys,
		responseCountField:      len(apiKeys),
		responsePaginationField: paginationEnvelope(limit, offset, hasMore),
	}

	if err := dto.WriteJSON(w, http.StatusOK, response); err != nil {
		log.Error().Err(err).Msg("Failed to write response")
	}
}

// positiveQueryInt parses one non-negative integer query parameter.
func positiveQueryInt(r *http.Request, name string, fallback, ceiling int) (int, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", name)
	}
	if value > ceiling {
		return 0, fmt.Errorf("%s must not exceed %d", name, ceiling)
	}
	return value, nil
}

// CreateKey handles POST /api/v1/admin/keys
func (h *AdminController) CreateKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		// AccountID names the owning account. An empty value issues the key to
		// the canonical account.
		AccountID string `json:"account_id,omitempty"`
		// TeamID attributes the key to one team, so the team's budget meters
		// the key's spend. An empty value leaves the key teamless.
		TeamID        string            `json:"team_id,omitempty"`
		Scopes        []string          `json:"scopes,omitempty"`
		AllowedModels []string          `json:"allowed_models,omitempty"`
		Limits        *limits.Limits    `json:"limits,omitempty"`
		ExpiresAt     *time.Time        `json:"expires_at,omitempty"`
		Metadata      map[string]string `json:"metadata,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Invalid request body")
		return
	}
	if _, err := keyring.ParseStrategy(req.Metadata[keyring.StrategyMetadataKey]); err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, err.Error())
		return
	}
	if req.Limits.IsZero() {
		req.Limits = nil
	}

	issued, err := h.issuer.Issue(ctx, apikey.IssueRequest{
		Name:          req.Name,
		AccountID:     req.AccountID,
		TeamID:        req.TeamID,
		Scopes:        req.Scopes,
		AllowedModels: req.AllowedModels,
		Limits:        req.Limits,
		ExpiresAt:     req.ExpiresAt,
		Metadata:      convertStringMapToInterface(req.Metadata),
	})
	subject := req.Name
	if err == nil {
		subject = issued.APIKey.ID
	}
	writeAudit(ctx, h.audit, "key.create", subject, err)
	if err == nil && h.events != nil {
		// The payload names the key. The token itself never leaves the
		// response that answers this one request.
		h.events.Emit(events.TypeKeyCreated, map[string]string{
			fieldKeyID: issued.APIKey.ID, fieldName: req.Name,
		})
	}
	if err != nil {
		if isKeyValidationError(err) {
			dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, err.Error())
			return
		}
		log.Error().Err(err).Msg("Failed to create the API key")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to create API key")
		return
	}
	apiKey := issued.APIKey

	// Return created key (with the actual key value visible once)
	response := map[string]any{
		"key": map[string]any{
			"id":             apiKey.ID,
			"key":            issued.Secret,
			fieldName:        apiKey.Name,
			fieldAccountID:   apiKey.EffectiveAccountID(),
			"team_id":        apiKey.TeamID,
			"scopes":         apiKey.Scopes,
			"allowed_models": apiKey.AllowedModels,
			"limits":         apiKey.Limits,
			"expires_at":     apiKey.ExpiresAt,
			"active":         apiKey.Active,
			fieldCreatedAt:   apiKey.CreatedAt,
		},
		responseMessageField: "API key created successfully. Save the key value as it won't be shown again.",
	}

	if err := dto.WriteJSON(w, http.StatusCreated, response); err != nil {
		log.Error().Err(err).Msg("Failed to write response")
	}
}

// GetKey handles GET /api/v1/admin/keys/{key_id}
func (h *AdminController) GetKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	keyID := chi.URLParam(r, "key_id")

	record, err := h.apiKeys.GetByID(ctx, keyID)
	if err != nil {
		if errors.Is(err, apikey.ErrNotFound) {
			dto.WriteError(w, http.StatusNotFound, dto.ErrorTypeNotFound, "API key not found")
			return
		}
		log.Error().Err(err).Str("key_id", keyID).Msg("Failed to get API key")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to get API key")
		return
	}

	apiKey := record.APIKey
	apiKey.Hash = ""

	response := map[string]any{
		"id":             apiKey.ID,
		fieldName:        apiKey.Name,
		fieldAccountID:   apiKey.EffectiveAccountID(),
		"scopes":         apiKey.Scopes,
		"allowed_models": apiKey.AllowedModels,
		"limits":         apiKey.Limits,
		"metadata":       apiKey.Metadata,
		"active":         apiKey.Active,
		fieldCreatedAt:   apiKey.CreatedAt,
		"expires_at":     apiKey.ExpiresAt,
		"usage":          h.keyUsage(ctx, apiKey),
	}

	if err := dto.WriteJSON(w, http.StatusOK, response); err != nil {
		log.Error().Err(err).Msg("Failed to write response")
	}
}

// keyUsage reads the key's consumption for the current fixed UTC windows
// and, for each configured budget, the remaining allowance.
func (h *AdminController) keyUsage(ctx context.Context, apiKey apikey.APIKey) map[string]any {
	if h.usageRecords == nil {
		return nil
	}
	now := time.Now().UTC()
	windows := map[string]any{}
	totalsByInterval := map[string]usage.Totals{}
	for _, interval := range []string{usage.IntervalDay, usage.IntervalWeek, usage.IntervalMonth} {
		totals, err := h.usageRecords.Totals(ctx, usage.KeyScope(apiKey.ID), interval, now)
		if err != nil {
			log.Error().Err(err).Str("key_id", apiKey.ID).Str("interval", interval).
				Msg("Failed to read key usage totals")
			windows[interval] = map[string]any{fieldError: systemInfoUnavailable}
			continue
		}
		totalsByInterval[interval] = totals
		windows[interval] = map[string]any{
			fieldRequests:    totals.Requests,
			fieldTokens:      totals.Tokens,
			"spend_nano_usd": totals.SpendNanoUSD,
		}
	}

	result := map[string]any{"windows": windows}
	if apiKey.Limits == nil {
		return result
	}
	budgets := map[string]any{}
	if budget := apiKey.Limits.Spend; budget != nil {
		budgets["spend"] = budgetUsage(budget, totalsByInterval, func(t usage.Totals) int64 { return t.SpendNanoUSD })
	}
	if budget := apiKey.Limits.Tokens; budget != nil {
		budgets[fieldTokens] = budgetUsage(budget, totalsByInterval, func(t usage.Totals) int64 { return t.Tokens })
	}
	if len(budgets) > 0 {
		result["budgets"] = budgets
	}
	return result
}

// budgetUsage shapes one budget's current-window consumption.
func budgetUsage(
	budget *limits.Budget,
	totalsByInterval map[string]usage.Totals,
	used func(usage.Totals) int64,
) map[string]any {
	totals, ok := totalsByInterval[budget.Interval]
	if !ok {
		return map[string]any{fieldLimit: budget.Limit, "interval": budget.Interval, fieldError: systemInfoUnavailable}
	}
	consumed := used(totals)
	remaining := budget.Limit - consumed
	if remaining < 0 {
		remaining = 0
	}
	return map[string]any{
		fieldLimit:  budget.Limit,
		"interval":  budget.Interval,
		"used":      consumed,
		"remaining": remaining,
	}
}

// isKeyValidationError reports whether err is a caller-shaped API key error.
func isKeyValidationError(err error) bool {
	return errors.Is(err, apikey.ErrInvalidName) ||
		errors.Is(err, apikey.ErrMissingScopes) ||
		errors.Is(err, apikey.ErrInvalidScope) ||
		errors.Is(err, apikey.ErrInvalidModel) ||
		errors.Is(err, apikey.ErrInvalidExpiration) ||
		// Naming an account that does not exist is the caller's mistake, not
		// a gateway failure, so it answers 400 rather than 500.
		errors.Is(err, apikey.ErrUnknownAccount) ||
		errors.Is(err, account.ErrInvalidID) ||
		errors.Is(err, limits.ErrInvalidRequestLimit) ||
		errors.Is(err, limits.ErrInvalidRequestWindow) ||
		errors.Is(err, limits.ErrInvalidBudgetLimit) ||
		errors.Is(err, limits.ErrInvalidBudgetInterval)
}

// UpdateKey handles PUT /api/v1/admin/keys/{key_id}
func (h *AdminController) UpdateKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	keyID := chi.URLParam(r, "key_id")

	record, err := h.apiKeys.GetByID(ctx, keyID)
	if err != nil {
		if errors.Is(err, apikey.ErrNotFound) {
			dto.WriteError(w, http.StatusNotFound, dto.ErrorTypeNotFound, "API key not found")
			return
		}
		log.Error().Err(err).Str("key_id", keyID).Msg("Failed to get API key")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to get API key")
		return
	}

	apiKey := record.APIKey

	// Parse update request
	var req struct {
		Name          *string           `json:"name,omitempty"`
		Scopes        []string          `json:"scopes,omitempty"`
		AllowedModels []string          `json:"allowed_models,omitempty"`
		Limits        *limits.Limits    `json:"limits,omitempty"`
		ExpiresAt     *time.Time        `json:"expires_at,omitempty"`
		Metadata      map[string]string `json:"metadata,omitempty"`
		Active        *bool             `json:"active,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Invalid request body")
		return
	}

	// Update fields
	if req.Name != nil {
		apiKey.Name = *req.Name
	}
	if req.Scopes != nil {
		apiKey.Scopes = req.Scopes
	}
	if req.AllowedModels != nil {
		// An explicit empty list clears the restriction.
		apiKey.AllowedModels = req.AllowedModels
		if len(req.AllowedModels) == 0 {
			apiKey.AllowedModels = nil
		}
	}
	if req.Limits != nil {
		// An explicit empty object clears every limit.
		apiKey.Limits = req.Limits
		if req.Limits.IsZero() {
			apiKey.Limits = nil
		}
	}
	if req.ExpiresAt != nil {
		apiKey.ExpiresAt = req.ExpiresAt
	}
	if req.Metadata != nil {
		if _, err := keyring.ParseStrategy(req.Metadata[keyring.StrategyMetadataKey]); err != nil {
			dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, err.Error())
			return
		}
		apiKey.Metadata = convertStringMapToInterface(req.Metadata)
	}
	if req.Active != nil {
		apiKey.Active = *req.Active
	}

	if err := apiKey.Validate(); err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, err.Error())
		return
	}

	updated, err := h.apiKeys.Update(ctx, apiKey, record.Revision)
	writeAudit(ctx, h.audit, "key.update", keyID, err)
	if err != nil {
		if errors.Is(err, apikey.ErrConflict) {
			dto.WriteError(w, http.StatusConflict, dto.ErrorTypeInvalidRequest, "API key changed during update")
			return
		}
		log.Error().Err(err).Msg("Failed to update the API key")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to update API key")
		return
	}

	apiKey = updated.APIKey
	apiKey.Hash = ""

	if err := dto.WriteJSON(w, http.StatusOK, apiKey); err != nil {
		log.Error().Err(err).Msg("Failed to write response")
	}
}

// DeleteKey handles DELETE /api/v1/admin/keys/{key_id}
func (h *AdminController) DeleteKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	keyID := chi.URLParam(r, "key_id")

	err := h.apiKeys.Delete(ctx, keyID, 0)
	writeAudit(ctx, h.audit, "key.delete", keyID, err)
	if err == nil && h.events != nil {
		h.events.Emit(events.TypeKeyDeleted, map[string]string{fieldKeyID: keyID})
	}
	if err != nil {
		if errors.Is(err, apikey.ErrNotFound) {
			dto.WriteError(w, http.StatusNotFound, dto.ErrorTypeNotFound, "API key not found")
			return
		}
		log.Error().Err(err).Str("key_id", keyID).Msg("Failed to delete API key")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to delete API key")
		return
	}

	response := map[string]any{
		responseMessageField: "API key deleted successfully",
		fieldKeyID:           keyID,
	}

	if err := dto.WriteJSON(w, http.StatusOK, response); err != nil {
		log.Error().Err(err).Msg("Failed to write response")
	}
}

// SystemInfo handles GET /api/v1/admin/info
func (h *AdminController) SystemInfo(w http.ResponseWriter, _ *http.Request) {
	// TODO: Implement actual system info gathering
	info := map[string]any{
		"service":    "starport",
		"version":    "1.0.0",
		"uptime":     systemInfoUnavailable,
		"go_version": runtime.Version(),
		"os":         runtime.GOOS,
		"arch":       runtime.GOARCH,
		"storage": map[string]any{
			"type":      "badger",
			fieldStatus: "healthy",
		},
		// Stored file bytes do not live in the record store, so the backend
		// that holds them is a separate fact. A deployment that stores no
		// files says so rather than leaving the field out, because an absent
		// field reads as an older gateway.
		"files": map[string]any{
			"backend": h.fileStorage(),
		},
		providersField: map[string]any{
			responseCountField: systemInfoUnavailable,
			fieldStatus:        systemInfoUnavailable,
		},
	}

	if err := dto.WriteJSON(w, http.StatusOK, info); err != nil {
		log.Error().Err(err).Msg("Failed to write response")
	}
}

// fileStorage names the blob backend, or says that this deployment stores no
// files at all.
func (h *AdminController) fileStorage() string {
	if h.fileBackend == "" {
		return "not configured"
	}
	return h.fileBackend
}

// metricsSampleWindow bounds the record sample behind rates, error
// counts, latency percentiles, and per-provider counts.
const metricsSampleWindow = 24 * time.Hour

// Metrics handles GET /api/v1/admin/metrics
func (h *AdminController) Metrics(w http.ResponseWriter, r *http.Request) {
	if h.usageRecords == nil {
		dto.WriteError(w, http.StatusServiceUnavailable, dto.ErrorTypeServerError, usageNotConfiguredMessage)
		return
	}

	ctx := r.Context()
	now := time.Now().UTC()

	// No key and no account: deployment metrics sample every request.
	sample, err := h.usageRecords.List(ctx, usage.Query{
		Since: now.Add(-metricsSampleWindow),
		Limit: usage.MaxListLimit,
	})
	if err != nil {
		log.Error().Err(err).Msg("Failed to sample usage records for metrics")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to compute metrics")
		return
	}

	metrics := metricsFromSample(sample.Records, now)
	metrics["windows"] = h.metricsWindows(ctx, now)
	metrics["sample"] = map[string]any{
		"records":   len(sample.Records),
		"window":    metricsSampleWindow.String(),
		"truncated": sample.NextCursor != "",
	}

	if err := dto.WriteJSON(w, http.StatusOK, metrics); err != nil {
		log.Error().Err(err).Msg("Failed to write response")
	}
}

// metricsFromSample derives one consistent metrics view from a single
// newest-first record sample.
func metricsFromSample(records []usage.Record, now time.Time) map[string]any {
	var success, failures, lastMinute, tokens, spendNanoUSD, uncosted int64
	latencies := make([]int64, 0, len(records))
	overheads := make([]int64, 0, len(records))
	currency := "USD"

	type providerCounters struct {
		Requests int64 `json:"requests"`
		Errors   int64 `json:"errors"`
	}
	providers := map[string]*providerCounters{}

	for _, record := range records {
		if record.Status == usage.StatusError {
			failures++
		} else {
			success++
		}
		if record.Timestamp.After(now.Add(-time.Minute)) {
			lastMinute++
		}
		tokens += record.Tokens.Total
		if record.Cost != nil {
			spendNanoUSD += record.Cost.NanoUSD
			if record.Cost.Currency != "" {
				currency = record.Cost.Currency
			}
		} else {
			uncosted++
		}
		latencies = append(latencies, record.LatencyMS)
		overheads = append(overheads, record.OverheadMS)

		provider := record.Provider
		if provider == "" {
			provider = "unrouted"
		}
		counters := providers[provider]
		if counters == nil {
			counters = &providerCounters{}
			providers[provider] = counters
		}
		counters.Requests++
		if record.Status == usage.StatusError {
			counters.Errors++
		}
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	sort.Slice(overheads, func(i, j int) bool { return overheads[i] < overheads[j] })

	return map[string]any{
		fieldRequests: map[string]any{
			"total":     success + failures,
			"success":   success,
			"errors":    failures,
			"rate_1min": lastMinute,
		},
		"latency": map[string]any{
			"p50": latencyPercentile(latencies, 0.50),
			"p95": latencyPercentile(latencies, 0.95),
			"p99": latencyPercentile(latencies, 0.99),
		},
		// Gateway-added latency only: total handling minus upstream waits.
		"overhead": map[string]any{
			"p50": latencyPercentile(overheads, 0.50),
			"p95": latencyPercentile(overheads, 0.95),
			"p99": latencyPercentile(overheads, 0.99),
		},
		fieldTokens: map[string]any{
			"total": tokens,
		},
		"spend": map[string]any{
			"nano_usd":              spendNanoUSD,
			"currency":              currency,
			"requests_without_cost": uncosted,
		},
		providersField: providers,
	}
}

// metricsWindows reads the exact aggregate counters for the fixed
// UTC-aligned day, week, and month windows containing now.
func (h *AdminController) metricsWindows(ctx context.Context, now time.Time) map[string]any {
	windows := map[string]any{}
	for _, interval := range []string{usage.IntervalDay, usage.IntervalWeek, usage.IntervalMonth} {
		totals, err := h.usageRecords.Totals(ctx, usage.GatewayScope(), interval, now)
		if err != nil {
			log.Error().Err(err).Str("interval", interval).Msg("Failed to read usage totals")
			windows[interval] = map[string]any{fieldError: systemInfoUnavailable}
			continue
		}
		windows[interval] = map[string]any{
			fieldRequests:    totals.Requests,
			fieldTokens:      totals.Tokens,
			"spend_nano_usd": totals.SpendNanoUSD,
		}
	}
	return windows
}

// latencyPercentile returns the nearest-rank percentile of an ascending
// latency sample, or zero for an empty sample.
func latencyPercentile(sorted []int64, quantile float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(quantile*float64(len(sorted)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}

// Helper functions

func convertStringMapToInterface(m map[string]string) map[string]any {
	if m == nil {
		return nil
	}
	result := make(map[string]any, len(m))
	for k, v := range m {
		result[k] = v
	}
	return result
}
