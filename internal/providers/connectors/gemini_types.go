package connectors

import "encoding/json"

// geminiPart represents a part of content which can be text or thought
type geminiPart struct {
	Text    string `json:"text,omitempty"`
	Thought bool   `json:"thought,omitempty"`
}

// geminiContent represents the content structure with parts
type geminiContent struct {
	Parts []geminiPart `json:"parts"`
	Role  string       `json:"role"`
}

// geminiCandidate represents a candidate response
type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
	Index        int           `json:"index"`
}

// geminiUsageMetadata represents token usage information
type geminiUsageMetadata struct {
	zeroReported            bool
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	TotalTokenCount         int `json:"totalTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount,omitempty"`
	ThoughtsTokenCount      int `json:"thoughtsTokenCount,omitempty"`
}

// UnmarshalJSON preserves explicit zero usage without treating an empty object as a measurement.
func (m *geminiUsageMetadata) UnmarshalJSON(data []byte) error {
	var decoded struct {
		Prompt     *int `json:"promptTokenCount"`
		Total      *int `json:"totalTokenCount"`
		Candidates int  `json:"candidatesTokenCount"`
		Cached     int  `json:"cachedContentTokenCount"`
		Thoughts   int  `json:"thoughtsTokenCount"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*m = geminiUsageMetadata{
		CandidatesTokenCount: decoded.Candidates, CachedContentTokenCount: decoded.Cached,
		ThoughtsTokenCount: decoded.Thoughts,
		zeroReported:       decoded.Prompt != nil && decoded.Total != nil,
	}
	if decoded.Prompt != nil {
		m.PromptTokenCount = *decoded.Prompt
	}
	if decoded.Total != nil {
		m.TotalTokenCount = *decoded.Total
	}
	return nil
}

func (m *geminiUsageMetadata) reported() bool {
	return m != nil && (m.zeroReported || m.PromptTokenCount != 0 || m.CandidatesTokenCount != 0 || m.TotalTokenCount != 0 || m.ThoughtsTokenCount != 0 || m.CachedContentTokenCount != 0)
}

// geminiResponse is the shared response type for Google AI Studio and Vertex AI
type geminiResponse struct {
	Candidates    []geminiCandidate    `json:"candidates"`
	UsageMetadata *geminiUsageMetadata `json:"usageMetadata"`
	// Error is a provider rejection delivered inside the stream body. A
	// chunk that carries it is a failure, never an empty candidate list.
	Error *geminiError `json:"error,omitempty"`
}

// geminiError is the google.rpc.Status shape Gemini embeds in stream bodies.
type geminiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}
