package cache

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/agentstation/starport/internal/inference"
)

// The pinned key in media_test.go catches a field that reaches the key when it
// should not. This file catches the opposite defect, which is the expensive
// one: a field that never reaches the key lets two requests that ask for
// different answers share one cached answer, and the caller receives a reply to
// a question it did not ask.
//
// The key payload embeds inference.ChatRequest whole, so today every field
// arrives by default. That is a property of one line in ChatKey, not a promise.
// A later change that keys an explicit projection, or that normalizes a field
// away as this one normalizes Stream, would drop a field with nothing failing.

// keyedField names one request field and the change that must move the key.
// The change has to keep the request eligible: an ineligible request returns an
// error rather than a key, and an error proves nothing about the encoding.
type keyedField struct {
	field  string
	change func(*inference.ChatRequest)
}

// exemptFromKey names each field that deliberately does not reach the key, and
// why. A field is exempt only when caching it wrong is impossible, not when
// keying it is inconvenient.
var exemptFromKey = map[string]string{
	// Delivery format does not change the canonical completed result, so
	// ChatKey clears both before hashing.
	"Stream":        "delivery format, normalized away in ChatKey",
	"StreamOptions": "delivery format, normalized away in ChatKey",
	// Provider extension semantics are unknown to the gateway, so a request
	// carrying them is refused before a key exists.
	"Extensions": "makes the request ineligible, so it never reaches a key",
}

func keyedFields() []keyedField {
	return []keyedField{
		{"Model", func(r *inference.ChatRequest) { r.Model = "openai/gpt-4o" }},
		{"FallbackModels", func(r *inference.ChatRequest) {
			r.FallbackModels = []string{"anthropic/claude-sonnet-4"}
		}},
		{"Messages", func(r *inference.ChatRequest) {
			r.Messages[0].Content[0].Text = "a different question"
		}},
		{"Sampling", func(r *inference.ChatRequest) {
			temperature := float32(0.25)
			r.Sampling.Temperature = &temperature
		}},
		{"Tools", func(r *inference.ChatRequest) {
			r.Tools = []inference.Tool{{
				Name:       "lookup",
				Parameters: json.RawMessage(`{"type":"object"}`),
			}}
		}},
		{"ToolChoice", func(r *inference.ChatRequest) {
			r.ToolChoice = inference.ToolChoice{Mode: inference.ToolChoiceRequired}
		}},
		{"ParallelToolCalls", func(r *inference.ChatRequest) {
			parallel := true
			r.ParallelToolCalls = &parallel
		}},
		{"Output", func(r *inference.ChatRequest) {
			r.Output = inference.StructuredOutput{Format: inference.OutputJSONObject}
		}},
		{"Reasoning", func(r *inference.ChatRequest) {
			r.Reasoning = inference.Reasoning{Effort: inference.ReasoningHigh}
		}},
		{"OutputModalities", func(r *inference.ChatRequest) {
			r.OutputModalities = []inference.Modality{inference.ModalityText, inference.ModalityAudio}
		}},
		{"AudioOutput", func(r *inference.ChatRequest) {
			r.AudioOutput = &inference.AudioOutput{Voice: "alloy", Format: "wav"}
		}},
		{"User", func(r *inference.ChatRequest) { r.User = "caller-two" }},
	}
}

// baselineIdentity is one eligible text request that every case starts from.
func baselineIdentity() ChatIdentity {
	return ChatIdentity{
		TenantID:          "tenant",
		CatalogGeneration: "generation",
		Request: inference.ChatRequest{
			Model: "openai/gpt-4.1",
			Messages: []inference.Message{{
				Role:    inference.RoleUser,
				Content: []inference.ContentPart{{Kind: inference.ContentText, Text: "hello"}},
			}},
		},
	}
}

// TestEveryRequestFieldIsKeyedOrExempt walks ChatRequest by reflection, so a
// field added tomorrow fails here until someone states which side it is on.
func TestEveryRequestFieldIsKeyedOrExempt(t *testing.T) {
	covered := make(map[string]bool, len(keyedFields()))
	for _, keyed := range keyedFields() {
		covered[keyed.field] = true
	}

	requestType := reflect.TypeOf(inference.ChatRequest{})
	for i := range requestType.NumField() {
		name := requestType.Field(i).Name
		switch {
		case covered[name] && exemptFromKey[name] != "":
			t.Errorf("%s is both keyed and exempt; the field is one or the other", name)
		case covered[name] || exemptFromKey[name] != "":
		default:
			t.Errorf("ChatRequest.%s reaches neither keyedFields nor exemptFromKey; "+
				"add a case that proves it changes the key, or state why caching across it is safe", name)
		}
	}
}

// TestChangingAKeyedFieldChangesTheKey proves each listed field is in the key
// rather than merely declared to be.
func TestChangingAKeyedFieldChangesTheKey(t *testing.T) {
	baseline, err := ChatKey(baselineIdentity())
	if err != nil {
		t.Fatal(err)
	}

	for _, keyed := range keyedFields() {
		t.Run(keyed.field, func(t *testing.T) {
			identity := baselineIdentity()
			keyed.change(&identity.Request)
			key, err := ChatKey(identity)
			if err != nil {
				t.Fatalf("the change made the request ineligible: %v", err)
			}
			if key == baseline {
				t.Fatalf("%s does not reach the key, so two requests that want "+
					"different answers share one cache entry", keyed.field)
			}
		})
	}
}

// TestExemptFieldsDoNotChangeTheKey holds the other half of the table. A field
// listed as exempt has to behave that way, or the reason recorded beside it is
// documentation of something that is not true.
func TestExemptFieldsDoNotChangeTheKey(t *testing.T) {
	baseline, err := ChatKey(baselineIdentity())
	if err != nil {
		t.Fatal(err)
	}

	identity := baselineIdentity()
	identity.Request.Stream = true
	identity.Request.StreamOptions = inference.StreamOptions{IncludeUsage: true}
	key, err := ChatKey(identity)
	if err != nil {
		t.Fatal(err)
	}
	if key != baseline {
		t.Fatal("stream delivery changed the key, so a streamed caller cannot read what a buffered caller stored")
	}

	identity = baselineIdentity()
	identity.Request.Extensions = map[string]json.RawMessage{"vendor": json.RawMessage(`{}`)}
	if _, err := ChatKey(identity); err == nil {
		t.Fatal("a request carrying provider extensions produced a key; the exemption claims it cannot")
	}
}

// TestAskingForTextAloneKeysAsAPlainTextRequest holds the normalization. An
// OpenAI client sends modalities: ["text"] on every ordinary turn, so without
// this the field would split the cache in two the day the codec starts reading
// it, and half the deployment would never read the other half's entries.
func TestAskingForTextAloneKeysAsAPlainTextRequest(t *testing.T) {
	baseline, err := ChatKey(baselineIdentity())
	if err != nil {
		t.Fatal(err)
	}

	identity := baselineIdentity()
	identity.Request.OutputModalities = []inference.Modality{inference.ModalityText}
	key, err := ChatKey(identity)
	if err != nil {
		t.Fatal(err)
	}
	if key != baseline {
		t.Fatal("asking for text alone produced a second key for the request that asks for nothing")
	}
}

// TestModalityOrderDoesNotChangeTheKey holds the other half of the
// normalization. The field names a set, so a caller that lists audio first
// asks the same question as one that lists text first.
func TestModalityOrderDoesNotChangeTheKey(t *testing.T) {
	first := baselineIdentity()
	first.Request.OutputModalities = []inference.Modality{inference.ModalityText, inference.ModalityAudio}
	firstKey, err := ChatKey(first)
	if err != nil {
		t.Fatal(err)
	}

	second := baselineIdentity()
	second.Request.OutputModalities = []inference.Modality{inference.ModalityAudio, inference.ModalityText}
	secondKey, err := ChatKey(second)
	if err != nil {
		t.Fatal(err)
	}

	if firstKey != secondKey {
		t.Fatal("two spellings of one modality set produced two keys")
	}
}
