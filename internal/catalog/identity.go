package catalog

import "strings"

// SplitModelID splits one provider-scoped model ID.
func SplitModelID(modelID string) (provider, model string, ok bool) {
	provider, model, ok = strings.Cut(modelID, "/")
	if !ok || provider == "" || model == "" {
		return "", modelID, false
	}
	return provider, model, true
}

// ProviderFromModelID returns the adapter ID named by a provider-scoped model ID.
func ProviderFromModelID(modelID string) string {
	provider, _, ok := SplitModelID(modelID)
	if !ok {
		return ""
	}
	return provider
}
