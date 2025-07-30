package connectors

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestVertexAI_MultiRegionConfiguration(t *testing.T) {
	tests := []struct {
		name              string
		config            ProviderConfig
		expectedLocation  string
		expectedFallbacks []string
		wantErr           bool
	}{
		{
			name: "default location with auto fallbacks",
			config: ProviderConfig{
				BaseURL: "",
				Timeout: 30 * time.Second,
				Extra: map[string]any{
					"project_id": "test-project",
				},
			},
			expectedLocation:  "us-central1",
			expectedFallbacks: []string{"us-east4", "us-west1", "us-west4"},
			wantErr:           false,
		},
		{
			name: "custom location with auto fallbacks",
			config: ProviderConfig{
				BaseURL: "",
				Timeout: 30 * time.Second,
				Extra: map[string]any{
					"project_id": "test-project",
					"location":   "europe-west1",
				},
			},
			expectedLocation:  "europe-west1",
			expectedFallbacks: []string{"europe-west4", "europe-west2", "europe-north1"},
			wantErr:           false,
		},
		{
			name: "custom location with custom fallbacks",
			config: ProviderConfig{
				BaseURL: "",
				Timeout: 30 * time.Second,
				Extra: map[string]any{
					"project_id": "test-project",
					"location":   "us-central1",
					"fallback_locations": []any{
						"asia-southeast1",
						"europe-west4",
					},
				},
			},
			expectedLocation:  "us-central1",
			expectedFallbacks: []string{"asia-southeast1", "europe-west4"},
			wantErr:           false,
		},
		{
			name: "missing project_id",
			config: ProviderConfig{
				BaseURL: "",
				Timeout: 30 * time.Second,
				Extra:   map[string]any{},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			connector, err := NewVertexAIConnector(tt.config)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.NotNil(t, connector)
			assert.Equal(t, tt.expectedLocation, connector.location)
			assert.Equal(t, tt.expectedFallbacks, connector.fallbackLocations)
			assert.Equal(t, GoogleVertexAIProvider, connector.Name())

			// Verify base URL is set correctly
			if tt.config.BaseURL == "" {
				projectID := tt.config.Extra["project_id"].(string)
				expectedURL := "https://" + tt.expectedLocation + "-aiplatform.googleapis.com/v1/projects/" + projectID + "/locations/" + tt.expectedLocation
				assert.Equal(t, expectedURL, connector.config.BaseURL)
			}
		})
	}
}

func TestVertexAI_DefaultFallbackLocations(t *testing.T) {
	tests := []struct {
		primaryLocation   string
		expectedFallbacks []string
	}{
		// US regions
		{"us-central1", []string{"us-east4", "us-west1", "us-west4"}},
		{"us-east4", []string{"us-central1", "us-west1", "us-west4"}},
		{"us-west1", []string{"us-west4", "us-central1", "us-east4"}},
		{"us-west4", []string{"us-west1", "us-central1", "us-east4"}},

		// Europe regions
		{"europe-west1", []string{"europe-west4", "europe-west2", "europe-north1"}},
		{"europe-west2", []string{"europe-west1", "europe-west4", "europe-north1"}},
		{"europe-west4", []string{"europe-west1", "europe-west2", "europe-north1"}},
		{"europe-north1", []string{"europe-west4", "europe-west1", "europe-west2"}},

		// Asia regions
		{"asia-southeast1", []string{"asia-northeast1", "asia-east1", "asia-south1"}},
		{"asia-northeast1", []string{"asia-southeast1", "asia-east1", "asia-south1"}},
		{"asia-east1", []string{"asia-southeast1", "asia-northeast1", "asia-south1"}},
		{"asia-south1", []string{"asia-southeast1", "asia-northeast1", "asia-east1"}},

		// Unknown region
		{"unknown-region", []string{"us-central1", "europe-west4", "asia-southeast1"}},
	}

	for _, tt := range tests {
		t.Run(tt.primaryLocation, func(t *testing.T) {
			fallbacks := getDefaultFallbackLocations(tt.primaryLocation)
			assert.Equal(t, tt.expectedFallbacks, fallbacks)
		})
	}
}

func TestVertexAI_IsRetryableError(t *testing.T) {
	connector := &VertexAIConnector{}

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name: "rate limit error",
			err: &APIError{
				StatusCode: 429,
			},
			expected: true,
		},
		{
			name: "server error",
			err: &APIError{
				StatusCode: 500,
			},
			expected: true,
		},
		{
			name: "bad gateway",
			err: &APIError{
				StatusCode: 502,
			},
			expected: true,
		},
		{
			name: "service unavailable",
			err: &APIError{
				StatusCode: 503,
			},
			expected: true,
		},
		{
			name: "gateway timeout",
			err: &APIError{
				StatusCode: 504,
			},
			expected: true,
		},
		{
			name: "authentication error",
			err: &APIError{
				StatusCode: 401,
			},
			expected: false,
		},
		{
			name: "forbidden error",
			err: &APIError{
				StatusCode: 403,
			},
			expected: false,
		},
		{
			name: "bad request",
			err: &APIError{
				StatusCode: 400,
			},
			expected: false,
		},
		{
			name:     "network error",
			err:      assert.AnError,
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := connector.isRetryableError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}
