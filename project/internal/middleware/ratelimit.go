package middleware

import (
	"net/http"
	"sync"
	"time"

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
		ip := c.ClientIP()
		
		if !rl.allow(ip) {
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
	tokensToAdd := int(elapsed / rl.duration * time.Duration(rl.rate))
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
