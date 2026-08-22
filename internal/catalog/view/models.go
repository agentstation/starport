package view

import (
	"strconv"

	starmapcatalogs "github.com/agentstation/starmap/pkg/catalogs"

	runtimecatalog "github.com/agentstation/starport/internal/catalog"
)

// Models projects every routable model definition in the snapshot. A nil
// snapshot projects to nil.
func Models(snapshot *runtimecatalog.RoutableSnapshot) []ModelInfo {
	if snapshot == nil {
		return nil
	}
	definitions := snapshot.Definitions()
	models := make([]ModelInfo, 0, len(definitions))
	for _, definition := range definitions {
		created := definition.CreatedAt.Unix()
		if !definition.Metadata.ReleaseDate.IsZero() {
			created = definition.Metadata.ReleaseDate.Unix()
		}
		ownedBy := ""
		if len(definition.AuthorIDs) > 0 {
			ownedBy = string(definition.AuthorIDs[0])
		}
		model := ModelInfo{
			ID:      string(definition.ID),
			Object:  "model",
			Created: created,
			OwnedBy: ownedBy,
		}
		enrichModelInfo(snapshot, definition, &model)
		models = append(models, model)
	}
	return models
}

func enrichModelInfo(
	snapshot *runtimecatalog.RoutableSnapshot,
	definition starmapcatalogs.ModelDefinition,
	model *ModelInfo,
) {
	if snapshot == nil || model == nil {
		return
	}
	model.CanonicalSlug = string(definition.ID)
	model.Name = definition.Name
	model.Description = definition.Description
	if definition.Weights.Architecture != nil {
		model.Architecture = &ModelArchitecture{
			InputModalities:  modelInputModalities(definition),
			OutputModalities: modelOutputModalities(definition),
			Tokenizer:        definition.Weights.Architecture.Tokenizer.String(),
		}
	} else {
		model.Architecture = &ModelArchitecture{
			InputModalities:  modelInputModalities(definition),
			OutputModalities: modelOutputModalities(definition),
		}
	}
	model.SupportedParameters = supportedModelParameters(definition)
	routes := snapshot.RoutesForDefinition(definition.ID)
	if len(routes) == 0 {
		return
	}
	offering, err := snapshot.Offering(routes[0])
	if err != nil {
		return
	}
	if offering.Limits != nil {
		contextLength := boundedModelInt(offering.Limits.ContextWindow)
		model.Context = &contextLength
		model.TopProvider = &TopProviderInfo{
			ContextLength:       contextLength,
			MaxCompletionTokens: boundedModelInt(offering.Limits.OutputTokens),
		}
	}
	if offering.Pricing != nil && offering.Pricing.Tokens != nil {
		model.Pricing = &ModelPricing{Currency: offering.Pricing.Currency.String()}
		if offering.Pricing.Tokens.Input != nil {
			model.Pricing.Prompt = formatTokenPrice(offering.Pricing.Tokens.Input)
		}
		if offering.Pricing.Tokens.Output != nil {
			model.Pricing.Completion = formatTokenPrice(offering.Pricing.Tokens.Output)
		}
	}
}

func formatTokenPrice(cost *starmapcatalogs.ModelTokenCost) string {
	if cost == nil {
		return ""
	}
	value := cost.PerToken
	if value == 0 && cost.Per1M != 0 {
		value = cost.Per1M / 1_000_000
	}
	return strconv.FormatFloat(value, 'g', -1, 64)
}

func boundedModelInt(value int64) int {
	maxInt := int64(^uint(0) >> 1)
	if value > maxInt {
		return int(maxInt)
	}
	return int(value)
}

func modelInputModalities(definition starmapcatalogs.ModelDefinition) []string {
	if definition.Capabilities.Features == nil {
		return nil
	}
	result := make([]string, 0, len(definition.Capabilities.Features.Modalities.Input))
	for _, modality := range definition.Capabilities.Features.Modalities.Input {
		result = append(result, modality.String())
	}
	return result
}

func modelOutputModalities(definition starmapcatalogs.ModelDefinition) []string {
	if definition.Capabilities.Features == nil {
		return nil
	}
	result := make([]string, 0, len(definition.Capabilities.Features.Modalities.Output))
	for _, modality := range definition.Capabilities.Features.Modalities.Output {
		result = append(result, modality.String())
	}
	return result
}

func supportedModelParameters(definition starmapcatalogs.ModelDefinition) []string {
	features := definition.Capabilities.Features
	if features == nil {
		return nil
	}
	parameters := make([]string, 0, 16)
	for _, item := range []struct {
		name      string
		supported bool
	}{
		{"tools", features.Tools},
		{"tool_choice", features.ToolChoice},
		{"reasoning", features.Reasoning},
		{"reasoning_effort", features.ReasoningEffort},
		{"temperature", features.Temperature},
		{"top_p", features.TopP},
		{"top_k", features.TopK},
		{"max_tokens", features.MaxTokens || features.MaxOutputTokens},
		{"stop", features.Stop},
		{"frequency_penalty", features.FrequencyPenalty},
		{"presence_penalty", features.PresencePenalty},
		{"logit_bias", features.LogitBias},
		{"seed", features.Seed},
		{"logprobs", features.Logprobs},
		{"top_logprobs", features.TopLogprobs},
		{"n", features.N},
		{"response_format", features.StructuredOutputs},
	} {
		if item.supported {
			parameters = append(parameters, item.name)
		}
	}
	return parameters
}
