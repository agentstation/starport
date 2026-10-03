package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/config"
	"github.com/agentstation/starport/internal/server/dto"
	"github.com/agentstation/starport/internal/server/requestctx"
)

const configurationNotConfiguredMessage = "Configuration operations are not configured"

// maxConfigurationRequestBytes bounds one configuration request body.
const maxConfigurationRequestBytes = 64 << 10

// fieldOperationID names the configuration operation that a caller reads.
const fieldOperationID = "operation_id"

// ConfigurationOperations serves the configuration admin surface. Schema and
// Effective read process memory only. A refusal is a *config.Refusal error.
type ConfigurationOperations interface {
	Schema() []config.SchemaSetting
	Effective() config.EffectiveReport
	Validate(context.Context, config.FieldSave) (config.Validation, error)
	TestConnection(context.Context, config.FieldSave) (config.ConnectionResult, error)
	Save(context.Context, config.FieldSave, string) (config.Receipt, error)
	Receipt(context.Context, string) (config.Receipt, bool, error)
}

// ConfigurationController serves /api/v1/admin/config. The admin group
// supplies the admin scope. Each write adds the same-origin check here, and
// the operations add the deployment and management checks before any store
// or file access.
type ConfigurationController struct {
	operations ConfigurationOperations
	origin     *http.CrossOriginProtection
}

// NewConfigurationController creates the configuration operations adapter.
func NewConfigurationController(operations ConfigurationOperations) *ConfigurationController {
	return &ConfigurationController{operations: operations, origin: http.NewCrossOriginProtection()}
}

// configurationRefusal is the body of a refused configuration operation. A
// refused save also carries its refused receipt.
type configurationRefusal struct {
	Error   dto.ErrorDetail `json:"error"`
	Refusal *config.Refusal `json:"refusal"`
	Receipt *config.Receipt `json:"receipt,omitempty"`
}

// Schema handles GET /api/v1/admin/config/schema.
func (h *ConfigurationController) Schema(w http.ResponseWriter, _ *http.Request) {
	if !h.configured(w) {
		return
	}
	h.write(w, http.StatusOK, map[string]any{"settings": h.operations.Schema()})
}

// Effective handles GET /api/v1/admin/config/effective. It reports the
// redacted values that this process serves.
func (h *ConfigurationController) Effective(w http.ResponseWriter, _ *http.Request) {
	if !h.configured(w) {
		return
	}
	h.write(w, http.StatusOK, h.operations.Effective())
}

// Validate handles POST /api/v1/admin/config/validate. It writes nothing.
func (h *ConfigurationController) Validate(w http.ResponseWriter, r *http.Request) {
	request, ok := h.writeRequest(w, r)
	if !ok {
		return
	}
	validation, err := h.operations.Validate(r.Context(), request)
	if err != nil {
		h.writeError(w, err, nil)
		return
	}
	h.write(w, http.StatusOK, validation)
}

// TestConnection handles POST /api/v1/admin/config/test-connection. The
// probe is bounded, and the result never echoes a credential.
func (h *ConfigurationController) TestConnection(w http.ResponseWriter, r *http.Request) {
	request, ok := h.writeRequest(w, r)
	if !ok {
		return
	}
	result, err := h.operations.TestConnection(r.Context(), request)
	if err != nil {
		h.writeError(w, err, nil)
		return
	}
	h.write(w, http.StatusOK, result)
}

// Save handles POST /api/v1/admin/config/save. A new save and an exact retry
// both answer 200 with the receipt.
func (h *ConfigurationController) Save(w http.ResponseWriter, r *http.Request) {
	request, ok := h.writeRequest(w, r)
	if !ok {
		return
	}
	receipt, err := h.operations.Save(r.Context(), request, auditActor(r.Context()))
	if err != nil {
		h.writeError(w, err, &config.Receipt{
			OperationID: request.OperationID, DeploymentID: request.DeploymentID,
			Management: h.operations.Effective().Management, Status: config.OperationRefused,
		})
		return
	}
	h.write(w, http.StatusOK, receipt)
}

// Receipt handles GET /api/v1/admin/config/operations/{operation_id}.
func (h *ConfigurationController) Receipt(w http.ResponseWriter, r *http.Request) {
	if !h.configured(w) {
		return
	}
	receipt, found, err := h.operations.Receipt(r.Context(), chi.URLParam(r, fieldOperationID))
	if err != nil {
		h.writeError(w, err, nil)
		return
	}
	if !found {
		dto.WriteError(w, http.StatusNotFound, dto.ErrorTypeNotFound, "The configuration operation is not found")
		return
	}
	h.write(w, http.StatusOK, receipt)
}

// writeRequest refuses a cross-site write before it decodes the body. A
// console session write must name its origin, because the browser sends the
// header on every write that it allows.
func (h *ConfigurationController) writeRequest(w http.ResponseWriter, r *http.Request) (config.FieldSave, bool) {
	var request config.FieldSave
	if !h.configured(w) {
		return request, false
	}
	if _, _, console := requestctx.GetConsoleSession(r.Context()); console && r.Header.Get("Origin") == "" {
		dto.WriteError(w, http.StatusForbidden, dto.ErrorTypePermissionError, "A console configuration write requires the Origin header")
		return request, false
	}
	if err := h.origin.Check(r); err != nil {
		dto.WriteError(w, http.StatusForbidden, dto.ErrorTypePermissionError, "A cross-origin configuration write is refused")
		return request, false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxConfigurationRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		// A decode error can quote a value of the body, so it is not repeated.
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "The configuration request body is not valid JSON of the documented form")
		return request, false
	}
	return request, true
}

func (h *ConfigurationController) configured(w http.ResponseWriter) bool {
	w.Header().Set("Cache-Control", "no-store")
	if h.operations == nil {
		dto.WriteError(w, http.StatusServiceUnavailable, dto.ErrorTypeServiceUnavailable, configurationNotConfiguredMessage)
		return false
	}
	return true
}

// writeError maps a refusal to its status. Any other failure is a generic
// server error: its text can name storage details.
func (h *ConfigurationController) writeError(w http.ResponseWriter, err error, receipt *config.Receipt) {
	var refusal *config.Refusal
	if !errors.As(err, &refusal) {
		log.Error().Err(err).Msg("configuration operation failed")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "The configuration operation failed")
		return
	}
	status, errorType := refusalShape(refusal.Reason)
	if receipt != nil {
		receipt.Refusal = refusal
	}
	h.write(w, status, configurationRefusal{
		Error:   dto.ErrorDetail{Message: refusal.Error(), Type: errorType, Code: refusal.Reason},
		Refusal: refusal, Receipt: receipt,
	})
}

func refusalShape(reason string) (int, string) {
	switch reason {
	case config.RefusalStaleRevision, config.RefusalOperationConflict, config.RefusalBusy, config.RefusalIncomplete:
		return http.StatusConflict, dto.ErrorTypeInvalidRequest
	case config.RefusalExternalManagement:
		return http.StatusLocked, dto.ErrorTypePermissionError
	case config.RefusalForeignDeployment:
		return http.StatusForbidden, dto.ErrorTypePermissionError
	case config.RefusalSchemaBehind, config.RefusalUnavailable:
		return http.StatusServiceUnavailable, dto.ErrorTypeServiceUnavailable
	default:
		return http.StatusUnprocessableEntity, dto.ErrorTypeInvalidRequest
	}
}

func (h *ConfigurationController) write(w http.ResponseWriter, status int, body any) {
	if err := dto.WriteJSON(w, status, body); err != nil {
		log.Error().Err(err).Msg("failed to write the configuration response")
	}
}
