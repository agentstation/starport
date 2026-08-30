package openai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/agentstation/starport/internal/inference"
)

// This file adapts the stateless subset of the OpenAI Responses API
// (POST /v1/responses) onto the canonical chat request and response.
// The gateway keeps no response store, so the codec refuses each
// stored-state feature with a named reason: previous_response_id,
// store=true, and built-in tools.

const (
	responsesObject = "response"

	responsesItemMessage            = "message"
	responsesItemFunctionCall       = "function_call"
	responsesItemFunctionCallOutput = "function_call_output"
	responsesPartOutputText         = "output_text"

	responsesStatusInProgress = "in_progress"
	responsesStatusCompleted  = "completed"
	responsesStatusIncomplete = "incomplete"

	// Payload keys the stream encoder writes on more than one event.
	responsesKeyResponse     = "response"
	responsesKeyItem         = "item"
	responsesKeyItemID       = "item_id"
	responsesKeyOutputIndex  = "output_index"
	responsesKeyContentIndex = "content_index"
	responsesKeyText         = "text"
)

// UnsupportedError names one Responses API field the gateway refuses,
// because honoring it needs stored state the gateway does not keep. The
// controller maps it to a 400 whose param is the refused field.
type UnsupportedError struct {
	Param   string
	Message string
}

func (e *UnsupportedError) Error() string { return e.Message }

// ResponsesRequest is the OpenAI Responses wire request. The stored-state
// fields previous_response_id and store are declared so a caller that sets
// one reads a refusal that names it, not an unknown-field error.
type ResponsesRequest struct {
	Model              string                    `json:"model"`
	Input              json.RawMessage           `json:"input"`
	Instructions       string                    `json:"instructions,omitempty"`
	Temperature        *float32                  `json:"temperature,omitempty"`
	TopP               *float32                  `json:"top_p,omitempty"`
	MaxOutputTokens    *int                      `json:"max_output_tokens,omitempty"`
	Stream             bool                      `json:"stream,omitempty"`
	User               string                    `json:"user,omitempty"`
	Tools              []ResponsesTool           `json:"tools,omitempty"`
	ToolChoice         json.RawMessage           `json:"tool_choice,omitempty"`
	ParallelToolCalls  *bool                     `json:"parallel_tool_calls,omitempty"`
	Text               *ResponsesTextConfig      `json:"text,omitempty"`
	Reasoning          *ResponsesReasoningConfig `json:"reasoning,omitempty"`
	PreviousResponseID *string                   `json:"previous_response_id,omitempty"`
	Store              *bool                     `json:"store,omitempty"`
}

// ResponsesTool is one Responses tool declaration. The shape is flat where
// the chat shape nests a function object.
type ResponsesTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Strict      *bool           `json:"strict,omitempty"`
}

// ResponsesTextConfig carries the structured-output selection.
type ResponsesTextConfig struct {
	Format *ResponsesTextFormat `json:"format,omitempty"`
}

// ResponsesTextFormat is the Responses spelling of response_format.
type ResponsesTextFormat struct {
	Type        string          `json:"type"`
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Schema      json.RawMessage `json:"schema,omitempty"`
	Strict      bool            `json:"strict,omitempty"`
}

// ResponsesReasoningConfig selects the reasoning depth.
type ResponsesReasoningConfig struct {
	Effort string `json:"effort,omitempty"`
}

// responsesInputItem is one entry of an input item array. One struct holds
// the message, function_call, and function_call_output shapes, and the type
// switch reads only the fields its arm owns.
type responsesInputItem struct {
	Type      string          `json:"type"`
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	ID        string          `json:"id"`
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Arguments string          `json:"arguments"`
	Output    json.RawMessage `json:"output"`
	Status    string          `json:"status"`
}

// responsesContentPart is one Responses input content part. The image URL
// is a plain string here, where the chat shape nests an object.
type responsesContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	ImageURL string `json:"image_url"`
	Detail   string `json:"detail"`
	FileID   string `json:"file_id"`
	FileData string `json:"file_data"`
	Filename string `json:"filename"`
}

// DecodeResponses decodes one strict Responses request into the canonical
// chat request. A stored-state field returns an UnsupportedError that names
// it.
func DecodeResponses(reader io.Reader) (inference.ChatRequest, error) {
	var wire ResponsesRequest
	if err := decodeStrict(reader, &wire); err != nil {
		return inference.ChatRequest{}, err
	}
	if err := refuseResponsesStoredState(wire); err != nil {
		return inference.ChatRequest{}, err
	}
	messages, err := decodeResponsesInput(wire.Input)
	if err != nil {
		return inference.ChatRequest{}, err
	}
	if wire.Instructions != "" {
		system := inference.Message{
			Role:    inference.RoleSystem,
			Content: []inference.ContentPart{{Kind: inference.ContentText, Text: wire.Instructions}},
		}
		messages = append([]inference.Message{system}, messages...)
	}
	tools, err := decodeResponsesTools(wire.Tools)
	if err != nil {
		return inference.ChatRequest{}, err
	}
	toolChoice, err := decodeResponsesToolChoice(wire.ToolChoice)
	if err != nil {
		return inference.ChatRequest{}, err
	}
	output, err := decodeResponsesTextFormat(wire.Text)
	if err != nil {
		return inference.ChatRequest{}, err
	}
	reasoning := inference.Reasoning{}
	if wire.Reasoning != nil {
		reasoning.Effort = inference.ReasoningEffort(wire.Reasoning.Effort)
	}
	return inference.ChatRequest{
		Model: wire.Model, Messages: messages,
		Sampling: inference.Sampling{
			Temperature: wire.Temperature, TopP: wire.TopP, MaxTokens: wire.MaxOutputTokens,
		},
		Tools: tools, ToolChoice: toolChoice, ParallelToolCalls: wire.ParallelToolCalls,
		Output: output, Reasoning: reasoning,
		Stream: wire.Stream, User: wire.User,
	}, nil
}

// refuseResponsesStoredState refuses the features that need a response
// store. Each refusal names the field, so the caller learns which one to
// drop rather than guessing at a generic message.
func refuseResponsesStoredState(wire ResponsesRequest) error {
	if wire.PreviousResponseID != nil {
		return &UnsupportedError{
			Param:   "previous_response_id",
			Message: "previous_response_id needs stored responses, and this gateway stores none",
		}
	}
	if wire.Store != nil && *wire.Store {
		return &UnsupportedError{
			Param:   "store",
			Message: "store=true asks the gateway to keep the response, and this gateway stores none",
		}
	}
	for index, tool := range wire.Tools {
		if tool.Type != functionToolType {
			return &UnsupportedError{
				Param: "tools",
				Message: fmt.Sprintf(
					"tools[%d].type %q is a built-in tool, and this gateway runs only function tools",
					index, tool.Type),
			}
		}
	}
	return nil
}

func decodeResponsesInput(raw json.RawMessage) ([]inference.Message, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, fmt.Errorf("input is required")
	}
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return nil, err
		}
		return []inference.Message{{
			Role:    inference.RoleUser,
			Content: []inference.ContentPart{{Kind: inference.ContentText, Text: text}},
		}}, nil
	}
	var items []responsesInputItem
	if err := json.Unmarshal(trimmed, &items); err != nil {
		return nil, fmt.Errorf("input must be text or an item array: %w", err)
	}
	messages := make([]inference.Message, 0, len(items))
	for index, item := range items {
		message, err := decodeResponsesInputItem(item, index)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, nil
}

func decodeResponsesInputItem(item responsesInputItem, index int) (inference.Message, error) {
	itemType := item.Type
	if itemType == "" {
		itemType = responsesItemMessage
	}
	switch itemType {
	case responsesItemMessage:
		role, err := decodeResponsesRole(item.Role, index)
		if err != nil {
			return inference.Message{}, err
		}
		content, err := decodeResponsesContent(item.Content, index)
		if err != nil {
			return inference.Message{}, err
		}
		return inference.Message{Role: role, Content: content}, nil
	case responsesItemFunctionCall:
		callID := item.CallID
		if callID == "" {
			callID = item.ID
		}
		if callID == "" || item.Name == "" {
			return inference.Message{}, fmt.Errorf("input[%d] needs call_id and name", index)
		}
		return inference.Message{
			Role:      inference.RoleAssistant,
			ToolCalls: []inference.ToolCall{{ID: callID, Name: item.Name, Arguments: item.Arguments}},
		}, nil
	case responsesItemFunctionCallOutput:
		if item.CallID == "" {
			return inference.Message{}, fmt.Errorf("input[%d].call_id is required", index)
		}
		content, err := decodeResponsesContent(item.Output, index)
		if err != nil {
			return inference.Message{}, err
		}
		return inference.Message{
			Role: inference.RoleTool, Content: content, ToolCallID: item.CallID,
		}, nil
	default:
		return inference.Message{}, fmt.Errorf("input[%d].type %q is not supported", index, item.Type)
	}
}

func decodeResponsesRole(role string, index int) (inference.Role, error) {
	switch role {
	case "user":
		return inference.RoleUser, nil
	case "assistant":
		return inference.RoleAssistant, nil
	case "system", "developer":
		return inference.RoleSystem, nil
	default:
		return "", fmt.Errorf("input[%d].role %q is not supported", index, role)
	}
}

func decodeResponsesContent(raw json.RawMessage, index int) ([]inference.ContentPart, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return nil, err
		}
		return []inference.ContentPart{{Kind: inference.ContentText, Text: text}}, nil
	}
	var parts []responsesContentPart
	if err := json.Unmarshal(trimmed, &parts); err != nil {
		return nil, fmt.Errorf("input[%d].content must be a string or content-part array: %w", index, err)
	}
	result := make([]inference.ContentPart, len(parts))
	for partIndex, part := range parts {
		switch part.Type {
		case "input_text", responsesPartOutputText:
			result[partIndex] = inference.ContentPart{Kind: inference.ContentText, Text: part.Text}
		case "input_image":
			if part.ImageURL == "" {
				return nil, fmt.Errorf("input[%d].content[%d].image_url is required", index, partIndex)
			}
			result[partIndex] = inference.ContentPart{
				Kind:  inference.ContentImage,
				Image: &inference.Image{URL: part.ImageURL, Detail: part.Detail},
			}
		case "input_file":
			document, err := decodeFilePart(&File{
				Filename: part.Filename, FileData: part.FileData, FileID: part.FileID,
			}, partIndex)
			if err != nil {
				return nil, fmt.Errorf("input[%d]: %w", index, err)
			}
			result[partIndex] = inference.ContentPart{Kind: inference.ContentDocument, Document: document}
		default:
			return nil, fmt.Errorf("input[%d].content[%d].type %q is not supported", index, partIndex, part.Type)
		}
	}
	return result, nil
}

func decodeResponsesTools(wire []ResponsesTool) ([]inference.Tool, error) {
	if len(wire) == 0 {
		return nil, nil
	}
	tools := make([]inference.Tool, len(wire))
	for index, tool := range wire {
		if tool.Name == "" {
			return nil, fmt.Errorf("tools[%d].name is required", index)
		}
		tools[index] = inference.Tool{
			Name: tool.Name, Description: tool.Description,
			Parameters: append(json.RawMessage(nil), tool.Parameters...),
		}
	}
	return tools, nil
}

// decodeResponsesToolChoice reads the Responses tool_choice, whose named
// arm is flat where the chat arm nests a function object.
func decodeResponsesToolChoice(raw json.RawMessage) (inference.ToolChoice, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return inference.ToolChoice{}, nil
	}
	var mode string
	if err := json.Unmarshal(raw, &mode); err == nil {
		switch inference.ToolChoiceMode(mode) {
		case inference.ToolChoiceAuto, inference.ToolChoiceNone, inference.ToolChoiceRequired:
			return inference.ToolChoice{Mode: inference.ToolChoiceMode(mode)}, nil
		default:
			return inference.ToolChoice{}, fmt.Errorf("tool_choice %q is not supported", mode)
		}
	}
	var named struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &named); err != nil || named.Type != functionToolType || named.Name == "" {
		return inference.ToolChoice{}, fmt.Errorf("tool_choice must name a function")
	}
	return inference.ToolChoice{Mode: inference.ToolChoiceNamed, Name: named.Name}, nil
}

func decodeResponsesTextFormat(wire *ResponsesTextConfig) (inference.StructuredOutput, error) {
	if wire == nil || wire.Format == nil || wire.Format.Type == "" || wire.Format.Type == "text" {
		return inference.StructuredOutput{Format: inference.OutputText}, nil
	}
	format := wire.Format
	switch format.Type {
	case "json_object":
		return inference.StructuredOutput{Format: inference.OutputJSONObject}, nil
	case "json_schema":
		if format.Name == "" || len(format.Schema) == 0 {
			return inference.StructuredOutput{}, fmt.Errorf("text.format json_schema requires name and schema")
		}
		return inference.StructuredOutput{
			Format: inference.OutputJSONSchema, Name: format.Name,
			Description: format.Description, Schema: format.Schema, Strict: format.Strict,
		}, nil
	default:
		return inference.StructuredOutput{}, fmt.Errorf("text.format.type %q is not supported", format.Type)
	}
}

// ResponsesResponse is the OpenAI Responses wire response. Error and
// incomplete_details serialize as explicit nulls because SDK readers
// expect the keys to exist.
type ResponsesResponse struct {
	ID                string                      `json:"id"`
	Object            string                      `json:"object"`
	CreatedAt         int64                       `json:"created_at"`
	Status            string                      `json:"status"`
	Error             *ResponsesError             `json:"error"`
	IncompleteDetails *ResponsesIncompleteDetails `json:"incomplete_details"`
	Model             string                      `json:"model"`
	Output            []ResponsesOutputItem       `json:"output"`
	ParallelToolCalls bool                        `json:"parallel_tool_calls"`
	ToolChoice        string                      `json:"tool_choice"`
	Tools             []ResponsesTool             `json:"tools"`
	Usage             *ResponsesUsage             `json:"usage,omitempty"`
}

// ResponsesError mirrors the response-level error object. The gateway
// reports request errors through the error envelope instead, so this
// field stays null and exists for shape fidelity.
type ResponsesError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ResponsesIncompleteDetails names why a response stopped early.
type ResponsesIncompleteDetails struct {
	Reason string `json:"reason"`
}

// ResponsesOutputItem is one output entry: an assistant message or a
// function call. Arguments is a pointer so a function call carries the
// key even while its value is still empty.
type ResponsesOutputItem struct {
	Type      string                `json:"type"`
	ID        string                `json:"id,omitempty"`
	Status    string                `json:"status,omitempty"`
	Role      string                `json:"role,omitempty"`
	Content   []ResponsesOutputPart `json:"content,omitempty"`
	CallID    string                `json:"call_id,omitempty"`
	Name      string                `json:"name,omitempty"`
	Arguments *string               `json:"arguments,omitempty"`
}

// ResponsesOutputPart is one message content part.
type ResponsesOutputPart struct {
	Type        string `json:"type"`
	Text        string `json:"text"`
	Annotations []any  `json:"annotations"`
}

// ResponsesUsage is the Responses spelling of token usage.
type ResponsesUsage struct {
	InputTokens         int                          `json:"input_tokens"`
	InputTokensDetails  ResponsesInputTokensDetails  `json:"input_tokens_details"`
	OutputTokens        int                          `json:"output_tokens"`
	OutputTokensDetails ResponsesOutputTokensDetails `json:"output_tokens_details"`
	TotalTokens         int                          `json:"total_tokens"`
}

// ResponsesInputTokensDetails breaks the input total down.
type ResponsesInputTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

// ResponsesOutputTokensDetails breaks the output total down.
type ResponsesOutputTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

// EncodeResponses encodes one canonical chat response as a Responses
// object. The first choice becomes the output: its text becomes one
// message item and each tool call becomes one function_call item.
func EncodeResponses(response inference.ChatResponse) ResponsesResponse {
	usage := encodeResponsesUsage(response.Usage)
	result := ResponsesResponse{
		ID: response.ID, Object: responsesObject, CreatedAt: response.CreatedUnix,
		Status: responsesStatusCompleted,
		Model:  responseModel(response.Model, response.ModelUsed),
		Output: []ResponsesOutputItem{}, ParallelToolCalls: true,
		ToolChoice: string(inference.ToolChoiceAuto), Tools: []ResponsesTool{},
		Usage: &usage,
	}
	if len(response.Choices) == 0 {
		return result
	}
	choice := response.Choices[0]
	text := messageText(choice.Message)
	if text != "" || len(choice.Message.ToolCalls) == 0 {
		result.Output = append(result.Output,
			responsesMessageItem(responsesMessageID(response.ID), text, responsesStatusCompleted))
	}
	for _, call := range choice.Message.ToolCalls {
		arguments := call.Arguments
		result.Output = append(result.Output, ResponsesOutputItem{
			Type: responsesItemFunctionCall, ID: responsesCallItemID(call.ID),
			Status: responsesStatusCompleted,
			CallID: call.ID, Name: call.Name, Arguments: &arguments,
		})
	}
	if choice.FinishReason == "length" {
		result.Status = responsesStatusIncomplete
		result.IncompleteDetails = &ResponsesIncompleteDetails{Reason: "max_output_tokens"}
	}
	return result
}

func encodeResponsesUsage(usage inference.Usage) ResponsesUsage {
	return ResponsesUsage{
		InputTokens:         usage.InputTokens,
		InputTokensDetails:  ResponsesInputTokensDetails{CachedTokens: usage.CacheReadTokens},
		OutputTokens:        usage.OutputTokens,
		OutputTokensDetails: ResponsesOutputTokensDetails{ReasoningTokens: usage.ReasoningTokens},
		TotalTokens:         usage.TotalTokens,
	}
}

func responsesMessageItem(id, text, status string) ResponsesOutputItem {
	return ResponsesOutputItem{
		Type: responsesItemMessage, ID: id, Status: status, Role: "assistant",
		Content: []ResponsesOutputPart{{
			Type: responsesPartOutputText, Text: text, Annotations: []any{},
		}},
	}
}

func responsesMessageID(responseID string) string { return "msg_" + responseID }
func responsesCallItemID(callID string) string    { return "fc_" + callID }

// ResponsesStreamEvent is one named server-sent event: the event name and
// its JSON payload. The Responses stream ends with response.completed and
// carries no [DONE] terminator.
type ResponsesStreamEvent struct {
	Type string
	Data json.RawMessage
}

// ResponsesStreamEncoder folds the canonical stream into the named
// Responses event sequence. It is stateful: it numbers every event,
// accumulates the output items, and closes them in order. Call Encode for
// each canonical event and Finish after the stream ends.
type ResponsesStreamEncoder struct {
	sequence int
	started  bool
	err      error

	id      string
	model   string
	created int64

	messageOpen bool
	text        strings.Builder

	callOpen bool
	callID   string
	callName string
	callArgs strings.Builder

	outputIndex int
	items       []ResponsesOutputItem
	usage       *ResponsesUsage
	finish      string
}

// Encode folds one canonical stream event into zero or more named events.
func (e *ResponsesStreamEncoder) Encode(event inference.StreamEvent) ([]ResponsesStreamEvent, error) {
	var out []ResponsesStreamEvent
	if !e.started {
		e.started = true
		e.id = event.ID
		e.model = responseModel(event.Model, event.ModelUsed)
		e.created = event.CreatedUnix
		snapshot := e.snapshot(responsesStatusInProgress)
		e.push(&out, "response.created", map[string]any{responsesKeyResponse: snapshot})
		e.push(&out, "response.in_progress", map[string]any{responsesKeyResponse: snapshot})
	}
	if event.Usage != nil {
		usage := encodeResponsesUsage(*event.Usage)
		e.usage = &usage
	}
	for _, delta := range event.Deltas {
		if delta.Index != 0 {
			continue
		}
		if delta.Text != "" {
			e.ensureMessageOpen(&out)
			e.push(&out, "response.output_text.delta", map[string]any{
				responsesKeyItemID: responsesMessageID(e.id), responsesKeyOutputIndex: e.outputIndex,
				responsesKeyContentIndex: 0, "delta": delta.Text, "logprobs": []any{},
			})
			e.text.WriteString(delta.Text)
		}
		for _, call := range delta.ToolCalls {
			e.encodeCallDelta(&out, call)
		}
		if delta.FinishReason != "" {
			e.finish = delta.FinishReason
		}
	}
	if event.Kind == inference.StreamEnd {
		e.closeOpenItem(&out)
	}
	return out, e.err
}

// Finish closes any open item and emits the terminal snapshot event.
func (e *ResponsesStreamEncoder) Finish() ([]ResponsesStreamEvent, error) {
	var out []ResponsesStreamEvent
	e.closeOpenItem(&out)
	status, eventType := responsesStatusCompleted, "response.completed"
	if e.finish == "length" {
		status, eventType = responsesStatusIncomplete, "response.incomplete"
	}
	e.push(&out, eventType, map[string]any{responsesKeyResponse: e.snapshot(status)})
	return out, e.err
}

func (e *ResponsesStreamEncoder) ensureMessageOpen(out *[]ResponsesStreamEvent) {
	if e.messageOpen {
		return
	}
	e.closeOpenItem(out)
	e.messageOpen = true
	e.push(out, "response.output_item.added", map[string]any{
		responsesKeyOutputIndex: e.outputIndex,
		responsesKeyItem:        responsesMessageItem(responsesMessageID(e.id), "", responsesStatusInProgress),
	})
	e.push(out, "response.content_part.added", map[string]any{
		responsesKeyItemID: responsesMessageID(e.id), responsesKeyOutputIndex: e.outputIndex, responsesKeyContentIndex: 0,
		"part": ResponsesOutputPart{Type: responsesPartOutputText, Text: "", Annotations: []any{}},
	})
}

func (e *ResponsesStreamEncoder) encodeCallDelta(out *[]ResponsesStreamEvent, call inference.ToolCall) {
	opensNewCall := call.ID != "" && (!e.callOpen || call.ID != e.callID)
	if opensNewCall {
		e.closeOpenItem(out)
		e.callOpen = true
		e.callID = call.ID
		e.callName = call.Name
		e.callArgs.Reset()
		arguments := ""
		e.push(out, "response.output_item.added", map[string]any{
			responsesKeyOutputIndex: e.outputIndex,
			responsesKeyItem: ResponsesOutputItem{
				Type: responsesItemFunctionCall, ID: responsesCallItemID(call.ID),
				Status: responsesStatusInProgress,
				CallID: call.ID, Name: call.Name, Arguments: &arguments,
			},
		})
	}
	if !e.callOpen {
		return
	}
	if !opensNewCall && call.Name != "" && e.callName == "" {
		e.callName = call.Name
	}
	if call.Arguments != "" {
		e.push(out, "response.function_call_arguments.delta", map[string]any{
			responsesKeyItemID: responsesCallItemID(e.callID), responsesKeyOutputIndex: e.outputIndex,
			"delta": call.Arguments,
		})
		e.callArgs.WriteString(call.Arguments)
	}
}

func (e *ResponsesStreamEncoder) closeOpenItem(out *[]ResponsesStreamEvent) {
	if e.messageOpen {
		text := e.text.String()
		e.push(out, "response.output_text.done", map[string]any{
			responsesKeyItemID: responsesMessageID(e.id), responsesKeyOutputIndex: e.outputIndex,
			responsesKeyContentIndex: 0, responsesKeyText: text, "logprobs": []any{},
		})
		e.push(out, "response.content_part.done", map[string]any{
			responsesKeyItemID: responsesMessageID(e.id), responsesKeyOutputIndex: e.outputIndex, responsesKeyContentIndex: 0,
			"part": ResponsesOutputPart{Type: responsesPartOutputText, Text: text, Annotations: []any{}},
		})
		item := responsesMessageItem(responsesMessageID(e.id), text, responsesStatusCompleted)
		e.push(out, "response.output_item.done", map[string]any{
			responsesKeyOutputIndex: e.outputIndex, responsesKeyItem: item,
		})
		e.items = append(e.items, item)
		e.outputIndex++
		e.messageOpen = false
		e.text.Reset()
		return
	}
	if e.callOpen {
		arguments := e.callArgs.String()
		e.push(out, "response.function_call_arguments.done", map[string]any{
			responsesKeyItemID: responsesCallItemID(e.callID), responsesKeyOutputIndex: e.outputIndex,
			"arguments": arguments,
		})
		item := ResponsesOutputItem{
			Type: responsesItemFunctionCall, ID: responsesCallItemID(e.callID),
			Status: responsesStatusCompleted,
			CallID: e.callID, Name: e.callName, Arguments: &arguments,
		}
		e.push(out, "response.output_item.done", map[string]any{
			responsesKeyOutputIndex: e.outputIndex, responsesKeyItem: item,
		})
		e.items = append(e.items, item)
		e.outputIndex++
		e.callOpen = false
		e.callID = ""
		e.callName = ""
		e.callArgs.Reset()
	}
}

func (e *ResponsesStreamEncoder) snapshot(status string) ResponsesResponse {
	response := ResponsesResponse{
		ID: e.id, Object: responsesObject, CreatedAt: e.created, Status: status,
		Model:  e.model,
		Output: append([]ResponsesOutputItem{}, e.items...), ParallelToolCalls: true,
		ToolChoice: string(inference.ToolChoiceAuto), Tools: []ResponsesTool{},
	}
	if status == responsesStatusIncomplete {
		response.IncompleteDetails = &ResponsesIncompleteDetails{Reason: "max_output_tokens"}
	}
	if status != responsesStatusInProgress {
		response.Usage = e.usage
	}
	return response
}

// push appends one named event. It numbers the event and records the
// first marshal failure, and it goes quiet after that failure so the
// caller reads one error for the whole stream.
func (e *ResponsesStreamEncoder) push(out *[]ResponsesStreamEvent, eventType string, fields map[string]any) {
	if e.err != nil {
		return
	}
	fields["type"] = eventType
	fields["sequence_number"] = e.sequence
	data, err := json.Marshal(fields)
	if err != nil {
		e.err = err
		return
	}
	e.sequence++
	*out = append(*out, ResponsesStreamEvent{Type: eventType, Data: data})
}
