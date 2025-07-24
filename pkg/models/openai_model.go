package models

// OpenAIModel represents the minimal OpenAI /v1/models response format
type OpenAIModel struct {
	ID      string `json:"id"`       // Model identifier
	Object  string `json:"object"`   // Always "model"
	Created int64  `json:"created"`  // Unix timestamp
	OwnedBy string `json:"owned_by"` // Organization that owns the model
}

// OpenAIModelsResponse represents the response from OpenAI's /v1/models endpoint
type OpenAIModelsResponse struct {
	Object string        `json:"object"` // Always "list"
	Data   []OpenAIModel `json:"data"`   // Array of models
}