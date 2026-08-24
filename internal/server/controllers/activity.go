package controllers

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/server/dto"
	"github.com/agentstation/starport/internal/server/requestctx"
	"github.com/agentstation/starport/internal/usage"
)

const usageNotConfiguredMessage = "Usage accounting is not configured"

// ActivityController serves recorded request activity.
type ActivityController struct {
	usageRecords usage.Repository
}

// NewActivityController creates a new activity controller.
func NewActivityController(usageRecords usage.Repository) *ActivityController {
	return &ActivityController{usageRecords: usageRecords}
}

// List handles GET /api/v1/activity. It always scopes the listing to the
// authenticated key; only the admin listing can widen the scope.
func (h *ActivityController) List(w http.ResponseWriter, r *http.Request) {
	if h.usageRecords == nil {
		dto.WriteError(w, http.StatusServiceUnavailable, dto.ErrorTypeServerError, usageNotConfiguredMessage)
		return
	}

	keyID, ok := requestctx.GetAPIKeyID(r.Context())
	if !ok || keyID == "" {
		dto.WriteError(w, http.StatusUnauthorized, dto.ErrorTypeAuthenticationError, "Authentication is required")
		return
	}

	query, err := activityQueryFromRequest(r)
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, err.Error())
		return
	}
	query.KeyID = keyID

	h.respondWithPage(w, r, query)
}

// AdminList handles GET /api/v1/admin/activity. An empty key_id parameter
// lists activity across every key.
func (h *ActivityController) AdminList(w http.ResponseWriter, r *http.Request) {
	if h.usageRecords == nil {
		dto.WriteError(w, http.StatusServiceUnavailable, dto.ErrorTypeServerError, usageNotConfiguredMessage)
		return
	}

	query, err := activityQueryFromRequest(r)
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, err.Error())
		return
	}
	query.KeyID = r.URL.Query().Get("key_id")

	h.respondWithPage(w, r, query)
}

func (h *ActivityController) respondWithPage(w http.ResponseWriter, r *http.Request, query usage.Query) {
	page, err := h.usageRecords.List(r.Context(), query)
	if err != nil {
		if errors.Is(err, usage.ErrInvalidQuery) {
			dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, err.Error())
			return
		}
		log.Error().Err(err).Msg("Failed to list usage records")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to list activity")
		return
	}

	records := page.Records
	if records == nil {
		records = []usage.Record{}
	}
	if err := dto.WriteList(w, http.StatusOK, records, page.NextCursor); err != nil {
		log.Error().Err(err).Msg("Failed to write response")
	}
}

// activityQueryFromRequest parses the shared activity filter parameters.
// The caller owns the KeyID scope decision.
func activityQueryFromRequest(r *http.Request) (usage.Query, error) {
	values := r.URL.Query()
	query := usage.Query{
		Model:    values.Get("model"),
		Provider: values.Get("provider"),
		Status:   values.Get("status"),
		Cursor:   values.Get("cursor"),
	}

	since, err := activityTimeParameter(values.Get("since"), "since")
	if err != nil {
		return usage.Query{}, err
	}
	query.Since = since

	until, err := activityTimeParameter(values.Get("until"), "until")
	if err != nil {
		return usage.Query{}, err
	}
	query.Until = until

	if raw := values.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 {
			return usage.Query{}, errors.New("limit must be a positive integer")
		}
		query.Limit = limit
	}

	return query, nil
}

func activityTimeParameter(raw, name string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, errors.New(name + " must be an RFC 3339 timestamp")
	}
	return value, nil
}

// providerUsageWindow bounds the per-provider usage aggregation.
const providerUsageWindow = 30 * 24 * time.Hour

// providerUsageMaxPages bounds the record walk behind one aggregation.
const providerUsageMaxPages = 30

// providerUsageSummary aggregates one provider's recorded usage for one
// gateway key.
type providerUsageSummary struct {
	Provider            string       `json:"provider"`
	Requests            int64        `json:"requests"`
	Errors              int64        `json:"errors"`
	Tokens              usage.Tokens `json:"tokens"`
	SpendNanoUSD        int64        `json:"spend_nano_usd"`
	RequestsWithoutCost int64        `json:"requests_without_cost"`
}

// ByProvider handles GET /api/v1/keys/{key_id}/usage/providers. It groups
// one key's recorded requests by the provider that served them. The
// grouping is by provider and not by credential: a record names which
// provider answered, never which of the three credential sources paid.
func (h *ActivityController) ByProvider(w http.ResponseWriter, r *http.Request) {
	if h.usageRecords == nil {
		dto.WriteError(w, http.StatusServiceUnavailable, dto.ErrorTypeServerError, "Usage accounting is not configured")
		return
	}

	ctx := r.Context()
	apiKeyID := chi.URLParam(r, "key_id")
	until := time.Now().UTC()
	since := until.Add(-providerUsageWindow)

	summaries := map[string]*providerUsageSummary{}
	truncated := false
	cursor := ""
	for page := 0; page < providerUsageMaxPages; page++ {
		result, err := h.usageRecords.List(ctx, usage.Query{
			KeyID:  apiKeyID,
			Since:  since,
			Limit:  usage.MaxListLimit,
			Cursor: cursor,
		})
		if err != nil {
			log.Error().Err(err).Str("api_key_id", apiKeyID).Msg("Failed to aggregate provider usage")
			dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to aggregate provider usage")
			return
		}
		for _, record := range result.Records {
			provider := record.Provider
			if provider == "" {
				provider = "unrouted"
			}
			summary := summaries[provider]
			if summary == nil {
				summary = &providerUsageSummary{Provider: provider}
				summaries[provider] = summary
			}
			summary.Requests++
			if record.Status == usage.StatusError {
				summary.Errors++
			}
			summary.Tokens.Input += record.Tokens.Input
			summary.Tokens.Output += record.Tokens.Output
			summary.Tokens.Total += record.Tokens.Total
			summary.Tokens.Reasoning += record.Tokens.Reasoning
			summary.Tokens.CacheRead += record.Tokens.CacheRead
			summary.Tokens.CacheWrite += record.Tokens.CacheWrite
			if record.Cost != nil {
				summary.SpendNanoUSD += record.Cost.NanoUSD
			} else {
				summary.RequestsWithoutCost++
			}
		}
		cursor = result.NextCursor
		if cursor == "" {
			break
		}
		if page == providerUsageMaxPages-1 {
			truncated = true
		}
	}

	data := make([]providerUsageSummary, 0, len(summaries))
	for _, summary := range summaries {
		data = append(data, *summary)
	}
	sort.Slice(data, func(i, j int) bool { return data[i].Provider < data[j].Provider })

	response := map[string]any{
		"data": data,
		"window": map[string]any{
			"since": since.Format(time.RFC3339),
			"until": until.Format(time.RFC3339),
		},
	}
	if truncated {
		response["truncated"] = true
	}

	if err := dto.WriteJSON(w, http.StatusOK, response); err != nil {
		log.Error().Err(err).Msg("Failed to write response")
	}
}
