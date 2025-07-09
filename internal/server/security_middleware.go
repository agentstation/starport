package server

import (
	"net/http"
)

// SecurityHeaders adds security headers to responses
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Prevent content sniffing
		w.Header().Set("X-Content-Type-Options", "nosniff")
		
		// Prevent clickjacking
		w.Header().Set("X-Frame-Options", "DENY")
		
		// Enable XSS protection
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		
		// Prevent exposing referrer information
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		
		// Enforce HTTPS (optional - only if TLS is configured)
		// This is commented out by default since not all deployments use TLS
		// w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		
		// Content Security Policy - restrictive by default
		// This prevents loading resources from external sources
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none';")
		
		// Permissions Policy (formerly Feature Policy)
		// Disable features that aren't needed for an API
		w.Header().Set("Permissions-Policy", "accelerometer=(), camera=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), payment=(), usb=()")
		
		next.ServeHTTP(w, r)
	})
}

// RequestSizeLimiter limits the size of incoming request bodies
func RequestSizeLimiter(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Only limit request body size for methods that have a body
			if r.Method == "POST" || r.Method == "PUT" || r.Method == "PATCH" {
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}
			next.ServeHTTP(w, r)
		})
	}
}