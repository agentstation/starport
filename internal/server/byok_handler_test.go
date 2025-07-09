package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentstation/starport/internal/byok"
	"github.com/agentstation/starport/internal/storage"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockBYOKManager is a mock implementation of byok.Manager
type MockBYOKManager struct {
	mock.Mock
}

func (m *MockBYOKManager) AddCredential(ctx context.Context, apiKeyID, provider string, cred map[string]string, config map[string]interface{}) error {
	args := m.Called(ctx, apiKeyID, provider, cred, config)
	return args.Error(0)
}

func (m *MockBYOKManager) GetCredential(ctx context.Context, apiKeyID, provider string) (*byok.Credential, error) {
	args := m.Called(ctx, apiKeyID, provider)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*byok.Credential), args.Error(1)
}

func (m *MockBYOKManager) GetCredentials(ctx context.Context, apiKeyID, provider string) ([]*byok.Credential, error) {
	args := m.Called(ctx, apiKeyID, provider)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*byok.Credential), args.Error(1)
}

func (m *MockBYOKManager) ListCredentials(ctx context.Context, apiKeyID string) ([]*byok.Credential, error) {
	args := m.Called(ctx, apiKeyID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*byok.Credential), args.Error(1)
}

func (m *MockBYOKManager) UpdateCredential(ctx context.Context, apiKeyID, provider string, cred map[string]string, config map[string]interface{}) error {
	args := m.Called(ctx, apiKeyID, provider, cred, config)
	return args.Error(0)
}

func (m *MockBYOKManager) DeleteCredential(ctx context.Context, apiKeyID, provider string) error {
	args := m.Called(ctx, apiKeyID, provider)
	return args.Error(0)
}

func (m *MockBYOKManager) ValidateCredential(ctx context.Context, provider string, cred map[string]string, config map[string]interface{}) error {
	args := m.Called(ctx, provider, cred, config)
	return args.Error(0)
}

func (m *MockBYOKManager) SetDefaultKey(ctx context.Context, provider string, cred map[string]string, config map[string]interface{}) error {
	args := m.Called(ctx, provider, cred, config)
	return args.Error(0)
}

func (m *MockBYOKManager) GetDefaultKey(ctx context.Context, provider string) (*byok.Credential, error) {
	args := m.Called(ctx, provider)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*byok.Credential), args.Error(1)
}

func (m *MockBYOKManager) DeleteDefaultKey(ctx context.Context, provider string) error {
	args := m.Called(ctx, provider)
	return args.Error(0)
}

func (m *MockBYOKManager) ListDefaultKeys(ctx context.Context) ([]*byok.Credential, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*byok.Credential), args.Error(1)
}

func (m *MockBYOKManager) DetermineKeyStrategy(ctx context.Context, apiKeyID string, provider string) byok.FallbackStrategy {
	args := m.Called(ctx, apiKeyID, provider)
	return args.Get(0).(byok.FallbackStrategy)
}

func (m *MockBYOKManager) CalculateBYOKCost(usage *byok.Usage) float64 {
	args := m.Called(usage)
	return args.Get(0).(float64)
}

func (m *MockBYOKManager) RecordUsage(ctx context.Context, apiKeyID string, provider string, usage *byok.Usage) error {
	args := m.Called(ctx, apiKeyID, provider, usage)
	return args.Error(0)
}

func (m *MockBYOKManager) RotateEncryptionKey(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func TestBYOKHandler_ListCredentials(t *testing.T) {
	mockManager := new(MockBYOKManager)
	handler := NewBYOKHandler(mockManager)

	router := chi.NewRouter()
	router.Get("/api/v1/keys/{key_id}/credentials", handler.ListCredentials)

	testTime := time.Now()
	credentials := []*byok.Credential{
		{
			Provider:   "openai",
			Config:     map[string]interface{}{"model": "gpt-4"},
			IsFallback: true,
			Priority:   0,
			CreatedAt:  testTime,
			UsageCount: 10,
		},
		{
			Provider:   "anthropic",
			IsFallback: false,
			Priority:   1,
			CreatedAt:  testTime,
			LastUsed:   &testTime,
			UsageCount: 5,
		},
	}

	mockManager.On("ListCredentials", mock.Anything, "test-key").Return(credentials, nil)

	req := httptest.NewRequest("GET", "/api/v1/keys/test-key/credentials", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var response map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	creds := response["credentials"].([]interface{})
	assert.Len(t, creds, 2)

	// Verify first credential
	cred0 := creds[0].(map[string]interface{})
	assert.Equal(t, "openai", cred0["provider"])
	assert.Equal(t, true, cred0["is_fallback"])
	assert.Equal(t, float64(0), cred0["priority"])
	assert.Equal(t, float64(10), cred0["usage_count"])
	assert.Nil(t, cred0["last_used"])

	// Verify second credential
	cred1 := creds[1].(map[string]interface{})
	assert.Equal(t, "anthropic", cred1["provider"])
	assert.Equal(t, false, cred1["is_fallback"])
	assert.Equal(t, float64(1), cred1["priority"])
	assert.Equal(t, float64(5), cred1["usage_count"])
	assert.NotNil(t, cred1["last_used"])

	mockManager.AssertExpectations(t)
}

func TestBYOKHandler_AddCredential(t *testing.T) {
	tests := []struct {
		name           string
		body           interface{}
		mockSetup      func(*MockBYOKManager)
		expectedStatus int
		expectedError  string
	}{
		{
			name: "Valid credential",
			body: map[string]interface{}{
				"provider":   "openai",
				"credential": map[string]string{"api_key": "sk-test123"},
				"config":     map[string]interface{}{"model": "gpt-4"},
			},
			mockSetup: func(m *MockBYOKManager) {
				m.On("AddCredential", mock.Anything, "test-key", "openai", 
					map[string]string{"api_key": "sk-test123"},
					map[string]interface{}{"model": "gpt-4"}).Return(nil)
			},
			expectedStatus: http.StatusCreated,
		},
		{
			name: "Missing provider",
			body: map[string]interface{}{
				"credential": map[string]string{"api_key": "sk-test123"},
			},
			mockSetup:      func(m *MockBYOKManager) {},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Provider is required",
		},
		{
			name: "Missing credential",
			body: map[string]interface{}{
				"provider": "openai",
			},
			mockSetup:      func(m *MockBYOKManager) {},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Credential is required",
		},
		{
			name: "Validation error",
			body: map[string]interface{}{
				"provider":   "openai",
				"credential": map[string]string{"api_key": "invalid"},
			},
			mockSetup: func(m *MockBYOKManager) {
				m.On("AddCredential", mock.Anything, "test-key", "openai",
					map[string]string{"api_key": "invalid"},
					mock.Anything).Return(&byok.ValidationError{
					Provider: "openai",
					Field:    "api_key",
					Message:  "invalid format",
				})
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "validation failed for openai api_key: invalid format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockManager := new(MockBYOKManager)
			handler := NewBYOKHandler(mockManager)

			router := chi.NewRouter()
			router.Post("/api/v1/keys/{key_id}/credentials", handler.AddCredential)

			tt.mockSetup(mockManager)

			body, _ := json.Marshal(tt.body)
			req := httptest.NewRequest("POST", "/api/v1/keys/test-key/credentials", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			router.ServeHTTP(rec, req)

			assert.Equal(t, tt.expectedStatus, rec.Code)

			if tt.expectedError != "" {
				var response map[string]interface{}
				err := json.Unmarshal(rec.Body.Bytes(), &response)
				require.NoError(t, err)
				assert.Contains(t, response["error"], tt.expectedError)
			}

			mockManager.AssertExpectations(t)
		})
	}
}

func TestBYOKHandler_GetCredential(t *testing.T) {
	mockManager := new(MockBYOKManager)
	handler := NewBYOKHandler(mockManager)

	router := chi.NewRouter()
	router.Get("/api/v1/keys/{key_id}/credentials/{provider}", handler.GetCredential)

	testTime := time.Now()

	tests := []struct {
		name           string
		provider       string
		mockSetup      func()
		expectedStatus int
	}{
		{
			name:     "Existing credential",
			provider: "openai",
			mockSetup: func() {
				mockManager.On("GetCredential", mock.Anything, "test-key", "openai").Return(&byok.Credential{
					Provider:   "openai",
					Config:     map[string]interface{}{"model": "gpt-4"},
					IsFallback: true,
					Priority:   0,
					CreatedAt:  testTime,
					LastUsed:   &testTime,
					UsageCount: 10,
				}, nil)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:     "Non-existent credential",
			provider: "anthropic",
			mockSetup: func() {
				mockManager.On("GetCredential", mock.Anything, "test-key", "anthropic").Return(nil, storage.ErrNotFound)
			},
			expectedStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockManager.ExpectedCalls = nil
			tt.mockSetup()

			req := httptest.NewRequest("GET", "/api/v1/keys/test-key/credentials/"+tt.provider, nil)
			rec := httptest.NewRecorder()

			router.ServeHTTP(rec, req)

			assert.Equal(t, tt.expectedStatus, rec.Code)

			if tt.expectedStatus == http.StatusOK {
				var response map[string]interface{}
				err := json.Unmarshal(rec.Body.Bytes(), &response)
				require.NoError(t, err)
				assert.Equal(t, tt.provider, response["provider"])
				assert.Equal(t, true, response["is_fallback"])
				assert.Equal(t, float64(0), response["priority"])
				assert.Equal(t, float64(10), response["usage_count"])
			}

			mockManager.AssertExpectations(t)
		})
	}
}

func TestBYOKHandler_DeleteCredential(t *testing.T) {
	mockManager := new(MockBYOKManager)
	handler := NewBYOKHandler(mockManager)

	router := chi.NewRouter()
	router.Delete("/api/v1/keys/{key_id}/credentials/{provider}", handler.DeleteCredential)

	tests := []struct {
		name           string
		provider       string
		mockSetup      func()
		expectedStatus int
	}{
		{
			name:     "Successful deletion",
			provider: "openai",
			mockSetup: func() {
				mockManager.On("DeleteCredential", mock.Anything, "test-key", "openai").Return(nil)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:     "Credential not found",
			provider: "anthropic",
			mockSetup: func() {
				mockManager.On("DeleteCredential", mock.Anything, "test-key", "anthropic").
					Return(errors.New("credential not found"))
			},
			expectedStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockManager.ExpectedCalls = nil
			tt.mockSetup()

			req := httptest.NewRequest("DELETE", "/api/v1/keys/test-key/credentials/"+tt.provider, nil)
			rec := httptest.NewRecorder()

			router.ServeHTTP(rec, req)

			assert.Equal(t, tt.expectedStatus, rec.Code)
			mockManager.AssertExpectations(t)
		})
	}
}

func TestBYOKHandler_ValidateCredential(t *testing.T) {
	mockManager := new(MockBYOKManager)
	handler := NewBYOKHandler(mockManager)

	router := chi.NewRouter()
	router.Post("/api/v1/keys/{key_id}/credentials/{provider}/validate", handler.ValidateCredential)

	tests := []struct {
		name           string
		body           interface{}
		mockSetup      func()
		expectedValid  bool
		expectedError  string
	}{
		{
			name: "Valid credential",
			body: map[string]interface{}{
				"credential": map[string]string{"api_key": "sk-test123"},
			},
			mockSetup: func() {
				mockManager.On("ValidateCredential", mock.Anything, "openai",
					map[string]string{"api_key": "sk-test123"},
					mock.Anything).Return(nil)
			},
			expectedValid: true,
		},
		{
			name: "Invalid credential",
			body: map[string]interface{}{
				"credential": map[string]string{"api_key": "invalid"},
			},
			mockSetup: func() {
				mockManager.On("ValidateCredential", mock.Anything, "openai",
					map[string]string{"api_key": "invalid"},
					mock.Anything).Return(&byok.ValidationError{
					Provider: "openai",
					Field:    "api_key",
					Message:  "invalid format",
				})
			},
			expectedValid: false,
			expectedError: "validation failed for openai api_key: invalid format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockManager.ExpectedCalls = nil
			tt.mockSetup()

			body, _ := json.Marshal(tt.body)
			req := httptest.NewRequest("POST", "/api/v1/keys/test-key/credentials/openai/validate", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			router.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusOK, rec.Code)

			var response map[string]interface{}
			err := json.Unmarshal(rec.Body.Bytes(), &response)
			require.NoError(t, err)

			assert.Equal(t, tt.expectedValid, response["valid"])
			if tt.expectedError != "" {
				assert.Contains(t, response["error"], tt.expectedError)
			}

			mockManager.AssertExpectations(t)
		})
	}
}