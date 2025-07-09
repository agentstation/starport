package server

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentstation/starport/internal/connectors"
)

func TestConnectorRegistry_Close(t *testing.T) {
	// Create registry
	registry := NewConnectorRegistry()

	// Create mock connectors
	mockConfig := connectors.ProviderConfig{
		BaseURL: "http://mock",
	}

	// Register multiple connectors
	mock1 := connectors.NewMockConnector(mockConfig)
	mock2 := connectors.NewMockConnector(mockConfig)
	mock3 := connectors.NewMockConnector(mockConfig)

	registry.Register("provider1", mock1)
	registry.Register("provider2", mock2)
	registry.Register("provider3", mock3)

	// Verify connectors are registered
	conn1, err := registry.GetWithError("provider1")
	require.NoError(t, err)
	assert.NotNil(t, conn1)

	// Close registry
	err = registry.Close()
	assert.NoError(t, err)

	// Verify all connectors are removed
	_, err = registry.GetWithError("provider1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "provider provider1 not found")

	_, err = registry.GetWithError("provider2")
	assert.Error(t, err)

	_, err = registry.GetWithError("provider3")
	assert.Error(t, err)

	// Verify registry can be reused
	mock4 := connectors.NewMockConnector(mockConfig)
	registry.Register("provider4", mock4)

	conn4, err := registry.GetWithError("provider4")
	require.NoError(t, err)
	assert.NotNil(t, conn4)
}

func TestConnectorRegistry_CloseMultipleConnectors(t *testing.T) {
	// Create registry
	registry := NewConnectorRegistry()

	// Create multiple mock connectors
	mockConfig := connectors.ProviderConfig{
		BaseURL: "http://mock",
	}
	
	// Register multiple connectors
	for i := 0; i < 5; i++ {
		mock := connectors.NewMockConnector(mockConfig)
		registry.Register(fmt.Sprintf("provider%d", i), mock)
	}

	// Close registry - should close all connectors successfully
	err := registry.Close()
	assert.NoError(t, err)

	// Verify all connectors are removed
	for i := 0; i < 5; i++ {
		_, err := registry.GetWithError(fmt.Sprintf("provider%d", i))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	}
}

func TestConnectorRegistry_ThreadSafety(t *testing.T) {
	// Create registry
	registry := NewConnectorRegistry()
	mockConfig := connectors.ProviderConfig{
		BaseURL: "http://mock",
	}

	// Test concurrent operations
	done := make(chan bool)

	// Goroutine 1: Register connectors
	go func() {
		for i := 0; i < 100; i++ {
			mock := connectors.NewMockConnector(mockConfig)
			registry.Register(fmt.Sprintf("provider%d", i), mock)
		}
		done <- true
	}()

	// Goroutine 2: Get connectors
	go func() {
		for i := 0; i < 100; i++ {
			_, _ = registry.GetWithError(fmt.Sprintf("provider%d", i))
		}
		done <- true
	}()

	// Goroutine 3: Close registry
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = registry.Close()
		done <- true
	}()

	// Wait for all goroutines
	for i := 0; i < 3; i++ {
		<-done
	}
}