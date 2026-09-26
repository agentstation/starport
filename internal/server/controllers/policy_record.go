package controllers

import (
	"errors"
	"github.com/agentstation/starport/internal/policyrecord"
	"github.com/agentstation/starport/internal/server/dto"
	"net/http"
)

// writePolicySizeRefusal reports a rejected policy write without exposing its contents.
func writePolicySizeRefusal(w http.ResponseWriter, err error) bool {
	if !errors.Is(err, policyrecord.ErrTooLarge) {
		return false
	}
	dto.WriteError(w, http.StatusRequestEntityTooLarge, dto.ErrorTypeInvalidRequest, "Authorization record exceeds 64 KiB. Reduce metadata or policy entries.")
	return true
}
