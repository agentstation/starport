// Package cache owns response-cache eligibility, semantic identity,
// versioned canonical records, and stream replay.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/agentstation/starport/internal/inference"
)

// SemanticKeyVersion identifies the canonical cache-identity encoding.
const SemanticKeyVersion = 2

var (
	// ErrIneligible reports a request whose identity is not cache-safe.
	ErrIneligible = errors.New("request is not eligible for response caching")
	// ErrTenantRequired reports a request without tenant identity.
	ErrTenantRequired = errors.New("cache tenant ID is required")
	// ErrGenerationRequired reports a request without catalog identity.
	ErrGenerationRequired = errors.New("catalog generation ID is required")
	// ErrMutableImage reports a remote image that can change without a new key.
	ErrMutableImage = errors.New("remote image input is mutable")
	// ErrUnknownExtension reports provider semantics outside the canonical identity.
	ErrUnknownExtension = errors.New("provider extension semantics are not cache-safe")
	// ErrInvalidJSONContract reports an invalid tool or output schema.
	ErrInvalidJSONContract = errors.New("request JSON contract is invalid")
)

// ProviderPolicy contains request-scoped provider routing semantics.
type ProviderPolicy struct {
	Order          []string          `json:"order,omitempty"`
	Only           []string          `json:"only,omitempty"`
	Ignore         []string          `json:"ignore,omitempty"`
	AllowFallbacks bool              `json:"allow_fallbacks"`
	Route          string            `json:"route,omitempty"`
	ModelOverrides map[string]string `json:"model_overrides,omitempty"`
}

// TenantPolicy contains tenant-scoped route restrictions.
type TenantPolicy struct {
	AllowedModels      []string `json:"allowed_models,omitempty"`
	AllowedProviders   []string `json:"allowed_providers,omitempty"`
	RateLimitTier      string   `json:"rate_limit_tier,omitempty"`
	CredentialStrategy string   `json:"credential_strategy,omitempty"`
}

// Policy contains all routing policy that can change a response.
type Policy struct {
	Provider ProviderPolicy `json:"provider"`
	Tenant   TenantPolicy   `json:"tenant"`
}

// ChatIdentity contains all semantic inputs for one chat cache entry.
type ChatIdentity struct {
	TenantID          string
	CatalogGeneration string
	Request           inference.ChatRequest
	Policy            Policy
}

// EmbeddingIdentity contains all semantic inputs for one embedding cache entry.
type EmbeddingIdentity struct {
	TenantID          string
	CatalogGeneration string
	Request           inference.EmbeddingRequest
	Policy            Policy
}

// ChatKey returns the versioned semantic key for an eligible chat request.
func ChatKey(identity ChatIdentity) (string, error) {
	if err := chatEligibility(identity); err != nil {
		return "", err
	}
	request := identity.Request.Clone()
	// Delivery format does not change the canonical completed result.
	request.Stream = false
	request.StreamOptions = inference.StreamOptions{}
	payload := struct {
		Version           int                   `json:"version"`
		TenantID          string                `json:"tenant_id"`
		CatalogGeneration string                `json:"catalog_generation"`
		Request           inference.ChatRequest `json:"request"`
		Policy            Policy                `json:"policy"`
	}{SemanticKeyVersion, identity.TenantID, identity.CatalogGeneration, request, normalizePolicy(identity.Policy)}
	return semanticKey("chat", payload)
}

// EmbeddingKey returns the versioned semantic key for an eligible embedding request.
func EmbeddingKey(identity EmbeddingIdentity) (string, error) {
	if err := commonEligibility(identity.TenantID, identity.CatalogGeneration); err != nil {
		return "", err
	}
	payload := struct {
		Version           int                        `json:"version"`
		TenantID          string                     `json:"tenant_id"`
		CatalogGeneration string                     `json:"catalog_generation"`
		Request           inference.EmbeddingRequest `json:"request"`
		Policy            Policy                     `json:"policy"`
	}{SemanticKeyVersion, identity.TenantID, identity.CatalogGeneration, identity.Request.Clone(), normalizePolicy(identity.Policy)}
	return semanticKey("embedding", payload)
}

func chatEligibility(identity ChatIdentity) error {
	if err := commonEligibility(identity.TenantID, identity.CatalogGeneration); err != nil {
		return err
	}
	if len(identity.Request.Extensions) > 0 {
		return fmt.Errorf("%w: %w", ErrIneligible, ErrUnknownExtension)
	}
	for _, message := range identity.Request.Messages {
		for _, part := range message.Content {
			if part.Kind == inference.ContentImage || part.Image != nil {
				return fmt.Errorf("%w: %w", ErrIneligible, ErrMutableImage)
			}
		}
	}
	for _, tool := range identity.Request.Tools {
		if len(tool.Parameters) > 0 && !json.Valid(tool.Parameters) {
			return fmt.Errorf("%w: tool %q: %w", ErrIneligible, tool.Name, ErrInvalidJSONContract)
		}
	}
	if len(identity.Request.Output.Schema) > 0 && !json.Valid(identity.Request.Output.Schema) {
		return fmt.Errorf("%w: output schema: %w", ErrIneligible, ErrInvalidJSONContract)
	}
	return nil
}

func commonEligibility(tenantID, generation string) error {
	if strings.TrimSpace(tenantID) == "" {
		return fmt.Errorf("%w: %w", ErrIneligible, ErrTenantRequired)
	}
	if strings.TrimSpace(generation) == "" {
		return fmt.Errorf("%w: %w", ErrIneligible, ErrGenerationRequired)
	}
	return nil
}

func normalizePolicy(policy Policy) Policy {
	policy.Provider.Order = append([]string(nil), policy.Provider.Order...)
	policy.Provider.Only = sortedCopy(policy.Provider.Only)
	policy.Provider.Ignore = sortedCopy(policy.Provider.Ignore)
	policy.Tenant.AllowedModels = sortedCopy(policy.Tenant.AllowedModels)
	policy.Tenant.AllowedProviders = sortedCopy(policy.Tenant.AllowedProviders)
	if policy.Provider.ModelOverrides != nil {
		cloned := make(map[string]string, len(policy.Provider.ModelOverrides))
		for model, override := range policy.Provider.ModelOverrides {
			cloned[model] = override
		}
		policy.Provider.ModelOverrides = cloned
	}
	return policy
}

func sortedCopy(values []string) []string {
	if values == nil {
		return nil
	}
	copyOfValues := append([]string(nil), values...)
	sort.Strings(copyOfValues)
	return copyOfValues
}

func semanticKey(kind string, payload any) (string, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode %s cache identity: %w", kind, err)
	}
	hash := sha256.Sum256(encoded)
	return fmt.Sprintf("responsecache:v%d:%s:%s", SemanticKeyVersion, kind, hex.EncodeToString(hash[:])), nil
}
