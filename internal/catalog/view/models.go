package view

import (
	"sort"
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
	model.Authors = modelAuthors(snapshot, definition)
	if len(definition.Metadata.Tags) > 0 {
		tags := make([]string, 0, len(definition.Metadata.Tags))
		for _, tag := range definition.Metadata.Tags {
			tags = append(tags, string(tag))
		}
		model.Tags = tags
	}
	model.Lineage = modelLineage(definition)
	if cutoff := definition.Metadata.KnowledgeCutoff; cutoff != nil && !cutoff.IsZero() {
		model.KnowledgeCutoff = cutoff.Format("2006-01-02")
	}
	if definition.Weights.Open != nil {
		open := *definition.Weights.Open
		model.OpenWeights = &open
	}
	routes := snapshot.RoutesForDefinition(definition.ID)
	if len(routes) == 0 {
		return
	}
	model.Offerings = modelOfferings(snapshot, routes)
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

func modelAuthors(
	snapshot *runtimecatalog.RoutableSnapshot,
	definition starmapcatalogs.ModelDefinition,
) []ModelAuthorInfo {
	if len(definition.AuthorIDs) == 0 {
		return nil
	}
	authors := make([]ModelAuthorInfo, 0, len(definition.AuthorIDs))
	for _, authorID := range definition.AuthorIDs {
		info := ModelAuthorInfo{ID: string(authorID)}
		if author, err := snapshot.Catalog().Author(authorID); err == nil {
			info.Name = author.Name
		}
		authors = append(authors, info)
	}
	return authors
}

func modelLineage(definition starmapcatalogs.ModelDefinition) *ModelLineageInfo {
	lineage := definition.Lineage
	if lineage.Family == "" && lineage.Root == nil && lineage.Parent == nil {
		return nil
	}
	info := &ModelLineageInfo{Family: lineage.Family}
	if lineage.Root != nil {
		info.Root = string(*lineage.Root)
	}
	if lineage.Parent != nil {
		info.Parent = string(*lineage.Parent)
	}
	return info
}

func modelOfferings(
	snapshot *runtimecatalog.RoutableSnapshot,
	routes []runtimecatalog.Route,
) []ModelOfferingInfo {
	offerings := make([]ModelOfferingInfo, 0, len(routes))
	for _, route := range routes {
		offering, err := snapshot.Offering(route)
		if err != nil {
			continue
		}
		info := ModelOfferingInfo{
			Provider:        string(offering.ProviderID),
			ProviderModelID: string(offering.ProviderModelID),
			Availability:    string(offering.Availability),
			Lifecycle:       string(offering.Lifecycle),
		}
		if provider, err := snapshot.Catalog().Provider(offering.ProviderID); err == nil {
			info.ProviderName = provider.Name
		}
		if offering.Limits != nil {
			contextLength := boundedModelInt(offering.Limits.ContextWindow)
			maxCompletion := boundedModelInt(offering.Limits.OutputTokens)
			info.ContextLength = &contextLength
			info.MaxCompletionTokens = &maxCompletion
		}
		info.Pricing = offeringPricing(offering.Pricing)
		offerings = append(offerings, info)
	}
	sort.Slice(offerings, func(left, right int) bool {
		if offerings[left].Provider != offerings[right].Provider {
			return offerings[left].Provider < offerings[right].Provider
		}
		return offerings[left].ProviderModelID < offerings[right].ProviderModelID
	})
	return offerings
}

func offeringPricing(pricing *starmapcatalogs.ModelPricing) *OfferingPricingInfo {
	if pricing == nil || pricing.Tokens == nil {
		return nil
	}
	return &OfferingPricingInfo{
		Prompt:     formatTokenPrice(pricing.Tokens.Input),
		Completion: formatTokenPrice(pricing.Tokens.Output),
		Reasoning:  formatTokenPrice(pricing.Tokens.Reasoning),
		CacheRead:  formatTokenPrice(pricing.Tokens.CacheRead),
		CacheWrite: formatTokenPrice(pricing.Tokens.CacheWrite),
		Currency:   pricing.Currency.String(),
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
	parameters := make([]string, 0, 17)
	for _, item := range []struct {
		name      string
		supported bool
	}{
		{"tools", features.Tools},
		{"tool_choice", features.ToolChoice},
		{"web_search_options", features.WebSearch},
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
