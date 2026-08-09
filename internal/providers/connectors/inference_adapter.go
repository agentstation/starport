package connectors

import (
	"encoding/json"
	"fmt"

	"github.com/agentstation/starport/internal/inference"
)

// ChatRequestFromInference converts a canonical request to provider wire values.
func ChatRequestFromInference(request inference.ChatRequest) (*ChatRequest, error) {
	request = request.Clone()
	messages, err := messagesFromInference(request.Messages)
	if err != nil {
		return nil, err
	}
	tools := make([]Tool, len(request.Tools))
	for i, tool := range request.Tools {
		var parameters any
		if len(tool.Parameters) > 0 {
			if err := json.Unmarshal(tool.Parameters, &parameters); err != nil {
				return nil, fmt.Errorf("decode tool %q parameters: %w", tool.Name, err)
			}
		}
		tools[i] = Tool{Type: toolTypeFunction, Function: Function{Name: tool.Name, Description: tool.Description, Parameters: parameters}}
	}

	providerOptions := make(map[string]any, len(request.Extensions))
	for name, raw := range request.Extensions {
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, fmt.Errorf("decode extension %q: %w", name, err)
		}
		providerOptions[name] = value
	}
	if len(providerOptions) == 0 {
		providerOptions = nil
	}

	wireRequest := &ChatRequest{
		Model:            request.Model,
		Messages:         messages,
		Temperature:      request.Sampling.Temperature,
		TopP:             request.Sampling.TopP,
		N:                request.Sampling.CandidateCount,
		MaxTokens:        request.Sampling.MaxTokens,
		Stream:           request.Stream,
		Stop:             append([]string(nil), request.Sampling.Stop...),
		PresencePenalty:  request.Sampling.PresencePenalty,
		FrequencyPenalty: request.Sampling.FrequencyPenalty,
		LogitBias:        cloneIntMap(request.Sampling.LogitBias),
		User:             request.User,
		Seed:             request.Sampling.Seed,
		Tools:            tools,
		ToolChoice:       toolChoiceFromInference(request.ToolChoice),
		ResponseFormat:   responseFormatFromInference(request.Output),
		Models:           append([]string(nil), request.FallbackModels...),
		ProviderOptions:  providerOptions,
	}
	if request.StreamOptions.IncludeUsage {
		wireRequest.StreamOptions = &StreamOptions{IncludeUsage: true}
	}
	if request.Reasoning.Effort != "" || request.Reasoning.MaxTokens != nil || request.Reasoning.Exclude {
		wireRequest.Reasoning = &ReasoningConfig{
			Effort: string(request.Reasoning.Effort), MaxTokens: request.Reasoning.MaxTokens,
			Exclude: request.Reasoning.Exclude,
		}
	}
	return wireRequest, nil
}

// ChatRequestToInference converts provider wire values to a canonical request.
func ChatRequestToInference(request *ChatRequest) (inference.ChatRequest, error) {
	if request == nil {
		return inference.ChatRequest{}, fmt.Errorf("chat request is required")
	}
	messages := make([]inference.Message, len(request.Messages))
	for i, message := range request.Messages {
		converted, err := messageToInference(message)
		if err != nil {
			return inference.ChatRequest{}, fmt.Errorf("convert message %d: %w", i, err)
		}
		messages[i] = converted
	}
	tools := make([]inference.Tool, len(request.Tools))
	for i, tool := range request.Tools {
		parameters, err := json.Marshal(tool.Function.Parameters)
		if err != nil {
			return inference.ChatRequest{}, fmt.Errorf("encode tool %q parameters: %w", tool.Function.Name, err)
		}
		tools[i] = inference.Tool{Name: tool.Function.Name, Description: tool.Function.Description, Parameters: parameters}
	}
	extensions := make(map[string]json.RawMessage, len(request.ProviderOptions))
	for name, value := range request.ProviderOptions {
		raw, err := json.Marshal(value)
		if err != nil {
			return inference.ChatRequest{}, fmt.Errorf("encode extension %q: %w", name, err)
		}
		extensions[name] = raw
	}
	if len(extensions) == 0 {
		extensions = nil
	}

	canonical := inference.ChatRequest{
		Model: request.Model, FallbackModels: append([]string(nil), request.Models...), Messages: messages,
		Sampling: inference.Sampling{
			Temperature: request.Temperature, TopP: request.TopP, CandidateCount: request.N,
			MaxTokens: request.MaxTokens, Stop: append([]string(nil), request.Stop...),
			PresencePenalty: request.PresencePenalty, FrequencyPenalty: request.FrequencyPenalty,
			LogitBias: cloneIntMap(request.LogitBias), Seed: request.Seed,
		},
		Tools: tools, ToolChoice: toolChoiceToInference(request.ToolChoice),
		Output: outputToInference(request.ResponseFormat), Stream: request.Stream, User: request.User,
		Extensions: extensions,
	}
	if request.StreamOptions != nil {
		canonical.StreamOptions.IncludeUsage = request.StreamOptions.IncludeUsage
	}
	if request.Reasoning != nil {
		canonical.Reasoning = inference.Reasoning{
			Effort:    inference.ReasoningEffort(request.Reasoning.Effort),
			MaxTokens: request.Reasoning.MaxTokens, Exclude: request.Reasoning.Exclude,
		}
	}
	return canonical.Clone(), nil
}

// ChatResponseToInference converts provider wire values to a canonical response.
func ChatResponseToInference(response *ChatResponse, modelUsed string) (inference.ChatResponse, error) {
	if response == nil {
		return inference.ChatResponse{}, fmt.Errorf("chat response is required")
	}
	choices := make([]inference.Choice, len(response.Choices))
	for i, choice := range response.Choices {
		message, err := messageToInference(choice.Message)
		if err != nil {
			return inference.ChatResponse{}, fmt.Errorf("convert choice %d: %w", choice.Index, err)
		}
		choices[i] = inference.Choice{
			Index:        choice.Index,
			Message:      message,
			FinishReason: choice.FinishReason,
			LogProbs:     logProbsToInference(choice.LogProbs),
		}
	}
	return inference.ChatResponse{
		ID:                response.ID,
		CreatedUnix:       response.Created,
		Model:             response.Model,
		ModelUsed:         modelUsed,
		Choices:           choices,
		Usage:             usageToInference(response.Usage),
		SystemFingerprint: response.SystemFingerprint,
	}, nil
}

// ChatResponseFromInference converts a canonical response to wire-compatible values.
func ChatResponseFromInference(response inference.ChatResponse) (*ChatResponse, error) {
	choices := make([]Choice, len(response.Choices))
	for i, choice := range response.Choices {
		messages, err := messagesFromInference([]inference.Message{choice.Message})
		if err != nil {
			return nil, fmt.Errorf("convert choice %d: %w", choice.Index, err)
		}
		choices[i] = Choice{
			Index: choice.Index, Message: messages[0], FinishReason: choice.FinishReason,
			LogProbs: logProbsFromInference(choice.LogProbs),
		}
	}
	return &ChatResponse{
		ID: response.ID, Object: objectChatCompletion, Created: response.CreatedUnix,
		Model: response.Model, Choices: choices, Usage: usageFromInference(response.Usage),
		SystemFingerprint: response.SystemFingerprint,
	}, nil
}

// StreamEventsToInference converts one provider chunk to typed canonical events.
func StreamEventsToInference(chunk *ChatStreamChunk, modelUsed string) ([]inference.StreamEvent, error) {
	if chunk == nil {
		return nil, fmt.Errorf("stream chunk is required")
	}
	events := make([]inference.StreamEvent, 0, 2)
	model := chunk.Model
	if model == "" {
		model = modelUsed
	}
	if len(chunk.Choices) > 0 {
		deltas := make([]inference.ChoiceDelta, len(chunk.Choices))
		for i, choice := range chunk.Choices {
			deltas[i] = inference.ChoiceDelta{
				Index:        choice.Index,
				Role:         inference.Role(choice.Delta.Role),
				Text:         choice.Delta.Content,
				Reasoning:    choice.Delta.Reasoning,
				ToolCalls:    toolCallsToInference(choice.Delta.ToolCalls),
				LogProbs:     logProbsToInference(choice.LogProbs),
				FinishReason: choice.FinishReason,
			}
		}
		events = append(events, inference.StreamEvent{
			Kind:              streamEventKind(deltas),
			ID:                chunk.ID,
			CreatedUnix:       chunk.Created,
			Model:             model,
			ModelUsed:         modelUsed,
			SystemFingerprint: chunk.SystemFingerprint,
			Deltas:            deltas,
		})
	}
	if chunk.Usage != nil {
		usage := usageToInference(*chunk.Usage)
		events = append(events, inference.StreamEvent{
			Kind:        inference.StreamUsage,
			ID:          chunk.ID,
			CreatedUnix: chunk.Created,
			Model:       model,
			ModelUsed:   modelUsed,
			Usage:       &usage,
		})
	}
	return events, nil
}

func streamEventKind(deltas []inference.ChoiceDelta) inference.StreamEventKind {
	allStart := len(deltas) > 0
	allEnd := len(deltas) > 0
	for _, delta := range deltas {
		allStart = allStart && delta.Role != "" && delta.Text == "" && delta.Reasoning == "" && len(delta.ToolCalls) == 0 && delta.FinishReason == ""
		allEnd = allEnd && delta.FinishReason != "" && delta.Role == "" && delta.Text == "" && delta.Reasoning == "" && len(delta.ToolCalls) == 0
	}
	if allStart {
		return inference.StreamStart
	}
	if allEnd {
		return inference.StreamEnd
	}
	return inference.StreamDelta
}

// StreamChunkFromInference converts one canonical event to a wire chunk.
func StreamChunkFromInference(event inference.StreamEvent) *ChatStreamChunk {
	chunk := &ChatStreamChunk{
		ID: event.ID, Object: objectChatCompletionChunk, Created: event.CreatedUnix,
		Model: event.Model, SystemFingerprint: event.SystemFingerprint,
	}
	if chunk.Model == "" {
		chunk.Model = event.ModelUsed
	}
	if event.Usage != nil {
		usage := usageFromInference(*event.Usage)
		chunk.Usage = &usage
	}
	if len(event.Deltas) > 0 {
		chunk.Choices = make([]StreamChoice, len(event.Deltas))
		for i, delta := range event.Deltas {
			chunk.Choices[i] = StreamChoice{
				Index: delta.Index,
				Delta: MessageDelta{
					Role: string(delta.Role), Content: delta.Text, Reasoning: delta.Reasoning,
					ToolCalls: toolCallsFromInference(delta.ToolCalls),
				},
				FinishReason: delta.FinishReason,
				LogProbs:     logProbsFromInference(delta.LogProbs),
			}
		}
	}
	return chunk
}

// EmbeddingRequestFromInference converts a canonical embedding request.
func EmbeddingRequestFromInference(request inference.EmbeddingRequest) *EmbeddingsRequest {
	var input any
	switch {
	case len(request.Input.TokenIDs) > 0:
		input = request.Input.TokenIDs
	case len(request.Input.Texts) == 1:
		input = request.Input.Texts[0]
	default:
		input = request.Input.Texts
	}
	return &EmbeddingsRequest{
		Model:          request.Model,
		Input:          input,
		EncodingFormat: request.EncodingFormat,
		Dimensions:     request.Dimensions,
		User:           request.User,
	}
}

// EmbeddingResponseToInference converts a provider embedding response.
func EmbeddingResponseToInference(response *EmbeddingsResponse) (inference.EmbeddingResponse, error) {
	if response == nil {
		return inference.EmbeddingResponse{}, fmt.Errorf("embedding response is required")
	}
	data := make([]inference.Embedding, len(response.Data))
	for i, embedding := range response.Data {
		data[i] = inference.Embedding{Index: embedding.Index, Vector: append([]float32(nil), embedding.Embedding...)}
	}
	return inference.EmbeddingResponse{Model: response.Model, Data: data, Usage: usageToInference(response.Usage)}, nil
}

// EmbeddingResponseFromInference converts a canonical embedding response.
func EmbeddingResponseFromInference(response inference.EmbeddingResponse) *EmbeddingsResponse {
	data := make([]Embedding, len(response.Data))
	for i, embedding := range response.Data {
		data[i] = Embedding{Object: objectEmbedding, Index: embedding.Index, Embedding: append([]float32(nil), embedding.Vector...)}
	}
	return &EmbeddingsResponse{Object: objectList, Data: data, Model: response.Model, Usage: usageFromInference(response.Usage)}
}

func messagesFromInference(messages []inference.Message) ([]Message, error) {
	converted := make([]Message, len(messages))
	for i, message := range messages {
		content := make([]ContentPart, len(message.Content))
		for j, part := range message.Content {
			content[j].Type = string(part.Kind)
			content[j].Text = part.Text
			if part.Kind == inference.ContentImage && part.Image != nil {
				content[j].Type = "image_url"
				content[j].ImageURL = &ImageURL{URL: part.Image.URL, Detail: part.Image.Detail}
			}
			if part.CacheControl != "" {
				content[j].CacheControl = &CacheControl{Type: part.CacheControl}
			}
		}
		var wireContent MessageContent = content
		if len(content) == 1 && content[0].Type == contentTypeText && content[0].CacheControl == nil {
			wireContent = content[0].Text
		}
		converted[i] = Message{
			Role:       string(message.Role),
			Content:    wireContent,
			Reasoning:  message.Reasoning,
			Name:       message.Name,
			ToolCalls:  toolCallsFromInference(message.ToolCalls),
			ToolCallID: message.ToolCallID,
		}
	}
	return converted, nil
}

func messageToInference(message Message) (inference.Message, error) {
	parts, err := ParseMessageContent(message.Content)
	if err != nil {
		return inference.Message{}, err
	}
	content := make([]inference.ContentPart, len(parts))
	for i, part := range parts {
		content[i] = inference.ContentPart{Kind: inference.ContentKind(part.Type), Text: part.Text}
		if part.ImageURL != nil {
			content[i].Kind = inference.ContentImage
			content[i].Image = &inference.Image{URL: part.ImageURL.URL, Detail: part.ImageURL.Detail}
		}
		if part.CacheControl != nil {
			content[i].CacheControl = part.CacheControl.Type
		}
	}
	return inference.Message{
		Role:       inference.Role(message.Role),
		Content:    content,
		Reasoning:  message.Reasoning,
		Name:       message.Name,
		ToolCalls:  toolCallsToInference(message.ToolCalls),
		ToolCallID: message.ToolCallID,
	}, nil
}

func toolCallsFromInference(calls []inference.ToolCall) []ToolCall {
	converted := make([]ToolCall, len(calls))
	for i, call := range calls {
		converted[i] = ToolCall{ID: call.ID, Type: toolTypeFunction, Function: FunctionCall{Name: call.Name, Arguments: call.Arguments}}
	}
	return converted
}

func toolCallsToInference(calls []ToolCall) []inference.ToolCall {
	converted := make([]inference.ToolCall, len(calls))
	for i, call := range calls {
		converted[i] = inference.ToolCall{ID: call.ID, Name: call.Function.Name, Arguments: call.Function.Arguments}
	}
	return converted
}

func toolChoiceFromInference(choice inference.ToolChoice) any {
	if choice.Mode == "" {
		return nil
	}
	if choice.Mode != inference.ToolChoiceNamed {
		return string(choice.Mode)
	}
	return map[string]any{wireTypeToken: toolTypeFunction, toolTypeFunction: map[string]string{"name": choice.Name}}
}

func toolChoiceToInference(value any) inference.ToolChoice {
	if value == nil {
		return inference.ToolChoice{}
	}
	if mode, ok := value.(string); ok {
		return inference.ToolChoice{Mode: inference.ToolChoiceMode(mode)}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return inference.ToolChoice{}
	}
	var named struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if json.Unmarshal(raw, &named) == nil && named.Function.Name != "" {
		return inference.ToolChoice{Mode: inference.ToolChoiceNamed, Name: named.Function.Name}
	}
	return inference.ToolChoice{}
}

func responseFormatFromInference(output inference.StructuredOutput) *ResponseFormat {
	if output.Format == "" {
		return nil
	}
	format := &ResponseFormat{Type: string(output.Format)}
	if output.Format == inference.OutputJSONSchema {
		format.JSONSchema = &ResponseJSONSchema{
			Name: output.Name, Description: output.Description,
			Schema: append(json.RawMessage(nil), output.Schema...), Strict: output.Strict,
		}
	}
	return format
}

func outputToInference(format *ResponseFormat) inference.StructuredOutput {
	if format == nil {
		return inference.StructuredOutput{}
	}
	output := inference.StructuredOutput{Format: inference.OutputFormat(format.Type)}
	if format.JSONSchema != nil {
		output.Name = format.JSONSchema.Name
		output.Description = format.JSONSchema.Description
		output.Schema = append(json.RawMessage(nil), format.JSONSchema.Schema...)
		output.Strict = format.JSONSchema.Strict
	}
	return output
}

func usageToInference(usage Usage) inference.Usage {
	reasoningTokens := 0
	if usage.CompletionTokensDetails != nil {
		reasoningTokens = usage.CompletionTokensDetails.ReasoningTokens
	}
	return inference.Usage{
		InputTokens: usage.PromptTokens, OutputTokens: usage.CompletionTokens,
		TotalTokens: usage.TotalTokens, ReasoningTokens: reasoningTokens,
	}
}

func usageFromInference(usage inference.Usage) Usage {
	converted := Usage{
		PromptTokens: usage.InputTokens, CompletionTokens: usage.OutputTokens, TotalTokens: usage.TotalTokens,
	}
	if usage.ReasoningTokens != 0 {
		converted.CompletionTokensDetails = &CompletionTokensDetails{ReasoningTokens: usage.ReasoningTokens}
	}
	return converted
}

func logProbsToInference(logProbs *LogProbs) []inference.LogProb {
	if logProbs == nil {
		return nil
	}
	converted := make([]inference.LogProb, len(logProbs.Content))
	for i, item := range logProbs.Content {
		converted[i] = inference.LogProb{Token: item.Token, Value: item.LogProb, Bytes: append([]int(nil), item.Bytes...)}
		converted[i].Top = make([]inference.TopLogProb, len(item.TopLogProbs))
		for j, top := range item.TopLogProbs {
			converted[i].Top[j] = inference.TopLogProb{Token: top.Token, Value: top.LogProb, Bytes: append([]int(nil), top.Bytes...)}
		}
	}
	return converted
}

func logProbsFromInference(logProbs []inference.LogProb) *LogProbs {
	if len(logProbs) == 0 {
		return nil
	}
	converted := &LogProbs{Content: make([]LogProbItem, len(logProbs))}
	for i, item := range logProbs {
		converted.Content[i] = LogProbItem{Token: item.Token, LogProb: item.Value, Bytes: append([]int(nil), item.Bytes...)}
		converted.Content[i].TopLogProbs = make([]TopLogProb, len(item.Top))
		for j, top := range item.Top {
			converted.Content[i].TopLogProbs[j] = TopLogProb{Token: top.Token, LogProb: top.Value, Bytes: append([]int(nil), top.Bytes...)}
		}
	}
	return converted
}

func cloneIntMap(source map[string]int) map[string]int {
	if source == nil {
		return nil
	}
	clone := make(map[string]int, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}
