package handlers

import (
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
	}
}

// GetMetrics returns current pool metrics.
func (a *TaskPoolAPI) GetMetrics(c *gin.Context) {
	c.JSON(200, a.pool.Metrics())
}
