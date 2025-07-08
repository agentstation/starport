package routing

import (
	"sync"
	"time"
)

// StickySessionManager manages conversation-to-provider mappings
type StickySessionManager interface {
	// GetProvider returns the provider for a conversation, if any
	GetProvider(conversationID string) (string, bool)
	
	// SetProvider sets the provider for a conversation
	SetProvider(conversationID string, provider string)
	
	// RemoveSession removes a conversation's sticky session
	RemoveSession(conversationID string)
	
	// CleanupExpired removes expired sessions
	CleanupExpired()
}

// memoryStickySessionManager implements in-memory sticky sessions
type memoryStickySessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*stickySession
	ttl      time.Duration
}

type stickySession struct {
	provider  string
	expiresAt time.Time
}

// NewStickySessionManager creates a new sticky session manager
func NewStickySessionManager(ttl time.Duration) StickySessionManager {
	if ttl <= 0 {
		ttl = 30 * time.Minute // Default 30 minutes
	}
	
	manager := &memoryStickySessionManager{
		sessions: make(map[string]*stickySession),
		ttl:      ttl,
	}
	
	// Start cleanup goroutine
	go manager.cleanupLoop()
	
	return manager
}

// GetProvider returns the provider for a conversation
func (m *memoryStickySessionManager) GetProvider(conversationID string) (string, bool) {
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
func (m *memoryStickySessionManager) SetProvider(conversationID string, provider string) {
	if conversationID == "" || provider == "" {
		return
	}
	
	m.mu.Lock()
	defer m.mu.Unlock()
	
	m.sessions[conversationID] = &stickySession{
		provider:  provider,
		expiresAt: time.Now().Add(m.ttl),
	}
}

// RemoveSession removes a conversation's sticky session
func (m *memoryStickySessionManager) RemoveSession(conversationID string) {
	if conversationID == "" {
		return
	}
	
	m.mu.Lock()
	defer m.mu.Unlock()
	
	delete(m.sessions, conversationID)
}

// CleanupExpired removes expired sessions
func (m *memoryStickySessionManager) CleanupExpired() {
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
func (m *memoryStickySessionManager) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	
	for range ticker.C {
		m.CleanupExpired()
	}
}