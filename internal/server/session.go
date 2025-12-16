// Package server provides the HTTP server and WebSocket handler for the web interface.
package server

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

const (
	sessionCookieName = "encoder_session"
	sessionDuration   = 24 * time.Hour
)

// session represents an authenticated user session.
type session struct {
	token     string
	expiresAt time.Time
}

// SessionManager handles user authentication sessions.
type SessionManager struct {
	sessions map[string]*session
	mu       sync.RWMutex
}

// NewSessionManager creates a new session manager.
func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*session),
	}
}

// generateToken creates a cryptographically secure random token.
func generateToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// Create creates a new session and returns the token.
func (sm *SessionManager) Create() string {
	token := generateToken()
	if token == "" {
		return ""
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.sessions[token] = &session{
		token:     token,
		expiresAt: time.Now().Add(sessionDuration),
	}
	return token
}

// Validate checks if a session token is valid.
func (sm *SessionManager) Validate(token string) bool {
	if token == "" {
		return false
	}

	sm.mu.RLock()
	sess, exists := sm.sessions[token]
	sm.mu.RUnlock()

	if !exists {
		return false
	}

	if time.Now().After(sess.expiresAt) {
		sm.mu.Lock()
		delete(sm.sessions, token)
		sm.mu.Unlock()
		return false
	}

	return true
}

// AuthMiddleware returns middleware that requires HTTP Basic Authentication or a valid session cookie.
func (sm *SessionManager) AuthMiddleware(username, password string) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			// Check for valid session cookie first
			if cookie, err := r.Cookie(sessionCookieName); err == nil {
				if sm.Validate(cookie.Value) {
					next(w, r)
					return
				}
			}

			// Fall back to Basic Auth
			user, pass, ok := r.BasicAuth()
			if !ok || user != username || pass != password {
				w.Header().Set("WWW-Authenticate", `Basic realm="Encoder"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			// Basic Auth succeeded - create session cookie
			token := sm.Create()
			if token != "" {
				http.SetCookie(w, &http.Cookie{
					Name:     sessionCookieName,
					Value:    token,
					Path:     "/",
					MaxAge:   int(sessionDuration.Seconds()),
					HttpOnly: true,
					Secure:   r.TLS != nil,
					SameSite: http.SameSiteStrictMode,
				})
			}

			next(w, r)
		}
	}
}
