package router

import (
	"sync"
	"time"
)

// StickyProviderSessionManager manages conversation-to-provider mappings
type StickyProviderSessionManager interface {
	// GetProvider returns the provider for a conversation, if any
	GetProvider(conversationID string) (string, bool)

	// SetProvider sets the provider for a conversation
	SetProvider(conversationID string, provider string)

	// RemoveSession removes a conversation's sticky session
	RemoveSession(conversationID string)

	// CleanupExpired removes expired sessions
	CleanupExpired()
}

// memoryStickyProviderSessionManager implements in-memory sticky sessions
type memoryStickyProviderSessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*stickyProviderSession
	ttl      time.Duration
}

type stickyProviderSession struct {
	provider  string
	expiresAt time.Time
}

// NewStickyProviderSessionManager creates a new sticky session manager
func NewStickyProviderSessionManager(ttl time.Duration) StickyProviderSessionManager {
	if ttl <= 0 {
		ttl = 30 * time.Minute // Default 30 minutes
	}

	manager := &memoryStickyProviderSessionManager{
		sessions: make(map[string]*stickyProviderSession),
		ttl:      ttl,
	}

	// Start cleanup goroutine
	go manager.cleanupLoop()

	return manager
}

// GetProvider returns the provider for a conversation
func (m *memoryStickyProviderSessionManager) GetProvider(conversationID string) (string, bool) {
	if conversationID == "" {
		return "", false
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	session, exists := m.sessions[conversationID]
	if !exists {
		return "", false
	}

	// Check if expired
	if time.Now().After(session.expiresAt) {
		return "", false
	}

	return session.provider, true
}

// SetProvider sets the provider for a conversation
func (m *memoryStickyProviderSessionManager) SetProvider(conversationID string, provider string) {
	if conversationID == "" || provider == "" {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.sessions[conversationID] = &stickyProviderSession{
		provider:  provider,
		expiresAt: time.Now().Add(m.ttl),
	}
}

// RemoveSession removes a conversation's sticky session
func (m *memoryStickyProviderSessionManager) RemoveSession(conversationID string) {
	if conversationID == "" {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.sessions, conversationID)
}

// CleanupExpired removes expired sessions
func (m *memoryStickyProviderSessionManager) CleanupExpired() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for id, session := range m.sessions {
		if now.After(session.expiresAt) {
			delete(m.sessions, id)
		}
	}
}

// cleanupLoop runs periodic cleanup
func (m *memoryStickyProviderSessionManager) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		m.CleanupExpired()
	}
}
