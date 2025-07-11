package connectors

// geminiResponse is the shared response type for Google AI Studio and Vertex AI
type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []map[string]interface{} `json:"parts"`
			Role  string                   `json:"role"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
		Index        int    `json:"index"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		TotalTokenCount      int `json:"totalTokenCount"`
	} `json:"usageMetadata"`
}
