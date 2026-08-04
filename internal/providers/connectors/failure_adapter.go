package connectors

import (
	"context"
	"errors"
	"strings"

	"github.com/agentstation/starport/internal/failure"
)

// NormalizeFailure converts a provider error into the canonical failure seam.
func NormalizeFailure(provider string, err error) *failure.Failure {
	if err == nil {
		return nil
	}

	var normalized *failure.Failure
	if errors.As(err, &normalized) {
		return normalized
	}

	var apiError *APIError
	if errors.As(err, &apiError) {
		kind := failureKindFromAPIError(apiError)
		return failure.New(
			kind,
			safeProviderMessage(kind),
			apiError.IsRetryable(),
			failure.ProviderDetails{
				Provider:   firstNonEmpty(apiError.Provider, provider),
				StatusCode: apiError.StatusCode,
				Type:       apiError.Type,
				Code:       apiError.Code,
				Message:    apiError.Message,
			},
			err,
		)
	}

	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, ErrContextCanceled):
		return failure.New(failure.Canceled, "The request was canceled.", false, failure.ProviderDetails{Provider: provider}, err)
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, ErrTimeout):
		return failure.New(failure.Timeout, "The provider request timed out.", true, failure.ProviderDetails{Provider: provider}, err)
	case errors.Is(err, ErrRateLimited):
		return failure.New(failure.RateLimit, "The provider rate limit was reached.", true, failure.ProviderDetails{Provider: provider}, err)
	case errors.Is(err, ErrInvalidAPIKey):
		return failure.New(failure.Authentication, "Provider authentication failed.", false, failure.ProviderDetails{Provider: provider}, err)
	default:
		return failure.New(failure.ProviderUnavailable, "The provider request failed.", true, failure.ProviderDetails{Provider: provider}, err)
	}
}

func failureKindFromAPIError(apiError *APIError) failure.Kind {
	if apiError != nil && (apiError.StatusCode == 400 || apiError.StatusCode == 422) {
		message := strings.ToLower(apiError.Message)
		for _, marker := range []string{"context length", "context_length_exceeded", "max_tokens", "token limit", "maximum context"} {
			if strings.Contains(message, marker) {
				return failure.ContextLimit
			}
		}
		for _, marker := range []string{"content_policy", "content moderation", "safety", "harmful", "inappropriate"} {
			if strings.Contains(message, marker) {
				return failure.ContentBlocked
			}
		}
	}
	return failureKindFromStatus(apiError.StatusCode)
}

func failureKindFromStatus(status int) failure.Kind {
	switch status {
	case 400, 422:
		return failure.Validation
	case 401:
		return failure.Authentication
	case 403:
		return failure.Permission
	case 404:
		return failure.NotFound
	case 408, 504:
		return failure.Timeout
	case 429:
		return failure.RateLimit
	default:
		return failure.ProviderUnavailable
	}
}

func safeProviderMessage(kind failure.Kind) string {
	switch kind {
	case failure.Validation:
		return "The provider rejected the request."
	case failure.Authentication:
		return "Provider authentication failed."
	case failure.Permission:
		return "The provider denied the request."
	case failure.NotFound:
		return "The provider model was not found."
	case failure.ContextLimit:
		return "The request exceeded the provider context limit."
	case failure.ContentBlocked:
		return "The provider blocked the request content."
	case failure.Timeout:
		return "The provider request timed out."
	case failure.RateLimit:
		return "The provider rate limit was reached."
	default:
		return "The provider request failed."
	}
}

func firstNonEmpty(first, second string) string {
	if first != "" {
		return first
	}
	return second
}
