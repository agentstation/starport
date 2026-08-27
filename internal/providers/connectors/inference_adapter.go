package connectors

import (
	"encoding/base64"
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
		Model:             request.Model,
		Messages:          messages,
		Temperature:       request.Sampling.Temperature,
		TopP:              request.Sampling.TopP,
		N:                 request.Sampling.CandidateCount,
		MaxTokens:         request.Sampling.MaxTokens,
		Stream:            request.Stream,
		Stop:              append([]string(nil), request.Sampling.Stop...),
		PresencePenalty:   request.Sampling.PresencePenalty,
		FrequencyPenalty:  request.Sampling.FrequencyPenalty,
		LogitBias:         cloneIntMap(request.Sampling.LogitBias),
		User:              request.User,
		Seed:              request.Sampling.Seed,
		Tools:             tools,
		ToolChoice:        toolChoiceFromInference(request.ToolChoice),
		ParallelToolCalls: request.ParallelToolCalls,
		ResponseFormat:    responseFormatFromInference(request.Output),
		Models:            append([]string(nil), request.FallbackModels...),
		ProviderOptions:   providerOptions,
		Modalities:        modalitiesFromInference(request.OutputModalities),
		Audio:             audioConfigFromInference(request.AudioOutput),
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
		ParallelToolCalls: request.ParallelToolCalls,
		Output:            outputToInference(request.ResponseFormat), Stream: request.Stream, User: request.User,
		OutputModalities: modalitiesToInference(request.Modalities),
		AudioOutput:      audioConfigToInference(request.Audio),
		Extensions:       extensions,
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
	// A provider reports no token count for a picture it generated, so the
	// answer itself is the only record of how many it produced. Counting them
	// here is what lets a cost and a spend budget see a media turn at all.
	usage := usageToInference(response.Usage)
	usage.GeneratedImages = inference.ResponseMediaUnits(choices).Images
	return inference.ChatResponse{
		ID:                response.ID,
		CreatedUnix:       response.Created,
		Model:             response.Model,
		ModelUsed:         modelUsed,
		Choices:           choices,
		Usage:             usage,
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
			media, err := appendGeneratedMedia(nil, choice.Delta.Images, nil)
			if err != nil {
				return nil, fmt.Errorf("choices[%d]: %w", i, err)
			}
			audio, err := audioChunkToInference(choice.Delta.Audio)
			if err != nil {
				return nil, fmt.Errorf("choices[%d]: %w", i, err)
			}
			deltas[i] = inference.ChoiceDelta{
				Index:        choice.Index,
				Role:         inference.Role(choice.Delta.Role),
				Text:         choice.Delta.Content,
				Reasoning:    choice.Delta.Reasoning,
				Audio:        audio,
				Media:        media,
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

// audioChunkToInference reads one streamed piece of a spoken answer. A chunk
// carries either bytes or transcript, so neither absence ends the chunk.
func audioChunkToInference(audio *GeneratedAudio) (*inference.AudioChunk, error) {
	if audio == nil {
		return nil, nil
	}
	chunk := inference.AudioChunk{Transcript: audio.Transcript}
	if audio.Data != "" {
		data, err := base64.StdEncoding.DecodeString(audio.Data)
		if err != nil {
			return nil, fmt.Errorf("decode streamed audio: %w", err)
		}
		chunk.Data = data
	}
	return &chunk, nil
}

func streamEventKind(deltas []inference.ChoiceDelta) inference.StreamEventKind {
	allStart := len(deltas) > 0
	allEnd := len(deltas) > 0
	for _, delta := range deltas {
		empty := delta.Text == "" && delta.Reasoning == "" && len(delta.ToolCalls) == 0 &&
			delta.Audio == nil && len(delta.Media) == 0
		allStart = allStart && delta.Role != "" && empty && delta.FinishReason == ""
		allEnd = allEnd && delta.FinishReason != "" && delta.Role == "" && empty
	}
	if allStart {
		return inference.StreamStart
	}
	if allEnd {
		return inference.StreamEnd
	}
	return inference.StreamDelta
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

func messagesFromInference(messages []inference.Message) ([]Message, error) {
	converted := make([]Message, len(messages))
	for i, message := range messages {
		content := make([]ContentPart, len(message.Content))
		for j, part := range message.Content {
			wire, err := contentFromInference(part)
			if err != nil {
				return nil, fmt.Errorf("messages[%d].content[%d]: %w", i, j, err)
			}
			content[j] = wire
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
		canonical, err := contentToInference(part)
		if err != nil {
			return inference.Message{}, fmt.Errorf("content[%d]: %w", i, err)
		}
		content[i] = canonical
	}
	content, err = appendGeneratedMedia(content, message.Images, message.Audio)
	if err != nil {
		return inference.Message{}, err
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

// appendGeneratedMedia folds the media a provider returns beside the content
// into the canonical part list. A generated image and a spoken answer arrive in
// their own wire fields, so a reader that only walked the content would drop
// the answer and report an empty turn.
func appendGeneratedMedia(content []inference.ContentPart, images []GeneratedImage, audio *GeneratedAudio) ([]inference.ContentPart, error) {
	for _, image := range images {
		if image.ImageURL == nil || image.ImageURL.URL == "" {
			continue
		}
		content = append(content, inference.ContentPart{
			Kind:  inference.ContentImage,
			Image: &inference.Image{URL: image.ImageURL.URL, Detail: image.ImageURL.Detail},
		})
	}
	if audio == nil {
		return content, nil
	}
	// The transcript is the answer in words, so it belongs with the text the
	// message already holds rather than in a field of its own.
	if audio.Transcript != "" {
		content = appendTranscript(content, audio.Transcript)
	}
	if audio.Data == "" {
		return content, nil
	}
	data, err := base64.StdEncoding.DecodeString(audio.Data)
	if err != nil {
		return nil, fmt.Errorf("decode generated audio: %w", err)
	}
	return append(content, inference.ContentPart{
		Kind: inference.ContentAudio, Audio: &inference.Audio{Data: data, Format: audio.Format},
	}), nil
}

func appendTranscript(content []inference.ContentPart, transcript string) []inference.ContentPart {
	for index := range content {
		if content[index].Kind == inference.ContentText {
			if content[index].Text == "" {
				content[index].Text = transcript
			}
			return content
		}
	}
	return append(content, inference.ContentPart{Kind: inference.ContentText, Text: transcript})
}

func modalitiesFromInference(modalities []inference.Modality) []string {
	if len(modalities) == 0 {
		return nil
	}
	names := make([]string, len(modalities))
	for index, modality := range modalities {
		names[index] = string(modality)
	}
	return names
}

func modalitiesToInference(names []string) []inference.Modality {
	if len(names) == 0 {
		return nil
	}
	modalities := make([]inference.Modality, len(names))
	for index, name := range names {
		modalities[index] = inference.Modality(name)
	}
	return modalities
}

func audioConfigFromInference(audio *inference.AudioOutput) *AudioConfig {
	if audio == nil {
		return nil
	}
	return &AudioConfig{Voice: audio.Voice, Format: audio.Format}
}

func audioConfigToInference(audio *AudioConfig) *inference.AudioOutput {
	if audio == nil {
		return nil
	}
	return &inference.AudioOutput{Voice: audio.Voice, Format: audio.Format}
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
	reasoningTokens, audioOutputTokens := 0, 0
	if usage.CompletionTokensDetails != nil {
		reasoningTokens = usage.CompletionTokensDetails.ReasoningTokens
		audioOutputTokens = usage.CompletionTokensDetails.AudioTokens
	}
	cacheReadTokens, audioInputTokens := 0, 0
	if usage.PromptTokensDetails != nil {
		cacheReadTokens = usage.PromptTokensDetails.CachedTokens
		audioInputTokens = usage.PromptTokensDetails.AudioTokens
	}
	return inference.Usage{
		InputTokens: usage.PromptTokens, OutputTokens: usage.CompletionTokens,
		TotalTokens: usage.TotalTokens, ReasoningTokens: reasoningTokens,
		CacheReadTokens: cacheReadTokens, CacheWriteTokens: usage.CacheWriteTokens,
		AudioInputTokens: audioInputTokens, AudioOutputTokens: audioOutputTokens,
	}
}

func usageFromInference(usage inference.Usage) Usage {
	converted := Usage{
		PromptTokens: usage.InputTokens, CompletionTokens: usage.OutputTokens, TotalTokens: usage.TotalTokens,
		CacheWriteTokens: usage.CacheWriteTokens,
	}
	if usage.ReasoningTokens != 0 || usage.AudioOutputTokens != 0 {
		converted.CompletionTokensDetails = &CompletionTokensDetails{
			ReasoningTokens: usage.ReasoningTokens,
			AudioTokens:     usage.AudioOutputTokens,
		}
	}
	if usage.CacheReadTokens != 0 || usage.AudioInputTokens != 0 {
		converted.PromptTokensDetails = &PromptTokensDetails{
			CachedTokens: usage.CacheReadTokens,
			AudioTokens:  usage.AudioInputTokens,
		}
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
