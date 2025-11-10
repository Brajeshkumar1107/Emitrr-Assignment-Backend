package middleware

import (
	"net/http"
	"os"
	"strings"
)

// CORSMiddleware handles CORS with secure configuration
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Get allowed origins from environment
		allowedOrigins := strings.Split(os.Getenv("ALLOWED_ORIGINS"), ",")
		origin := r.Header.Get("Origin")

		// If no allowed origins are set, only allow localhost in development
		if len(allowedOrigins) == 0 {
			if strings.HasPrefix(origin, "http://localhost:") ||
				strings.HasPrefix(origin, "https://localhost:") {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			}
		} else {
			// Check if origin is in allowed list
			for _, allowed := range allowedOrigins {
				if strings.TrimSpace(allowed) == origin {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					break
				}
			}
		}

		// Set secure CORS headers
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Max-Age", "3600")
		w.Header().Set("Access-Control-Allow-Credentials", "true")

		// Handle preflight requests
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}
