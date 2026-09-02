package controllers

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/audit"
	"github.com/agentstation/starport/internal/localauth"
	"github.com/agentstation/starport/internal/server/dto"
	"github.com/agentstation/starport/internal/server/requestctx"
)

// AuditRecorder records one admin mutation on the durable trail. Every
// mutating controller holds one; a nil recorder records nothing, which is
// what a deployment without the relational store gets.
type AuditRecorder interface {
	Record(ctx context.Context, record audit.Record) error
}

// AuditReader serves the recorded trail to the admin surface.
type AuditReader interface {
	List(ctx context.Context, query audit.Query) (audit.Page, error)
}

// AuditTrail is the whole trail: the mutating controllers write it and the
// admin listing reads it. The audit repository satisfies both halves.
type AuditTrail interface {
	AuditRecorder
	AuditReader
}

// auditActor names the request's actor for the trail. A console session
// beats the synthetic operator key it runs as, because the grant kind or the
// identity subject is who actually asked.
func auditActor(ctx context.Context) string {
	if grant, subject, ok := requestctx.GetConsoleSession(ctx); ok {
		if grant == string(localauth.GrantIdentity) && subject != "" {
			return audit.ActorUserPrefix + subject
		}
		return audit.ActorConsolePrefix + grant
	}
	if key, ok := requestctx.GetAPIKeyModel(ctx); ok && key != nil && key.Name != "" {
		return audit.ActorKeyPrefix + key.Name
	}
	if keyID, ok := requestctx.GetAPIKeyID(ctx); ok && keyID != "" {
		return audit.ActorKeyPrefix + keyID
	}
	return audit.ActorAnonymous
}

// writeAudit records one mutation attempt that reached its store. The
// failure argument is the store's answer: nil records OutcomeOK, anything
// else OutcomeError. A trail write failure is logged and does not change
// the caller's response, because the mutation already happened.
func writeAudit(ctx context.Context, recorder AuditRecorder, action, subject string, failure error) {
	if recorder == nil {
		return
	}
	outcome := audit.OutcomeOK
	if failure != nil {
		outcome = audit.OutcomeError
	}
	record := audit.Record{
		Actor:     auditActor(ctx),
		Action:    action,
		Subject:   subject,
		Outcome:   outcome,
		RequestID: middleware.GetReqID(ctx),
	}
	if err := recorder.Record(ctx, record); err != nil {
		log.Error().Err(err).Str("action", action).Msg("Failed to write the audit record")
	}
}

// AuditController serves the admin audit listing.
type AuditController struct {
	trail AuditReader
}

// NewAuditController creates the audit listing controller.
func NewAuditController(trail AuditReader) *AuditController {
	return &AuditController{trail: trail}
}

// List handles GET /api/v1/admin/audit. It serves one page of the trail,
// newest first, under the listing's filters.
func (h *AuditController) List(w http.ResponseWriter, r *http.Request) {
	if h.trail == nil {
		dto.WriteError(w, http.StatusServiceUnavailable, dto.ErrorTypeServerError,
			"The audit trail is not configured")
		return
	}

	query, err := auditQueryFromRequest(r)
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, err.Error())
		return
	}

	page, err := h.trail.List(r.Context(), query)
	if err != nil {
		if errors.Is(err, audit.ErrInvalidQuery) {
			dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, err.Error())
			return
		}
		log.Error().Err(err).Msg("Failed to list the audit trail")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError,
			"Failed to list the audit trail")
		return
	}

	records := page.Records
	if records == nil {
		records = []audit.Record{}
	}
	if err := dto.WriteList(w, http.StatusOK, records, page.NextCursor); err != nil {
		log.Error().Err(err).Msg("Failed to write response")
	}
}

func auditQueryFromRequest(r *http.Request) (audit.Query, error) {
	values := r.URL.Query()
	query := audit.Query{
		Action: values.Get("action"),
		Actor:  values.Get("actor"),
		Cursor: values.Get("cursor"),
	}

	since, err := activityTimeParameter(values.Get("since"), "since")
	if err != nil {
		return audit.Query{}, err
	}
	query.Since = since

	until, err := activityTimeParameter(values.Get("until"), "until")
	if err != nil {
		return audit.Query{}, err
	}
	query.Until = until

	if raw := values.Get(fieldLimit); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 {
			return audit.Query{}, errors.New("limit must be a positive integer")
		}
		query.Limit = limit
	}

	return query, nil
}
