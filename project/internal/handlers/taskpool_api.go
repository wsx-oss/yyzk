package handlers

import (
	"smartcontrol/internal/middleware"
	"smartcontrol/internal/taskpool"

	"github.com/gin-gonic/gin"
)

// TaskPoolAPI exposes pool metrics via HTTP.
type TaskPoolAPI struct {
	pool *taskpool.Pool
}

// RegisterTaskPoolRoutes wires the metrics endpoint.
func RegisterTaskPoolRoutes(r *gin.Engine, pool *taskpool.Pool) {
	api := &TaskPoolAPI{pool: pool}
	g := r.Group("/api/taskpool")
	{
		g.GET("/metrics", api.GetMetrics)
		g.GET("/http-rpm", api.GetHTTPRPM)
	}
}

// GetMetrics returns current pool metrics plus backend HTTP RPM data.
func (a *TaskPoolAPI) GetMetrics(c *gin.Context) {
	m := a.pool.Metrics()
	c.JSON(200, gin.H{
		"io_workers":      m.IOWorkers,
		"cpu_workers":     m.CPUWorkers,
		"io_active":       m.IOActive,
		"cpu_active":      m.CPUActive,
		"io_queue_len":    m.IOQueueLen,
		"cpu_queue_len":   m.CPUQueueLen,
		"io_queue_cap":    m.IOQueueCap,
		"cpu_queue_cap":   m.CPUQueueCap,
		"submitted":       m.Submitted,
		"completed":       m.Completed,
		"failed":          m.Failed,
		"recovered":       m.Recovered,
		"pending":         m.Pending,
		"avg_duration_ms": m.AvgDurationMs,
		"groups":          m.Groups,
		"http_rpm":        middleware.HTTPRPMMetrics(),
	})
}

// GetHTTPRPM returns backend HTTP request-per-minute metrics.
func (a *TaskPoolAPI) GetHTTPRPM(c *gin.Context) {
	c.JSON(200, middleware.HTTPRPMMetrics())
}
