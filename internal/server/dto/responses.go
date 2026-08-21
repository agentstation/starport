// Package dto owns shared administrative HTTP response values.
package dto

import (
	"encoding/json"
	"net/http"
)

// ErrorResponse represents an API error response
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail contains error details
type ErrorDetail struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Code    string  `json:"code,omitempty"`
	Param   *string `json:"param,omitempty"`
}

// HealthResponse represents a health check response
type HealthResponse struct {
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
	Service   string `json:"service,omitempty"`
	Version   string `json:"version,omitempty"`
}

// ListResponse is the shared envelope for cursor-paginated listings.
type ListResponse struct {
	Data any `json:"data"`
	// NextCursor continues the listing; empty when the page is the last.
	NextCursor string `json:"next_cursor,omitempty"`
}

// WriteList writes a cursor-paginated listing response.
func WriteList(w http.ResponseWriter, status int, data any, nextCursor string) error {
	return WriteJSON(w, status, ListResponse{Data: data, NextCursor: nextCursor})
}

// WriteJSON writes a JSON response
func WriteJSON(w http.ResponseWriter, status int, data any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(data)
}

// WriteError writes an error response
func WriteError(w http.ResponseWriter, status int, errType, message string) {
	resp := ErrorResponse{
		Error: ErrorDetail{
			Message: message,
			Type:    errType,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}

// WriteValidationError writes a validation error response
func WriteValidationError(w http.ResponseWriter, field, message string) {
	resp := ErrorResponse{
		Error: ErrorDetail{
			Message: message,
			Type:    "invalid_request_error",
			Param:   &field,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(resp)
}

// Common error types for OpenAI compatibility
const (
	ErrorTypeInvalidRequest      = "invalid_request_error"
	ErrorTypeAuthenticationError = "authentication_error"
	ErrorTypePermissionError     = "permission_error"
	ErrorTypeNotFound            = "not_found_error"
	ErrorTypeRateLimit           = "rate_limit_error"
	ErrorTypeServerError         = "server_error"
	ErrorTypeServiceUnavailable  = "service_unavailable"
)
