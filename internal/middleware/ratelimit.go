package middleware

import (
	"net/http"
	"sync"
	"time"
)

// RateLimiter implements a token bucket algorithm
type RateLimiter struct {
	rate       float64
	bucketSize float64
	mu         sync.Mutex
	tokens     float64
	lastRefill time.Time
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(rate float64, bucketSize float64) *RateLimiter {
	return &RateLimiter{
		rate:       rate,
		bucketSize: bucketSize,
		tokens:     bucketSize,
		lastRefill: time.Now(),
	}
}

// refill adds tokens based on elapsed time
func (rl *RateLimiter) refill() {
	now := time.Now()
	elapsed := now.Sub(rl.lastRefill).Seconds()
	rl.tokens = min(rl.bucketSize, rl.tokens+(elapsed*rl.rate))
	rl.lastRefill = now
}

// Allow checks if a request should be allowed
func (rl *RateLimiter) Allow() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.refill()
	if rl.tokens >= 1 {
		rl.tokens--
		return true
	}
	return false
}

// RateLimitMiddleware creates middleware that applies rate limiting
func RateLimitMiddleware(limiter *RateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !limiter.Allow() {
				http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
