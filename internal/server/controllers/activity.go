package controllers

import (
	"errors"
	"net/http"
	"strconv"
	"time"

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
