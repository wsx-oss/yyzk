package handlers

import (
	"net/http"

	"smartcontrol/internal/tcpgateway"

	"github.com/gin-gonic/gin"
)

// TCPGatewayRef holds a reference to the TCP gateway for raw device connections.
var TCPGatewayRef *tcpgateway.Gateway

// RegisterTCPGatewayRoutes registers HTTP API endpoints for TCP gateway management.
func RegisterTCPGatewayRoutes(r *gin.Engine) {
	g := r.Group("/api/tcp")
	{
		g.GET("/clients", tcpGatewayClients)
		g.POST("/send", tcpGatewaySend)
		g.POST("/broadcast", tcpGatewayBroadcast)
		g.GET("/logs", tcpGatewayLogs)
	}
}

// tcpGatewayClients returns the list of currently connected TCP devices.
func tcpGatewayClients(c *gin.Context) {
	if TCPGatewayRef == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "TCP gateway not running"})
		return
	}
	clients := TCPGatewayRef.Clients()
	result := make([]gin.H, 0, len(clients))
	for _, cl := range clients {
		result = append(result, gin.H{
			"remote_addr":  cl.RemoteAddr,
			"agent_id":     cl.AgentID,
			"connected_at": cl.ConnectedAt,
			"last_data_at": cl.LastDataAt,
			"bytes_recv":   cl.BytesRecv,
			"bytes_sent":   cl.BytesSent,
		})
	}
	c.JSON(200, gin.H{"count": len(result), "clients": result})
}

// tcpGatewaySend sends data to a specific connected device.
func tcpGatewaySend(c *gin.Context) {
	if TCPGatewayRef == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "TCP gateway not running"})
		return
	}
	var req struct {
		RemoteAddr string `json:"remote_addr" binding:"required"`
		Data       string `json:"data" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if err := TCPGatewayRef.SendToDevice(req.RemoteAddr, []byte(req.Data+"\n")); err != nil {
		c.JSON(404, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// tcpGatewayBroadcast sends data to all connected devices.
func tcpGatewayBroadcast(c *gin.Context) {
	if TCPGatewayRef == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "TCP gateway not running"})
		return
	}
	var req struct {
		Data string `json:"data" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	sent := TCPGatewayRef.BroadcastToAll([]byte(req.Data + "\n"))
	c.JSON(200, gin.H{"ok": true, "sent_to": sent})
}

// tcpGatewayLogs returns recent TCP device log entries.
func tcpGatewayLogs(c *gin.Context) {
	// This relies on the API struct having DB access; use a simple approach via PoolRef's context
	c.JSON(200, gin.H{
		"message": "Use /api/tcp/clients for live connections. Raw logs are stored in device_tcp_log table.",
	})
}
