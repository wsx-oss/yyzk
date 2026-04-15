package middleware

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"smartcontrol/internal/cache"

	"github.com/gin-gonic/gin"
)

// RateLimiter implements a simple token bucket rate limiter
type RateLimiter struct {
	visitors map[string]*visitor
	mu       sync.RWMutex
	rate     int           // requests per duration
	duration time.Duration // time window
}

type visitor struct {
	lastSeen time.Time
	tokens   int
}

// NewRateLimiter creates a new rate limiter
// rate: number of requests allowed
// duration: time window (e.g., 1*time.Minute for rate per minute)
func NewRateLimiter(rate int, duration time.Duration) *RateLimiter {
	rl := &RateLimiter{
		visitors: make(map[string]*visitor),
		rate:     rate,
		duration: duration,
	}
	
	// Cleanup old visitors every 5 minutes
	go rl.cleanup()
	
	return rl
}

// Middleware returns a Gin middleware handler
func (rl *RateLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Skip rate limiting for static files, auth, WebSocket and streaming endpoints
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/app/") || path == "/" ||
			strings.HasPrefix(path, "/api/auth/") ||
			path == "/api/healthz" ||
			path == "/api/vnc/ws" || path == "/api/ssh/ws" ||
			strings.HasSuffix(path, "/stream") {
			c.Next()
			return
		}
		
		ip := c.ClientIP()
		
		if !rl.allowWithRedis(ip) {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error": "rate limit exceeded",
				"retry_after": rl.duration.Seconds(),
			})
			c.Abort()
			return
		}
		
		c.Next()
	}
}

// allowWithRedis tries Redis-based rate limiting first; falls back to in-memory.
func (rl *RateLimiter) allowWithRedis(ip string) bool {
	if !cache.Available() {
		return rl.allow(ip)
	}
	key := fmt.Sprintf("ratelimit:%s", ip)
	count, err := cache.Incr(key)
	if err != nil {
		// Redis error – degrade to in-memory
		return rl.allow(ip)
	}
	if count == 1 {
		// First request in this window – set TTL
		_ = cache.Expire(key, rl.duration)
	}
	return count <= int64(rl.rate)
}

func (rl *RateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	
	v, exists := rl.visitors[ip]
	now := time.Now()
	
	if !exists {
		rl.visitors[ip] = &visitor{
			lastSeen: now,
			tokens:   rl.rate - 1,
		}
		return true
	}
	
	// Refill tokens based on time passed
	elapsed := now.Sub(v.lastSeen)
	tokensToAdd := int(float64(elapsed) / float64(rl.duration) * float64(rl.rate))
	v.tokens += tokensToAdd
	if v.tokens > rl.rate {
		v.tokens = rl.rate
	}
	v.lastSeen = now
	
	if v.tokens > 0 {
		v.tokens--
		return true
	}
	
	return false
}

func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	
	for range ticker.C {
		rl.mu.Lock()
		now := time.Now()
		for ip, v := range rl.visitors {
			if now.Sub(v.lastSeen) > 10*time.Minute {
				delete(rl.visitors, ip)
			}
		}
		rl.mu.Unlock()
	}
}
