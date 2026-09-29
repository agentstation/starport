package recovery

import (
	"errors"
	"strings"
	"unicode"
)

// PublishFilesRequest selects one canonical file role after restricted preparation.
// Role support and target paths belong to the host's file owners.
type PublishFilesRequest struct {
	PrepareRequest
	Role string
}

// Validate checks the request before configuration or target access.
func (r PublishFilesRequest) Validate() error {
	if err := r.PrepareRequest.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(r.Role) == "" || len(r.Role) > 128 || strings.ContainsFunc(r.Role, unicode.IsControl) {
		return errors.New("file publication requires a bounded canonical role")
	}
	return nil
}

// PublishFilesResult reports one published role and every remaining file disposition.
// The preparation barrier remains closed. This receipt never approves admission.
type PublishFilesResult struct {
	Preparation PrepareResult     `json:"preparation"`
	Role        string            `json:"role"`
	Tree        FileTreeResult    `json:"tree"`
	Remaining   []FileDisposition `json:"remaining"`
}
