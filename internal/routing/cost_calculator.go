package routing

import (
	"strings"
)

// ModelPricing contains pricing information for a model
type ModelPricing struct {
	PromptCostPer1M     float64 // Cost per 1M prompt tokens
	CompletionCostPer1M float64 // Cost per 1M completion tokens
}

// CostCalculator calculates costs for different models
type CostCalculator interface {
	// GetModelCost returns the pricing for a model
	GetModelCost(modelID string) (ModelPricing, bool)

	// EstimateCost estimates the cost for a request
	EstimateCost(modelID string, promptTokens, completionTokens int) float64

	// CompareModelCosts compares costs between models
	CompareModelCosts(modelID1, modelID2 string, promptTokens, completionTokens int) float64
}

// defaultCostCalculator implements CostCalculator with hardcoded pricing
type defaultCostCalculator struct {
	modelPricing map[string]ModelPricing
}

// NewCostCalculator creates a new cost calculator with default pricing
func NewCostCalculator() CostCalculator {
	return &defaultCostCalculator{
		modelPricing: getDefaultPricing(),
	}
}

// GetModelCost returns the pricing for a model
func (c *defaultCostCalculator) GetModelCost(modelID string) (ModelPricing, bool) {
	pricing, exists := c.modelPricing[modelID]
	if exists {
		return pricing, true
	}

	// Try without provider prefix for fallback
	parts := strings.SplitN(modelID, "/", 2)
	if len(parts) == 2 {
		if pricing, exists := c.modelPricing[parts[1]]; exists {
			return pricing, true
		}
	}

	return ModelPricing{}, false
}

// EstimateCost estimates the cost for a request in dollars
func (c *defaultCostCalculator) EstimateCost(modelID string, promptTokens, completionTokens int) float64 {
	pricing, exists := c.GetModelCost(modelID)
	if !exists {
		return 0
	}

	promptCost := (float64(promptTokens) / 1_000_000) * pricing.PromptCostPer1M
	completionCost := (float64(completionTokens) / 1_000_000) * pricing.CompletionCostPer1M

	return promptCost + completionCost
}

// CompareModelCosts returns the cost ratio between two models (model1 / model2)
func (c *defaultCostCalculator) CompareModelCosts(modelID1, modelID2 string, promptTokens, completionTokens int) float64 {
	cost1 := c.EstimateCost(modelID1, promptTokens, completionTokens)
	cost2 := c.EstimateCost(modelID2, promptTokens, completionTokens)

	if cost2 == 0 {
		return 0
	}

	return cost1 / cost2
}

// getDefaultPricing returns default model pricing as of January 2024
func getDefaultPricing() map[string]ModelPricing {
	return map[string]ModelPricing{
		// OpenAI models
		"openai/gpt-4-turbo-preview": {
			PromptCostPer1M:     10.0,
			CompletionCostPer1M: 30.0,
		},
		"openai/gpt-4": {
			PromptCostPer1M:     30.0,
			CompletionCostPer1M: 60.0,
		},
		"openai/gpt-4-32k": {
			PromptCostPer1M:     60.0,
			CompletionCostPer1M: 120.0,
		},
		"openai/gpt-3.5-turbo": {
			PromptCostPer1M:     0.5,
			CompletionCostPer1M: 1.5,
		},
		"openai/gpt-3.5-turbo-16k": {
			PromptCostPer1M:     3.0,
			CompletionCostPer1M: 4.0,
		},

		// Anthropic models
		"anthropic/claude-3-opus-20240229": {
			PromptCostPer1M:     15.0,
			CompletionCostPer1M: 75.0,
		},
		"anthropic/claude-3-sonnet-20240229": {
			PromptCostPer1M:     3.0,
			CompletionCostPer1M: 15.0,
		},
		"anthropic/claude-3-haiku-20240307": {
			PromptCostPer1M:     0.25,
			CompletionCostPer1M: 1.25,
		},
		"anthropic/claude-2.1": {
			PromptCostPer1M:     8.0,
			CompletionCostPer1M: 24.0,
		},
		"anthropic/claude-2.0": {
			PromptCostPer1M:     8.0,
			CompletionCostPer1M: 24.0,
		},
		"anthropic/claude-instant-1.2": {
			PromptCostPer1M:     0.8,
			CompletionCostPer1M: 2.4,
		},

		// Google AI Studio models
		"google-aistudio/gemini-1.5-pro": {
			PromptCostPer1M:     3.5,
			CompletionCostPer1M: 10.5,
		},
		"google-aistudio/gemini-1.5-pro-latest": {
			PromptCostPer1M:     3.5,
			CompletionCostPer1M: 10.5,
		},
		"google-aistudio/gemini-1.5-flash": {
			PromptCostPer1M:     0.35,
			CompletionCostPer1M: 0.70,
		},
		"google-aistudio/gemini-1.5-flash-latest": {
			PromptCostPer1M:     0.35,
			CompletionCostPer1M: 0.70,
		},
		"google-aistudio/gemini-pro": {
			PromptCostPer1M:     0.5,
			CompletionCostPer1M: 1.5,
		},
		"google-aistudio/gemini-pro-vision": {
			PromptCostPer1M:     0.5,
			CompletionCostPer1M: 1.5,
		},

		// Google Vertex AI models (different pricing for enterprise)
		"google-vertexai/gemini-1.5-pro": {
			PromptCostPer1M:     1.25,
			CompletionCostPer1M: 3.75,
		},
		"google-vertexai/gemini-1.5-flash": {
			PromptCostPer1M:     0.125,
			CompletionCostPer1M: 0.375,
		},
		"google-vertexai/text-bison@001": {
			PromptCostPer1M:     0.125,
			CompletionCostPer1M: 0.125,
		},
		"google-vertexai/code-bison@001": {
			PromptCostPer1M:     0.125,
			CompletionCostPer1M: 0.125,
		},
		// Claude via Vertex AI Model Garden (same as Anthropic pricing)
		"google-vertexai/claude-3-opus@20240229": {
			PromptCostPer1M:     15.0,
			CompletionCostPer1M: 75.0,
		},
		"google-vertexai/claude-3-sonnet@20240229": {
			PromptCostPer1M:     3.0,
			CompletionCostPer1M: 15.0,
		},
		"google-vertexai/claude-3-haiku@20240307": {
			PromptCostPer1M:     0.25,
			CompletionCostPer1M: 1.25,
		},

		// Groq models (very competitive pricing)
		"groq/llama-3.1-405b-reasoning": {
			PromptCostPer1M:     0.95,
			CompletionCostPer1M: 0.95,
		},
		"groq/llama-3.1-70b-versatile": {
			PromptCostPer1M:     0.64,
			CompletionCostPer1M: 0.80,
		},
		"groq/llama-3.1-8b-instant": {
			PromptCostPer1M:     0.05,
			CompletionCostPer1M: 0.10,
		},
		"groq/llama3-70b-8192": {
			PromptCostPer1M:     0.64,
			CompletionCostPer1M: 0.80,
		},
		"groq/llama3-8b-8192": {
			PromptCostPer1M:     0.05,
			CompletionCostPer1M: 0.10,
		},
		"groq/mixtral-8x7b-32768": {
			PromptCostPer1M:     0.27,
			CompletionCostPer1M: 0.27,
		},
		"groq/gemma-7b-it": {
			PromptCostPer1M:     0.10,
			CompletionCostPer1M: 0.10,
		},

		// Mistral models
		"mistral/mistral-large-latest": {
			PromptCostPer1M:     4.0,
			CompletionCostPer1M: 12.0,
		},
		"mistral/mistral-medium-latest": {
			PromptCostPer1M:     2.7,
			CompletionCostPer1M: 8.1,
		},
		"mistral/mistral-small-latest": {
			PromptCostPer1M:     1.0,
			CompletionCostPer1M: 3.0,
		},
		"mistral/open-mistral-7b": {
			PromptCostPer1M:     0.25,
			CompletionCostPer1M: 0.25,
		},
		"mistral/open-mixtral-8x7b": {
			PromptCostPer1M:     0.7,
			CompletionCostPer1M: 0.7,
		},
		"mistral/open-mixtral-8x22b": {
			PromptCostPer1M:     2.0,
			CompletionCostPer1M: 6.0,
		},

		// Azure models (same as OpenAI)
		"azure/gpt-4": {
			PromptCostPer1M:     30.0,
			CompletionCostPer1M: 60.0,
		},
		"azure/gpt-4-32k": {
			PromptCostPer1M:     60.0,
			CompletionCostPer1M: 120.0,
		},
		"azure/gpt-35-turbo": {
			PromptCostPer1M:     0.5,
			CompletionCostPer1M: 1.5,
		},
		"azure/gpt-35-turbo-16k": {
			PromptCostPer1M:     3.0,
			CompletionCostPer1M: 4.0,
		},
	}
}

// CostOptimizedSelector selects models based on cost optimization
type CostOptimizedSelector struct {
	calculator           CostCalculator
	maxCostMultiplier    float64
	latencyTracker       LatencyTracker
	maxLatencyMultiplier float64
}

// NewCostOptimizedSelector creates a selector that balances cost and latency
func NewCostOptimizedSelector(calculator CostCalculator, latencyTracker LatencyTracker, maxCostMultiplier, maxLatencyMultiplier float64) *CostOptimizedSelector {
	if maxCostMultiplier <= 0 {
		maxCostMultiplier = 2.0
	}
	if maxLatencyMultiplier <= 0 {
		maxLatencyMultiplier = 2.0
	}

	return &CostOptimizedSelector{
		calculator:           calculator,
		maxCostMultiplier:    maxCostMultiplier,
		latencyTracker:       latencyTracker,
		maxLatencyMultiplier: maxLatencyMultiplier,
	}
}

// SelectModel selects the most cost-effective model within latency constraints
func (s *CostOptimizedSelector) SelectModel(models []string, estimatedPromptTokens, estimatedCompletionTokens int) string {
	if len(models) == 0 {
		return ""
	}

	type modelScore struct {
		modelID string
		cost    float64
		latency float64
		score   float64
	}

	var scores []modelScore
	latencies := s.latencyTracker.GetAllLatencies()

	// Calculate scores for each model
	for _, modelID := range models {
		cost := s.calculator.EstimateCost(modelID, estimatedPromptTokens, estimatedCompletionTokens)
		if cost == 0 {
			// Unknown pricing, skip
			continue
		}

		provider := extractProviderFromModel(modelID)
		latency := float64(latencies[provider].Milliseconds())
		if latency == 0 {
			// No latency data, use a default
			latency = 1000 // 1 second default
		}

		// Simple scoring: lower is better
		// Normalize both cost and latency to 0-1 range, then combine
		// This is a simplified version - in production, you'd want more sophisticated scoring
		score := cost*1000 + latency // Cost in dollars * 1000 + latency in ms

		scores = append(scores, modelScore{
			modelID: modelID,
			cost:    cost,
			latency: latency,
			score:   score,
		})
	}

	if len(scores) == 0 {
		// No models with pricing data, return first
		return models[0]
	}

	// Find best score
	bestIdx := 0
	for i := 1; i < len(scores); i++ {
		if scores[i].score < scores[bestIdx].score {
			bestIdx = i
		}
	}

	return scores[bestIdx].modelID
}

func extractProviderFromModel(modelID string) string {
	parts := strings.SplitN(modelID, "/", 2)
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
}
