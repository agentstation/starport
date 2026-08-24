package controllers

import (
	"net/http"

	"github.com/agentstation/starport/internal/server/dto"
)

// AuthController reports how the gateway authenticates requests.
type AuthController struct {
	mode      string
	canChange bool
}

// NewAuthController creates a controller that reports the given mode.
func NewAuthController(mode string, canChange bool) *AuthController {
	return &AuthController{mode: mode, canChange: canChange}
}

// AuthModeResponse is the answer to GET /api/v1/auth/mode.
type AuthModeResponse struct {
	// Mode is "required" or "disabled".
	Mode string `json:"mode"`
	// CanChange reports whether the console may change the mode at runtime.
	// A mode fixed by configuration or by a command line flag is reported and
	// not offered, so the console shows the state without offering a control
	// that would fail.
	CanChange bool `json:"can_change"`
}

// Mode handles GET /api/v1/auth/mode.
//
// The route carries no key requirement on purpose. A client that does not yet
// hold a gateway API key needs to know whether it has to go get one, and
// answering that question with 401 tells it nothing it can act on. The answer
// discloses only what an unauthenticated request would discover by making one.
func (c *AuthController) Mode(w http.ResponseWriter, _ *http.Request) {
	_ = dto.WriteJSON(w, http.StatusOK, AuthModeResponse{
		Mode:      c.mode,
		CanChange: c.canChange,
	})
}
