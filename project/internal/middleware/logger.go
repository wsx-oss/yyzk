package middleware

import (
	"log"
	"time"

	"github.com/gin-gonic/gin"
)

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
