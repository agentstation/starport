package cache

import (
	"errors"
	"fmt"
	"sort"
	"strings"

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
	choices := make(map[int]*streamChoice)
	for _, event := range events {
		applyEventIdentity(&response, event)
		if event.Usage != nil {
			response.Usage = *event.Usage
		}
		for _, delta := range event.Deltas {
			choice := choices[delta.Index]
			if choice == nil {
				choice = &streamChoice{Choice: inference.Choice{Index: delta.Index}, textIndex: -1}
				choices[delta.Index] = choice
			}
			if delta.Role != "" {
				choice.Message.Role = delta.Role
			}
			choice.appendText(delta.Text)
			choice.Message.Content = append(choice.Message.Content, delta.Media...)
			choice.reasoning.WriteString(delta.Reasoning)
			choice.mergeToolCalls(delta.ToolCalls)
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
		response.Choices = append(response.Choices, choices[index].complete())
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

// streamChoice owns incremental strings without copying earlier deltas.
type streamChoice struct {
	inference.Choice
	textIndex int
	text      strings.Builder
	reasoning strings.Builder
	arguments []*strings.Builder
}

func (c *streamChoice) complete() inference.Choice {
	if c.textIndex >= 0 {
		c.Message.Content[c.textIndex].Text = c.text.String()
	}
	c.Message.Reasoning = c.reasoning.String()
	for index, arguments := range c.arguments {
		c.Message.ToolCalls[index].Arguments = arguments.String()
	}
	return c.Choice
}

func (c *streamChoice) appendText(text string) {
	if text == "" {
		return
	}
	if c.textIndex < 0 {
		for index, part := range c.Message.Content {
			if part.Kind == inference.ContentText {
				c.textIndex = index
				c.text.WriteString(part.Text)
				break
			}
		}
		if c.textIndex < 0 {
			c.textIndex = len(c.Message.Content)
			c.Message.Content = append(c.Message.Content, inference.ContentPart{Kind: inference.ContentText})
		}
	}
	c.text.WriteString(text)
}

func (c *streamChoice) mergeToolCalls(updates []inference.ToolCall) {
	for _, update := range updates {
		matched := false
		for index := range c.Message.ToolCalls {
			current := &c.Message.ToolCalls[index]
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
			_, _ = c.arguments[index].WriteString(update.Arguments)
			matched = true
			break
		}
		if !matched {
			c.Message.ToolCalls = append(c.Message.ToolCalls, update)
			arguments := new(strings.Builder)
			arguments.WriteString(update.Arguments)
			c.arguments = append(c.arguments, arguments)
		}
	}
}
