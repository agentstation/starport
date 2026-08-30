package controllers

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/authmode"
	"github.com/agentstation/starport/internal/server/dto"
)

// AuthController reports how the gateway authenticates requests, and lets an
// operator change it.
type AuthController struct {
	policy      *authmode.Policy
	store       authmode.Repository
	bindHost    string
	allowRemote bool
	audit       AuditRecorder
}

// NewAuthController creates a controller over the running authentication
// policy.
//
// bindHost and allowRemote are the same two values startup validation reads.
// They travel here because the runtime switch answers the same question
// startup does — may authentication be off on this address — and a rule
// enforced at startup and restated at runtime is a rule with two versions.
func NewAuthController(
	policy *authmode.Policy,
	store authmode.Repository,
	bindHost string,
	allowRemote bool,
) *AuthController {
	return &AuthController{
		policy:      policy,
		store:       store,
		bindHost:    bindHost,
		allowRemote: allowRemote,
	}
}

// AuthModeResponse is the answer to GET /api/v1/auth/mode and to a successful
// PUT.
type AuthModeResponse struct {
	// Mode is "required" or "disabled".
	Mode string `json:"mode"`
	// Source names what set the running mode: "default", "config", "flag", or
	// "console". An operator who wants to change a mode they cannot change
	// here needs to know which thing to edit.
	Source string `json:"source"`
	// CanChange reports whether this caller may change the mode at runtime. It
	// is answered per request, because the same deployment says yes to a
	// browser on the machine and no to one across the network.
	CanChange bool `json:"can_change"`
	// Reason names why CanChange is false, so the console can explain the
	// disabled control instead of showing one that fails. It is empty when
	// CanChange is true.
	Reason string `json:"reason,omitempty"`
}

// setAuthModeRequest is the body of PUT /api/v1/admin/auth/mode.
type setAuthModeRequest struct {
	Mode string `json:"mode"`
}

const (
	reasonNotLocal    = "the authentication mode can only be changed from this machine"
	reasonNoStorage   = "this deployment cannot store an authentication mode, so a change would not survive a restart"
	reasonFixedByFlag = "the authentication mode is fixed by a command line flag for this process"
	//nolint:gosec // Names a configuration variable, not a credential.
	reasonFixedByConfig = "the authentication mode is fixed by STARPORT_SECURITY_AUTH_MODE"
)

// Mode handles GET /api/v1/auth/mode.
//
// The route carries no key requirement on purpose. A client that does not yet
// hold a gateway API key needs to know whether it has to go get one, and
// answering that question with 401 tells it nothing it can act on. The answer
// discloses only what an unauthenticated request would discover by making one.
func (c *AuthController) Mode(w http.ResponseWriter, r *http.Request) {
	current := c.policy.Current()
	reason := c.refusal(r, current)
	_ = dto.WriteJSON(w, http.StatusOK, AuthModeResponse{
		Mode:      string(current.Mode),
		Source:    string(current.Source),
		CanChange: reason == "",
		Reason:    reason,
	})
}

// SetMode handles PUT /api/v1/admin/auth/mode.
//
// It is the only write in the gateway that can open the gateway, so it is
// guarded three ways and each guard answers a different question: the admin
// scope asks who is calling, the loopback checks ask from where, and the
// exposure rule asks whether the resulting gateway would be reachable without
// a key. Holding admin is not enough, because an operator whose key leaked
// should not be able to turn the lock off from anywhere on the network.
func (c *AuthController) SetMode(w http.ResponseWriter, r *http.Request) {
	current := c.policy.Current()
	if reason := c.refusal(r, current); reason != "" {
		dto.WriteError(w, http.StatusForbidden, dto.ErrorTypePermissionError, reason)
		return
	}

	var req setAuthModeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Invalid request body")
		return
	}
	mode := authmode.Mode(req.Mode)
	if mode == "" || !mode.Valid() {
		dto.WriteValidationError(w, "mode", `Mode must be "required" or "disabled"`)
		return
	}

	// The exposure rule is checked before the write, not after, so a refused
	// change leaves no stored mode a restart would then honor.
	if mode == authmode.Disabled && !authmode.AllowsDisabled(c.bindHost, c.allowRemote) {
		dto.WriteError(w, http.StatusConflict, dto.ErrorTypeInvalidRequest,
			"Authentication cannot be disabled while the gateway binds "+c.bindHost+
				", which is not a loopback address. Restart with --allow-remote-no-auth to allow it.")
		return
	}

	setting := authmode.Setting{Mode: mode, Source: authmode.SourceConsole, UpdatedAt: time.Now().UTC()}
	err := c.persist(r, setting)
	writeAudit(r.Context(), c.audit, "auth_mode.update", string(mode), err)
	if err != nil {
		if errors.Is(err, authmode.ErrConflict) {
			dto.WriteError(w, http.StatusConflict, dto.ErrorTypeInvalidRequest,
				"The authentication mode changed while this request was in flight")
			return
		}
		log.Error().Err(err).Msg("Failed to store the authentication mode")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError,
			"Failed to store the authentication mode")
		return
	}

	// The running policy is set only after the store accepts, so a caller that
	// sees 200 knows the change survives a restart. The middleware reads the
	// policy per request, so the next request already sees this.
	c.policy.Set(setting)

	_ = dto.WriteJSON(w, http.StatusOK, AuthModeResponse{
		Mode:      string(setting.Mode),
		Source:    string(setting.Source),
		CanChange: true,
	})
}

// persist writes the setting at whatever revision is stored now.
//
// The read-then-write is deliberately not a caller-supplied revision: the mode
// is one deployment-wide switch and an operator flipping it has no revision to
// hold. A racing second writer still loses, because the compare-and-swap sees
// the revision move.
func (c *AuthController) persist(r *http.Request, setting authmode.Setting) error {
	ctx := r.Context()
	var revision uint64
	record, err := c.store.Get(ctx)
	switch {
	case err == nil:
		revision = record.Revision
	case errors.Is(err, authmode.ErrNotFound):
		revision = 0
	default:
		return err
	}
	_, err = c.store.Put(ctx, setting, revision)
	return err
}

// refusal names why this caller may not change the mode, or returns empty when
// it may. Both the read and the write call it, so what the console is told and
// what the gateway enforces cannot disagree.
func (c *AuthController) refusal(r *http.Request, current authmode.Setting) string {
	switch {
	case c.policy == nil || c.store == nil:
		return reasonNoStorage
	case current.Source == authmode.SourceFlag:
		return reasonFixedByFlag
	case current.Source == authmode.SourceConfig:
		return reasonFixedByConfig
	case !authmode.LoopbackAddr(r.RemoteAddr):
		return reasonNotLocal
	case !authmode.LoopbackOrigin(r.Header.Get("Origin")):
		return reasonNotLocal
	default:
		return ""
	}
}
