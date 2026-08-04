package router

import (
	"sync"
	"time"
)

const defaultMaximumStickySessions = 100_000

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
	mu        sync.RWMutex
	sessions  map[string]*stickyProviderSession
	ttl       time.Duration
	maximum   int
	lastSweep time.Time
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
		maximum:  defaultMaximumStickySessions,
	}

	return manager
}

// GetProvider returns the provider for a conversation
func (m *memoryStickyProviderSessionManager) GetProvider(conversationID string) (string, bool) {
	if conversationID == "" {
		return "", false
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	session, exists := m.sessions[conversationID]
	if !exists {
		return "", false
	}

	// Check if expired
	if time.Now().After(session.expiresAt) {
		delete(m.sessions, conversationID)
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

	now := time.Now()
	sweepInterval := m.ttl / 2
	if sweepInterval > 5*time.Second {
		sweepInterval = 5 * time.Second
	}
	if m.lastSweep.IsZero() || now.Sub(m.lastSweep) >= sweepInterval {
		m.cleanupExpiredLocked(now)
		m.lastSweep = now
	}
	if _, exists := m.sessions[conversationID]; !exists && len(m.sessions) >= m.maximum {
		for id := range m.sessions {
			delete(m.sessions, id)
			break
		}
	}
	m.sessions[conversationID] = &stickyProviderSession{
		provider:  provider,
		expiresAt: now.Add(m.ttl),
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

	m.cleanupExpiredLocked(time.Now())
}

func (m *memoryStickyProviderSessionManager) cleanupExpiredLocked(now time.Time) {
	for id, session := range m.sessions {
		if now.After(session.expiresAt) {
			delete(m.sessions, id)
		}
	}
}
