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

// SemanticKeyVersion identifies the canonical cache-identity encoding. The
// key payload embeds inference.ChatRequest, and a canonical type carries no
// transport tag, so a field added to that struct reaches the hash even when
// no caller sets it. Raise this constant in the same change: the entries a
// running gateway holds keep their own prefix, and none of them is read back
// under an encoding that did not write it.
//
// Version 3 added the output modality and audio output request fields.
// Version 4 added the stored document reference.
// Version 5 added the document parser the caller asked for. The same bytes
// read by two engines are two different inputs to the model.
const SemanticKeyVersion = 5

var (
	// ErrIneligible reports a request whose identity is not cache-safe.
	ErrIneligible = errors.New("request is not eligible for response caching")
	// ErrTenantRequired reports a request without tenant identity.
	ErrTenantRequired = errors.New("cache tenant ID is required")
	// ErrGenerationRequired reports a request without catalog identity.
	ErrGenerationRequired = errors.New("catalog generation ID is required")
	// ErrMutableMedia reports a remote media input that can change without a
	// new key.
	ErrMutableMedia = errors.New("remote media input is mutable")
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

	// Sort and the price caps change which route serves the response, so
	// they are part of the cache identity.
	Sort                    string  `json:"sort,omitempty"`
	MaxPromptPricePer1M     float64 `json:"max_prompt_price_per_1m,omitempty"`
	MaxCompletionPricePer1M float64 `json:"max_completion_price_per_1m,omitempty"`
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
	request.OutputModalities = normalizeModalities(request.OutputModalities)
	// Inline media reaches the key as a digest rather than as bytes. A
	// request that carries no media folds nothing, so a text-only request
	// pays none of the cost of the media rule.
	digests := foldInlineMedia(&request)
	payload := struct {
		Version           int                   `json:"version"`
		TenantID          string                `json:"tenant_id"`
		CatalogGeneration string                `json:"catalog_generation"`
		Request           inference.ChatRequest `json:"request"`
		MediaDigests      []string              `json:"media_digests,omitempty"`
		Policy            Policy                `json:"policy"`
	}{
		SemanticKeyVersion, identity.TenantID, identity.CatalogGeneration,
		request, digests, normalizePolicy(identity.Policy),
	}
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
			if kind := remoteMediaKind(part); kind != "" {
				return fmt.Errorf("%w: %s: %w", ErrIneligible, kind, ErrMutableMedia)
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

// normalizeModalities reduces an output modality list to the shortest spelling
// that means the same thing. A caller that asks for text alone asks for what
// every model already served, so that list drops out and the request keys as
// the text request it is. The rest sort, because the field names a set: a
// caller that lists audio before text wants the answer the other order
// produced, and a cache that disagreed would pay a provider twice for it.
func normalizeModalities(modalities []inference.Modality) []inference.Modality {
	seen := make(map[inference.Modality]bool, len(modalities))
	unique := make([]inference.Modality, 0, len(modalities))
	for _, modality := range modalities {
		if seen[modality] {
			continue
		}
		seen[modality] = true
		unique = append(unique, modality)
	}
	if len(unique) == 0 || (len(unique) == 1 && unique[0] == inference.ModalityText) {
		return nil
	}
	sort.Slice(unique, func(i, j int) bool { return unique[i] < unique[j] })
	return unique
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
