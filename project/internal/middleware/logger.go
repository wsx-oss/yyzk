package middleware

import (
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

// ======================== HTTP Request RPM Tracker ========================

type httpRPMTracker struct {
	totalRequests  int64
	totalFailed    int64
	totalLatencyNs int64
	windowStart    int64
	windowCount    int64
	mu             sync.Mutex
}

var globalHTTPRPM = &httpRPMTracker{windowStart: time.Now().Unix()}

func (r *httpRPMTracker) record(latency time.Duration, failed bool) {
	atomic.AddInt64(&r.totalRequests, 1)
	atomic.AddInt64(&r.totalLatencyNs, int64(latency))
	if failed {
		atomic.AddInt64(&r.totalFailed, 1)
	}
	now := time.Now().Unix()
	r.mu.Lock()
	if now-atomic.LoadInt64(&r.windowStart) >= 60 {
		atomic.StoreInt64(&r.windowCount, 1)
		atomic.StoreInt64(&r.windowStart, now)
	} else {
		atomic.AddInt64(&r.windowCount, 1)
	}
	r.mu.Unlock()
}

// HTTPRPMSnapshot holds point-in-time HTTP request rate metrics.
type HTTPRPMSnapshot struct {
	CurrentMinute int64   `json:"current_minute_requests"`
	RPMLimit      int     `json:"rpm_limit"`
	TotalRequests int64   `json:"total_requests"`
	TotalFailed   int64   `json:"total_failed"`
	AvgLatencyMs  float64 `json:"avg_latency_ms"`
}

// HTTPRPMMetrics returns a snapshot of backend HTTP request rate metrics.
func HTTPRPMMetrics() HTTPRPMSnapshot {
	total := atomic.LoadInt64(&globalHTTPRPM.totalRequests)
	failed := atomic.LoadInt64(&globalHTTPRPM.totalFailed)
	latNs := atomic.LoadInt64(&globalHTTPRPM.totalLatencyNs)
	now := time.Now().Unix()

	globalHTTPRPM.mu.Lock()
	curMin := atomic.LoadInt64(&globalHTTPRPM.windowCount)
	if now-atomic.LoadInt64(&globalHTTPRPM.windowStart) >= 60 {
		curMin = 0
	}
	globalHTTPRPM.mu.Unlock()

	var avgMs float64
	if total > 0 {
		avgMs = float64(latNs) / float64(total) / 1e6
	}

	return HTTPRPMSnapshot{
		CurrentMinute: curMin,
		RPMLimit:      500,
		TotalRequests: total,
		TotalFailed:   failed,
		AvgLatencyMs:  avgMs,
	}
}

// RequestLogger logs all HTTP requests with details
func RequestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		startTime := time.Now()
		path := c.Request.URL.Path
		raw := c.Request.URL.RawQuery
		
		// Process request
		c.Next()
		
		// Calculate latency
		latency := time.Since(startTime)
		
		// Get status code
		statusCode := c.Writer.Status()

		// Track API request RPM (skip static files)
		if strings.HasPrefix(path, "/api/") {
			globalHTTPRPM.record(latency, statusCode >= 500)
		}
		
		// Log format
		if raw != "" {
			path = path + "?" + raw
		}
		
		log.Printf("[%s] %d | %13v | %15s | %-7s %s",
			time.Now().Format("2006-01-02 15:04:05"),
			statusCode,
			latency,
			c.ClientIP(),
			c.Request.Method,
			path,
		)
		
		// Log errors if any
		if len(c.Errors) > 0 {
			for _, err := range c.Errors {
				log.Printf("ERROR: %v", err)
			}
		}
	}
}

// Recovery recovers from panics and logs the error
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("PANIC: %v", err)
				c.JSON(500, gin.H{"error": "internal server error"})
			}
		}()
		c.Next()
	}
}
