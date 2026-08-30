package openai

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/inference"
)

func TestTheResponsesCodecMapsTextInputOntoTheCanonicalChat(t *testing.T) {
	body := `{
		"model": "openai/gpt-5-mini",
		"input": "say hello",
		"instructions": "answer in one word",
		"temperature": 0.2,
		"top_p": 0.9,
		"max_output_tokens": 64,
		"user": "caller-1"
	}`
	decoded, err := DecodeResponses(strings.NewReader(body))
	require.NoError(t, err)

	require.Equal(t, "openai/gpt-5-mini", decoded.Model)
	require.Len(t, decoded.Messages, 2)
	require.Equal(t, inference.RoleSystem, decoded.Messages[0].Role)
	require.Equal(t, "answer in one word", decoded.Messages[0].Content[0].Text)
	require.Equal(t, inference.RoleUser, decoded.Messages[1].Role)
	require.Equal(t, "say hello", decoded.Messages[1].Content[0].Text)
	require.InDelta(t, 0.2, *decoded.Sampling.Temperature, 0.0001)
	require.InDelta(t, 0.9, *decoded.Sampling.TopP, 0.0001)
	require.Equal(t, 64, *decoded.Sampling.MaxTokens)
	require.Equal(t, "caller-1", decoded.User)
	require.False(t, decoded.Stream)
	require.Equal(t, inference.OutputText, decoded.Output.Format)
}

func TestTheResponsesCodecMapsInputItemsAndToolResults(t *testing.T) {
	body := `{
		"model": "openai/gpt-5-mini",
		"input": [
			{"role": "developer", "content": "be terse"},
			{"role": "user", "content": [
				{"type": "input_text", "text": "what is in the picture"},
				{"type": "input_image", "image_url": "https://example.test/cat.png", "detail": "low"}
			]},
			{"type": "function_call", "call_id": "call_1", "name": "lookup", "arguments": "{\"q\":1}"},
			{"type": "function_call_output", "call_id": "call_1", "output": "42"}
		],
		"tools": [{"type": "function", "name": "lookup", "description": "find a number",
			"parameters": {"type": "object"}}],
		"tool_choice": {"type": "function", "name": "lookup"}
	}`
	decoded, err := DecodeResponses(strings.NewReader(body))
	require.NoError(t, err)

	require.Len(t, decoded.Messages, 4)
	require.Equal(t, inference.RoleSystem, decoded.Messages[0].Role)
	require.Equal(t, inference.RoleUser, decoded.Messages[1].Role)
	require.Len(t, decoded.Messages[1].Content, 2)
	require.Equal(t, inference.ContentImage, decoded.Messages[1].Content[1].Kind)
	require.Equal(t, "https://example.test/cat.png", decoded.Messages[1].Content[1].Image.URL)
	require.Equal(t, "low", decoded.Messages[1].Content[1].Image.Detail)

	require.Equal(t, inference.RoleAssistant, decoded.Messages[2].Role)
	require.Equal(t, "call_1", decoded.Messages[2].ToolCalls[0].ID)
	require.Equal(t, "lookup", decoded.Messages[2].ToolCalls[0].Name)
	require.Equal(t, inference.RoleTool, decoded.Messages[3].Role)
	require.Equal(t, "call_1", decoded.Messages[3].ToolCallID)
	require.Equal(t, "42", decoded.Messages[3].Content[0].Text)

	require.Len(t, decoded.Tools, 1)
	require.Equal(t, "lookup", decoded.Tools[0].Name)
	require.Equal(t, inference.ToolChoiceNamed, decoded.ToolChoice.Mode)
	require.Equal(t, "lookup", decoded.ToolChoice.Name)
}

func TestTheResponsesCodecMapsTheStructuredOutputFormat(t *testing.T) {
	body := `{
		"model": "openai/gpt-5-mini",
		"input": "list three colors",
		"text": {"format": {"type": "json_schema", "name": "colors",
			"schema": {"type": "object"}, "strict": true}},
		"reasoning": {"effort": "low"}
	}`
	decoded, err := DecodeResponses(strings.NewReader(body))
	require.NoError(t, err)
	require.Equal(t, inference.OutputJSONSchema, decoded.Output.Format)
	require.Equal(t, "colors", decoded.Output.Name)
	require.True(t, decoded.Output.Strict)
	require.Equal(t, inference.ReasoningLow, decoded.Reasoning.Effort)
}

func TestTheResponsesCodecRefusesStoredStateByName(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		param string
	}{
		{
			name:  "previous_response_id",
			body:  `{"model": "m", "input": "hi", "previous_response_id": "resp_0"}`,
			param: "previous_response_id",
		},
		{
			name:  "store",
			body:  `{"model": "m", "input": "hi", "store": true}`,
			param: "store",
		},
		{
			name:  "built-in tool",
			body:  `{"model": "m", "input": "hi", "tools": [{"type": "web_search"}]}`,
			param: "tools",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := DecodeResponses(strings.NewReader(testCase.body))
			var unsupported *UnsupportedError
			require.ErrorAs(t, err, &unsupported)
			require.Equal(t, testCase.param, unsupported.Param)
			require.Contains(t, unsupported.Message, testCase.param)
		})
	}
}

func TestTheResponsesCodecAcceptsAnExplicitStoreFalse(t *testing.T) {
	body := `{"model": "m", "input": "hi", "store": false}`
	decoded, err := DecodeResponses(strings.NewReader(body))
	require.NoError(t, err)
	require.Equal(t, "hi", decoded.Messages[0].Content[0].Text)
}

func TestTheResponsesCodecReportsAMisspelledField(t *testing.T) {
	var wire ResponsesRequest
	require.NotNil(t, &wire)
	_, err := DecodeResponses(strings.NewReader(`{"model": "m", "imput": "hi"}`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "imput")
	var unsupported *UnsupportedError
	require.False(t, errors.As(err, &unsupported),
		"a misspelled field is a caller typo, not a stored-state refusal")
}

func TestTheResponsesCodecEncodesTheResponseObject(t *testing.T) {
	encoded := EncodeResponses(inference.ChatResponse{
		ID: "resp_1", CreatedUnix: 33, Model: "openai/gpt-5-mini",
		Choices: []inference.Choice{{
			Message: inference.Message{
				Role:    inference.RoleAssistant,
				Content: []inference.ContentPart{{Kind: inference.ContentText, Text: "Hello"}},
				ToolCalls: []inference.ToolCall{{
					ID: "call_1", Name: "lookup", Arguments: `{"q":1}`,
				}},
			},
			FinishReason: "tool_calls",
		}},
		Usage: inference.Usage{
			InputTokens: 3, OutputTokens: 4, TotalTokens: 7,
			ReasoningTokens: 2, CacheReadTokens: 1,
		},
	})
	data, err := json.Marshal(encoded)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"id": "resp_1",
		"object": "response",
		"created_at": 33,
		"status": "completed",
		"error": null,
		"incomplete_details": null,
		"model": "openai/gpt-5-mini",
		"output": [
			{"type": "message", "id": "msg_resp_1", "status": "completed", "role": "assistant",
				"content": [{"type": "output_text", "text": "Hello", "annotations": []}]},
			{"type": "function_call", "id": "fc_call_1", "status": "completed",
				"call_id": "call_1", "name": "lookup", "arguments": "{\"q\":1}"}
		],
		"parallel_tool_calls": true,
		"tool_choice": "auto",
		"tools": [],
		"usage": {
			"input_tokens": 3, "input_tokens_details": {"cached_tokens": 1},
			"output_tokens": 4, "output_tokens_details": {"reasoning_tokens": 2},
			"total_tokens": 7
		}
	}`, string(data))
}

func TestTheResponsesCodecEncodesTheLengthStopAsIncomplete(t *testing.T) {
	encoded := EncodeResponses(inference.ChatResponse{
		ID: "resp_2", CreatedUnix: 34, Model: "m",
		Choices: []inference.Choice{{
			Message: inference.Message{
				Role:    inference.RoleAssistant,
				Content: []inference.ContentPart{{Kind: inference.ContentText, Text: "truncat"}},
			},
			FinishReason: "length",
		}},
	})
	require.Equal(t, "incomplete", encoded.Status)
	require.NotNil(t, encoded.IncompleteDetails)
	require.Equal(t, "max_output_tokens", encoded.IncompleteDetails.Reason)
}

func TestTheResponsesStreamEncoderEmitsTheNamedSequence(t *testing.T) {
	encoder := &ResponsesStreamEncoder{}
	var events []ResponsesStreamEvent

	batch, err := encoder.Encode(inference.StreamEvent{
		Kind: inference.StreamStart, ID: "resp_3", CreatedUnix: 35, Model: "m",
		Deltas: []inference.ChoiceDelta{{Role: inference.RoleAssistant}},
	})
	require.NoError(t, err)
	events = append(events, batch...)

	for _, chunk := range []string{"Hel", "lo"} {
		batch, err = encoder.Encode(inference.StreamEvent{
			Kind: inference.StreamDelta, ID: "resp_3",
			Deltas: []inference.ChoiceDelta{{Text: chunk}},
		})
		require.NoError(t, err)
		events = append(events, batch...)
	}

	batch, err = encoder.Encode(inference.StreamEvent{
		Kind: inference.StreamUsage, ID: "resp_3",
		Usage: &inference.Usage{InputTokens: 3, OutputTokens: 4, TotalTokens: 7},
	})
	require.NoError(t, err)
	events = append(events, batch...)

	batch, err = encoder.Encode(inference.StreamEvent{
		Kind: inference.StreamEnd, ID: "resp_3",
		Deltas: []inference.ChoiceDelta{{FinishReason: "stop"}},
	})
	require.NoError(t, err)
	events = append(events, batch...)

	batch, err = encoder.Finish()
	require.NoError(t, err)
	events = append(events, batch...)

	var names []string
	for _, event := range events {
		names = append(names, event.Type)
	}
	require.Equal(t, []string{
		"response.created",
		"response.in_progress",
		"response.output_item.added",
		"response.content_part.added",
		"response.output_text.delta",
		"response.output_text.delta",
		"response.output_text.done",
		"response.content_part.done",
		"response.output_item.done",
		"response.completed",
	}, names)

	for index, event := range events {
		var payload struct {
			Type           string `json:"type"`
			SequenceNumber int    `json:"sequence_number"`
		}
		require.NoError(t, json.Unmarshal(event.Data, &payload))
		require.Equal(t, event.Type, payload.Type)
		require.Equal(t, index, payload.SequenceNumber)
	}

	var textDone struct {
		Text string `json:"text"`
	}
	require.NoError(t, json.Unmarshal(events[6].Data, &textDone))
	require.Equal(t, "Hello", textDone.Text)

	var completed struct {
		Response ResponsesResponse `json:"response"`
	}
	require.NoError(t, json.Unmarshal(events[9].Data, &completed))
	require.Equal(t, "completed", completed.Response.Status)
	require.Len(t, completed.Response.Output, 1)
	require.Equal(t, "Hello", completed.Response.Output[0].Content[0].Text)
	require.NotNil(t, completed.Response.Usage)
	require.Equal(t, 7, completed.Response.Usage.TotalTokens)
}

func TestTheResponsesStreamEncoderStreamsAFunctionCall(t *testing.T) {
	encoder := &ResponsesStreamEncoder{}
	var events []ResponsesStreamEvent

	feed := []inference.StreamEvent{
		{Kind: inference.StreamStart, ID: "resp_4", Model: "m",
			Deltas: []inference.ChoiceDelta{{Role: inference.RoleAssistant}}},
		{Kind: inference.StreamDelta, ID: "resp_4",
			Deltas: []inference.ChoiceDelta{{ToolCalls: []inference.ToolCall{{
				ID: "call_9", Name: "lookup", Arguments: `{"q":`,
			}}}}},
		{Kind: inference.StreamDelta, ID: "resp_4",
			Deltas: []inference.ChoiceDelta{{ToolCalls: []inference.ToolCall{{
				Arguments: `1}`,
			}}}}},
		{Kind: inference.StreamEnd, ID: "resp_4",
			Deltas: []inference.ChoiceDelta{{FinishReason: "tool_calls"}}},
	}
	for _, event := range feed {
		batch, err := encoder.Encode(event)
		require.NoError(t, err)
		events = append(events, batch...)
	}
	batch, err := encoder.Finish()
	require.NoError(t, err)
	events = append(events, batch...)

	var names []string
	for _, event := range events {
		names = append(names, event.Type)
	}
	require.Equal(t, []string{
		"response.created",
		"response.in_progress",
		"response.output_item.added",
		"response.function_call_arguments.delta",
		"response.function_call_arguments.delta",
		"response.function_call_arguments.done",
		"response.output_item.done",
		"response.completed",
	}, names)

	var argumentsDone struct {
		Arguments string `json:"arguments"`
	}
	require.NoError(t, json.Unmarshal(events[5].Data, &argumentsDone))
	require.Equal(t, `{"q":1}`, argumentsDone.Arguments)

	var completed struct {
		Response ResponsesResponse `json:"response"`
	}
	require.NoError(t, json.Unmarshal(events[7].Data, &completed))
	require.Len(t, completed.Response.Output, 1)
	require.Equal(t, "function_call", completed.Response.Output[0].Type)
	require.Equal(t, "call_9", completed.Response.Output[0].CallID)
	require.Equal(t, "lookup", completed.Response.Output[0].Name)
	require.Equal(t, `{"q":1}`, *completed.Response.Output[0].Arguments)
}
