// Package tokenize owns gateway-side token estimation. When a provider
// stream ends without reporting usage, the estimator counts prompt and
// completion tokens with the closest known tokenizer so accounting,
// budgets, and clients still receive token totals. Codecs initialize at
// composition time and are shared across requests; counting is read-only
// and safe for concurrent use.
package tokenize

import (
	"strings"

	"github.com/tiktoken-go/tokenizer"

	"github.com/agentstation/starport/internal/inference"
)

// Hint names the routed model so the estimator can pick the closest
// codec. Both fields are optional; an empty hint selects the default.
type Hint struct {
	// Tokenizer is the catalog tokenizer family, for example "gpt".
	Tokenizer string
	// Model is the exact provider model ID.
	Model string
}

// Chat framing overhead per the OpenAI chat token accounting guide:
// every message costs fixed framing tokens, a message name costs one
// more, and every reply is primed with one assistant preamble.
const (
	tokensPerMessage = 3
	tokensPerName    = 1
	tokensPerReply   = 3
	// imageTokens matches the base image cost OpenAI documents for
	// low-detail images; exact image accounting is provider-specific.
	imageTokens = 85
)

// Estimator counts tokens with shared pre-built codecs. Create one with
// NewEstimator at composition time; the zero value has no codecs and
// falls back to a bytes-per-token heuristic.
type Estimator struct {
	codecs map[tokenizer.Encoding]tokenizer.Codec
}

// NewEstimator builds the estimator and initializes every codec it can
// select, so request paths never pay first-use construction and never
// touch the codecs' lazy decode state.
func NewEstimator() *Estimator {
	estimator := &Estimator{codecs: make(map[tokenizer.Encoding]tokenizer.Codec, 2)}
	for _, encoding := range []tokenizer.Encoding{tokenizer.O200kBase, tokenizer.Cl100kBase} {
		if codec, err := tokenizer.Get(encoding); err == nil {
			estimator.codecs[encoding] = codec
		}
	}
	return estimator
}

// CountText estimates the token count of one text with the hinted codec.
func (e *Estimator) CountText(hint Hint, text string) int {
	if text == "" {
		return 0
	}
	if codec := e.codec(hint); codec != nil {
		if count, err := codec.Count(text); err == nil {
			return count
		}
	}
	return fallbackCount(text)
}

// CountMessages estimates the prompt token count of a chat request,
// including per-message framing overhead and the reply preamble.
func (e *Estimator) CountMessages(hint Hint, messages []inference.Message) int {
	if len(messages) == 0 {
		return 0
	}
	total := tokensPerReply
	for _, message := range messages {
		total += tokensPerMessage
		total += e.CountText(hint, string(message.Role))
		if message.Name != "" {
			total += tokensPerName + e.CountText(hint, message.Name)
		}
		total += e.CountText(hint, message.Reasoning)
		for _, part := range message.Content {
			total += e.CountText(hint, part.Text)
			if part.Image != nil && part.Image.URL != "" {
				total += imageTokens
			}
		}
	}
	return total
}

// codec selects the closest pre-built codec for the hint. Models outside
// the GPT family approximate with o200k_base, the broadest modern BPE;
// same-family counts are exact and cross-family counts stay close enough
// for accounting marked as estimated.
func (e *Estimator) codec(hint Hint) tokenizer.Codec {
	encoding := tokenizer.O200kBase
	if strings.EqualFold(hint.Tokenizer, "gpt") && legacyGPTModel(hint.Model) {
		encoding = tokenizer.Cl100kBase
	}
	return e.codecs[encoding]
}

// legacyGPTModel reports whether a GPT-family model predates o200k_base.
func legacyGPTModel(model string) bool {
	model = strings.ToLower(model)
	if strings.HasPrefix(model, "gpt-4o") || strings.HasPrefix(model, "gpt-4.1") || strings.HasPrefix(model, "gpt-5") {
		return false
	}
	return strings.HasPrefix(model, "gpt-4") || strings.HasPrefix(model, "gpt-3.5")
}

// fallbackCount approximates tokens when no codec is available. Four
// bytes per token is the routing estimator's long-standing heuristic.
func fallbackCount(text string) int {
	count := len(text) / 4
	if count == 0 {
		count = 1
	}
	return count
}
