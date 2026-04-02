package server

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// RateLimiter implements a per-IP token-bucket rate limiter.
type RateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*bucket
	rate     float64 // tokens per second
	burst    int     // max tokens
	window   time.Duration
}

type bucket struct {
	tokens    float64
	lastSeen  time.Time
	updatedAt time.Time
}

type RateLimitConfig struct {
	// RequestsPerSecond is the sustained rate (e.g. 10 means 10 req/s).
	RequestsPerSecond float64
	// Burst is the maximum burst size allowed above the sustained rate.
	Burst int
	// CleanupInterval controls how often stale entries are evicted.
	CleanupInterval time.Duration
}

func DefaultRateLimitConfig() RateLimitConfig {
	return RateLimitConfig{
		RequestsPerSecond: 10,
		Burst:             30,
		CleanupInterval:   5 * time.Minute,
	}
}

func NewRateLimiter(cfg RateLimitConfig) *RateLimiter {
	rl := &RateLimiter{
		visitors: make(map[string]*bucket),
		rate:     cfg.RequestsPerSecond,
		burst:    cfg.Burst,
		window:   cfg.CleanupInterval,
	}
	go rl.cleanup()
	return rl
}

func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, ok := rl.visitors[key]
	if !ok {
		b = &bucket{
			tokens:    float64(rl.burst) - 1,
			lastSeen:  now,
			updatedAt: now,
		}
		rl.visitors[key] = b
		return true
	}

	elapsed := now.Sub(b.updatedAt).Seconds()
	b.tokens += elapsed * rl.rate
	if b.tokens > float64(rl.burst) {
		b.tokens = float64(rl.burst)
	}
	b.lastSeen = now
	b.updatedAt = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (rl *RateLimiter) cleanup() {
	for {
		time.Sleep(rl.window)
		rl.mu.Lock()
		cutoff := time.Now().Add(-rl.window)
		for k, b := range rl.visitors {
			if b.lastSeen.Before(cutoff) {
				delete(rl.visitors, k)
			}
		}
		rl.mu.Unlock()
	}
}

func (rl *RateLimiter) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := clientIP(r)
		if !rl.Allow(key) {
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded; retry later")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
