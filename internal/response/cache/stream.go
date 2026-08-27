package cache

import (
	"errors"
	"fmt"
	"sort"

	"github.com/agentstation/starport/internal/inference"
)

var (
	// ErrNoStreamEvents reports an empty canonical event sequence.
	ErrNoStreamEvents = errors.New("stream has no canonical events")
	// ErrUnsupportedContent reports a content part that canonical replay
	// cannot represent. A part now replays whatever kind it names, so the
	// remaining case is a malformed part: one that claims to be text while
	// carrying a media payload, which no reader can resolve without
	// guessing which half to believe.
	ErrUnsupportedContent = errors.New("cached stream content part is malformed")
)

// StreamEvents reconstructs canonical stream events from a completed result.
func StreamEvents(response inference.ChatResponse, options inference.StreamOptions) ([]inference.StreamEvent, error) {
	start := baseEvent(response, inference.StreamStart)
	delta := baseEvent(response, inference.StreamDelta)
	end := baseEvent(response, inference.StreamEnd)
	for _, choice := range response.Choices {
		text, media, err := splitMessage(choice.Message)
		if err != nil {
			return nil, err
		}
		start.Deltas = append(start.Deltas, inference.ChoiceDelta{Index: choice.Index, Role: choice.Message.Role})
		delta.Deltas = append(delta.Deltas, inference.ChoiceDelta{
			Index: choice.Index, Text: text, Reasoning: choice.Message.Reasoning,
			Media:     media,
			ToolCalls: append([]inference.ToolCall(nil), choice.Message.ToolCalls...),
			LogProbs:  append([]inference.LogProb(nil), choice.LogProbs...),
		})
		end.Deltas = append(end.Deltas, inference.ChoiceDelta{Index: choice.Index, FinishReason: choice.FinishReason})
	}
	events := []inference.StreamEvent{start, delta, end}
	if options.IncludeUsage {
		usage := response.Usage
		usageEvent := baseEvent(response, inference.StreamUsage)
		usageEvent.Usage = &usage
		events = append(events, usageEvent)
	}
	return events, nil
}

// CompleteStream builds one canonical completed result from stream events.
func CompleteStream(events []inference.StreamEvent) (inference.ChatResponse, error) {
	if len(events) == 0 {
		return inference.ChatResponse{}, ErrNoStreamEvents
	}
	response := inference.ChatResponse{}
	choices := make(map[int]*inference.Choice)
	for _, event := range events {
		applyEventIdentity(&response, event)
		if event.Usage != nil {
			response.Usage = *event.Usage
		}
		for _, delta := range event.Deltas {
			choice := choices[delta.Index]
			if choice == nil {
				choice = &inference.Choice{Index: delta.Index}
				choices[delta.Index] = choice
			}
			if delta.Role != "" {
				choice.Message.Role = delta.Role
			}
			appendMessageText(&choice.Message, delta.Text)
			choice.Message.Content = append(choice.Message.Content, delta.Media...)
			choice.Message.Reasoning += delta.Reasoning
			mergeToolCalls(&choice.Message.ToolCalls, delta.ToolCalls)
			choice.LogProbs = append(choice.LogProbs, delta.LogProbs...)
			if delta.FinishReason != "" {
				choice.FinishReason = delta.FinishReason
			}
		}
	}
	indexes := make([]int, 0, len(choices))
	for index := range choices {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	response.Choices = make([]inference.Choice, 0, len(indexes))
	for _, index := range indexes {
		response.Choices = append(response.Choices, *choices[index])
	}
	return response.Clone(), nil
}

func baseEvent(response inference.ChatResponse, kind inference.StreamEventKind) inference.StreamEvent {
	return inference.StreamEvent{
		Kind: kind, ID: response.ID, CreatedUnix: response.CreatedUnix,
		Model: response.Model, ModelUsed: response.ModelUsed,
		SystemFingerprint: response.SystemFingerprint,
	}
}

// splitMessage separates a completed message into the text a delta
// accumulates and the media parts a delta carries whole. A generated image
// used to end the replay here with an error, so a caller that asked for a
// stream received the answer only while the cache missed.
func splitMessage(message inference.Message) (string, []inference.ContentPart, error) {
	var text string
	var media []inference.ContentPart
	for _, part := range message.Content {
		if part.Kind == inference.ContentText {
			if part.Image != nil || part.Audio != nil || part.Document != nil || part.Video != nil {
				return "", nil, fmt.Errorf("%w: a text part carries a %s payload", ErrUnsupportedContent, part.Kind)
			}
			text += part.Text
			continue
		}
		media = append(media, part)
	}
	return text, media, nil
}

func applyEventIdentity(response *inference.ChatResponse, event inference.StreamEvent) {
	if response.ID == "" {
		response.ID = event.ID
	}
	if response.CreatedUnix == 0 {
		response.CreatedUnix = event.CreatedUnix
	}
	if response.Model == "" {
		response.Model = event.Model
	}
	if response.ModelUsed == "" {
		response.ModelUsed = event.ModelUsed
	}
	if response.SystemFingerprint == "" {
		response.SystemFingerprint = event.SystemFingerprint
	}
}

// appendMessageText accumulates streamed text into the message's text part.
// It looks the part up rather than assuming index zero: a delta that carried a
// generated image before the first text delta arrived would otherwise append
// the answer's words into the image part.
func appendMessageText(message *inference.Message, text string) {
	if text == "" {
		return
	}
	for index := range message.Content {
		if message.Content[index].Kind == inference.ContentText {
			message.Content[index].Text += text
			return
		}
	}
	message.Content = append(message.Content, inference.ContentPart{
		Kind: inference.ContentText, Text: text,
	})
}

func mergeToolCalls(target *[]inference.ToolCall, updates []inference.ToolCall) {
	for _, update := range updates {
		matched := false
		for index := range *target {
			current := &(*target)[index]
			if update.ID != "" && current.ID != update.ID {
				continue
			}
			if update.ID == "" && update.Name != "" && current.Name != update.Name {
				continue
			}
			if current.ID == "" {
				current.ID = update.ID
			}
			if current.Name == "" {
				current.Name = update.Name
			}
			current.Arguments += update.Arguments
			matched = true
			break
		}
		if !matched {
			*target = append(*target, update)
		}
	}
}
