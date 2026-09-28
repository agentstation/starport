package controllers

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"net/http"
	"time"

	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/jobs"
	"github.com/agentstation/starport/internal/limits"
	"github.com/agentstation/starport/internal/server/dto"
	"github.com/agentstation/starport/internal/server/requestctx"
	"github.com/agentstation/starport/internal/storage"
	"github.com/go-chi/chi/v5"
)

// InspectReconciliation serves private evidence through the administrator route.
func (h *VideosController) InspectReconciliation(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	if _, ok := reconciliationActor(r); !ok {
		writeReconciliationAuthentication(w)
		return
	}
	view, err := h.jobs.InspectReconciliation(r.Context(), chi.URLParam(r, fieldAccountID), chi.URLParam(r, videoIDParam))
	if err != nil {
		h.writeReconciliationError(w, err)
		return
	}
	h.writeReconciliationView(w, view)
}

// ReconcileAdministrator accepts evidence without another provider dispatch.
// Route middleware enforces administrator access before this method runs.
func (h *VideosController) ReconcileAdministrator(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	actor, ok := reconciliationActor(r)
	if !ok {
		writeReconciliationAuthentication(w)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var input jobs.ReconciliationRequest
	if err := json.UnmarshalRead(r.Body, &input, json.RejectUnknownMembers(true), jsontext.AllowDuplicateNames(false)); err != nil {
		h.writeReconciliationError(w, jobs.ErrReconciliationInvalid)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	view, err := h.jobs.ReconcileAdministrator(ctx, chi.URLParam(r, fieldAccountID), chi.URLParam(r, videoIDParam), actor, input)
	if err != nil {
		h.writeReconciliationError(w, err)
		return
	}
	h.writeReconciliationView(w, view)
}

// reconciliationActor uses a stable authenticated identity for private audit access.
func reconciliationActor(r *http.Request) (string, bool) {
	if _, _, console := requestctx.GetConsoleSession(r.Context()); console {
		return auditActor(r.Context()), true
	}
	if id, ok := requestctx.GetAPIKeyID(r.Context()); ok && id != "" && id != apikey.AnonymousKeyID {
		return "key:" + id, true
	}
	return "", false
}

func writeReconciliationAuthentication(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	dto.WriteError(w, http.StatusUnauthorized, dto.ErrorTypeInvalidRequest, "Authenticate an administrator before accessing video reconciliation.")
}

func (h *VideosController) writeReconciliationView(w http.ResponseWriter, view jobs.ReconciliationView) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.MarshalWrite(w, view)
}

func (h *VideosController) writeReconciliationError(w http.ResponseWriter, err error) {
	w.Header().Set("Cache-Control", "no-store")
	status, message := http.StatusServiceUnavailable, "Reconciliation is pending. Inspect the decision before retrying."
	switch {
	case errors.Is(err, limits.ErrCorrectionExpired):
		status, message = http.StatusConflict, "The 90-day correction deadline passed. Accepted audit evidence remains available."
	case errors.Is(err, jobs.ErrJobNotFound), errors.Is(err, jobs.ErrCorrectionNotFound):
		status, message = http.StatusNotFound, "Video job not found"
	case errors.Is(err, jobs.ErrReconciliationInvalid):
		status, message = http.StatusBadRequest, "Provide the current binding and complete usage or no-charge evidence."
	case errors.Is(err, jobs.ErrReconciliationConflict), errors.Is(err, storage.ErrConflict):
		status, message = http.StatusConflict, "Retained evidence requires administrator review."
	}
	kind := dto.ErrorTypeInvalidRequest
	if status == http.StatusServiceUnavailable {
		kind = dto.ErrorTypeServerError
	}
	dto.WriteError(w, status, kind, message)
}

// CorrectAdministrator accepts a new audited decision for an existing reconciliation.
// Route middleware enforces administrator access before this method runs.
func (h *VideosController) CorrectAdministrator(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	actor, ok := reconciliationActor(r)
	if !ok {
		writeReconciliationAuthentication(w)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var input jobs.ReconciliationRequest
	if err := json.UnmarshalRead(r.Body, &input, json.RejectUnknownMembers(true), jsontext.AllowDuplicateNames(false)); err != nil {
		h.writeReconciliationError(w, jobs.ErrReconciliationInvalid)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	view, err := h.jobs.CorrectAdministrator(ctx, chi.URLParam(r, fieldAccountID), chi.URLParam(r, videoIDParam), actor, input)
	if err != nil {
		h.writeReconciliationError(w, err)
		return
	}
	h.writeReconciliationView(w, view)
}

// InspectCorrection serves one immutable administrator decision and its outcome.
func (h *VideosController) InspectCorrection(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}
	if _, ok := reconciliationActor(r); !ok {
		writeReconciliationAuthentication(w)
		return
	}
	audit, err := h.jobs.InspectCorrection(r.Context(), chi.URLParam(r, fieldAccountID), chi.URLParam(r, videoIDParam), chi.URLParam(r, "decision_id"))
	if err != nil {
		h.writeReconciliationError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.MarshalWrite(w, audit)
}
