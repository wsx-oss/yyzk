package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"smartcontrol/internal/cache"
	"smartcontrol/internal/db"

	"github.com/gin-gonic/gin"
)

// MavlinkAPI holds the database reference for MAVLink telemetry endpoints.
type MavlinkAPI struct {
	db *db.DB
}

// RegisterMavlinkRoutes registers all MAVLink telemetry API routes.
func RegisterMavlinkRoutes(r *gin.Engine, database *db.DB) {
	api := &MavlinkAPI{db: database}
	g := r.Group("/api/mavlink")
	{
		g.GET("/drones", api.ListDrones)
		g.GET("/drones/:sysid/telemetry", api.GetTelemetry)
		g.GET("/drones/:sysid/telemetry/:msg_type", api.GetTelemetryByType)
		g.GET("/drones/:sysid/position", api.GetPosition)
		g.GET("/drones/:sysid/attitude", api.GetAttitude)
		g.GET("/drones/:sysid/battery", api.GetBattery)
		g.GET("/drones/:sysid/status", api.GetDroneStatus)
		g.GET("/messages", api.ListMessages)
		g.GET("/overview", api.Overview)
		g.GET("/stream", api.StreamTelemetry)
		// Debug endpoints
		g.GET("/debug/frames", api.DebugFrames)
		g.GET("/debug/telemetry", api.DebugTelemetryLog)
		g.GET("/debug/stream", api.DebugStream)
	}
}

// ListDrones returns all MAVLink-connected drones with their latest telemetry.
func (m *MavlinkAPI) ListDrones(c *gin.Context) {
	// Try Redis first for real-time data
	droneMap := map[int]map[string]interface{}{}

	// Query mavlink_telemetry for distinct sys_ids
	rows, err := m.db.Query(`SELECT sys_id, msg_type, payload, updated_at FROM mavlink_telemetry ORDER BY sys_id, msg_type`)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	for rows.Next() {
		var sysID int
		var msgType, payload, updatedAt string
		if err := rows.Scan(&sysID, &msgType, &payload, &updatedAt); err != nil {
			continue
		}
		if _, ok := droneMap[sysID]; !ok {
			droneMap[sysID] = map[string]interface{}{
				"sys_id":     sysID,
				"agent_id":   fmt.Sprintf("mavlink-%d", sysID),
				"last_seen":  updatedAt,
				"online":     false,
				"telemetry":  map[string]interface{}{},
			}
		}
		var parsed interface{}
		if json.Unmarshal([]byte(payload), &parsed) == nil {
			droneMap[sysID]["telemetry"].(map[string]interface{})[msgType] = parsed
		}
		// Track latest update
		if updatedAt > droneMap[sysID]["last_seen"].(string) {
			droneMap[sysID]["last_seen"] = updatedAt
		}
	}

	// Check online status from Redis
	for sysID, d := range droneMap {
		if cache.Available() {
			if v, err := cache.Get(fmt.Sprintf("mavlink:drone:%d:online", sysID)); err == nil && v == "1" {
				d["online"] = true
			}
		}
		// Also check by DB updated_at within 15 seconds
		if ls, ok := d["last_seen"].(string); ok {
			if t, err := time.Parse("2006-01-02 15:04:05", ls); err == nil {
				if time.Since(t) < 15*time.Second {
					d["online"] = true
				}
			}
		}

		// Enrich with drone table info
		var name, model, status string
		var droneID int
		// Try UAV-K9-xxx format first, then mavlink-x format
		agentPatterns := []string{
			fmt.Sprintf("UAV-K9-%03d", sysID),
			fmt.Sprintf("mavlink-%d", sysID),
		}
		for _, agentID := range agentPatterns {
			err := m.db.QueryRow(`SELECT id, name, COALESCE(model,''), status FROM drones WHERE agent_id=?`,
				agentID).Scan(&droneID, &name, &model, &status)
			if err == nil {
				d["drone_id"] = droneID
				d["name"] = name
				d["agent_id"] = agentID
				d["model"] = model
				d["drone_status"] = status
				break
			}
		}
		droneMap[sysID] = d
	}

	// Convert to list
	result := make([]map[string]interface{}, 0, len(droneMap))
	for _, d := range droneMap {
		result = append(result, d)
	}
	c.JSON(200, gin.H{"items": result, "total": len(result)})
}

// GetTelemetry returns all telemetry entries for a specific drone.
func (m *MavlinkAPI) GetTelemetry(c *gin.Context) {
	sysID, _ := strconv.Atoi(c.Param("sysid"))
	if sysID <= 0 {
		c.JSON(400, gin.H{"error": "invalid sys_id"})
		return
	}

	result := map[string]interface{}{}

	// Try Redis first for real-time data
	if cache.Available() {
		keys := []string{"heartbeat", "position", "gps_raw", "attitude", "vfr_hud",
			"battery", "sys_status", "rc_channels", "landed_state", "home_position",
			"gps2_raw", "mission_current", "command_ack"}
		for _, k := range keys {
			if v, err := cache.Get(fmt.Sprintf("mavlink:drone:%d:%s", sysID, k)); err == nil {
				var parsed interface{}
				if json.Unmarshal([]byte(v), &parsed) == nil {
					result[k] = parsed
				}
			}
		}
		if len(result) > 0 {
			result["source"] = "redis"
			c.JSON(200, result)
			return
		}
	}

	// Fallback to DB
	rows, err := m.db.Query(`SELECT msg_type, payload, updated_at FROM mavlink_telemetry WHERE sys_id=?`, sysID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	for rows.Next() {
		var msgType, payload, updatedAt string
		if rows.Scan(&msgType, &payload, &updatedAt) == nil {
			var parsed interface{}
			if json.Unmarshal([]byte(payload), &parsed) == nil {
				result[msgType] = parsed
			}
		}
	}
	result["source"] = "mysql"
	c.JSON(200, result)
}

// GetTelemetryByType returns a specific telemetry type for a drone.
func (m *MavlinkAPI) GetTelemetryByType(c *gin.Context) {
	sysID, _ := strconv.Atoi(c.Param("sysid"))
	msgType := c.Param("msg_type")
	if sysID <= 0 || msgType == "" {
		c.JSON(400, gin.H{"error": "invalid params"})
		return
	}

	// Try Redis
	if cache.Available() {
		if v, err := cache.Get(fmt.Sprintf("mavlink:drone:%d:%s", sysID, msgType)); err == nil {
			var parsed interface{}
			if json.Unmarshal([]byte(v), &parsed) == nil {
				c.JSON(200, gin.H{"sys_id": sysID, "msg_type": msgType, "data": parsed, "source": "redis"})
				return
			}
		}
	}

	// Fallback to DB
	var payload, updatedAt string
	err := m.db.QueryRow(`SELECT payload, updated_at FROM mavlink_telemetry WHERE sys_id=? AND msg_type=?`, sysID, msgType).Scan(&payload, &updatedAt)
	if err == sql.ErrNoRows {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	var parsed interface{}
	json.Unmarshal([]byte(payload), &parsed)
	c.JSON(200, gin.H{"sys_id": sysID, "msg_type": msgType, "data": parsed, "updated_at": updatedAt, "source": "mysql"})
}

// GetPosition returns the latest position for a drone.
func (m *MavlinkAPI) GetPosition(c *gin.Context) {
	sysID, _ := strconv.Atoi(c.Param("sysid"))
	if sysID <= 0 {
		c.JSON(400, gin.H{"error": "invalid sys_id"})
		return
	}
	if cache.Available() {
		if v, err := cache.Get(fmt.Sprintf("mavlink:drone:%d:position", sysID)); err == nil {
			var parsed interface{}
			if json.Unmarshal([]byte(v), &parsed) == nil {
				c.JSON(200, parsed)
				return
			}
		}
	}
	// Fallback
	var payload string
	if m.db.QueryRow(`SELECT payload FROM mavlink_telemetry WHERE sys_id=? AND msg_type='global_position'`, sysID).Scan(&payload) == nil {
		var parsed interface{}
		json.Unmarshal([]byte(payload), &parsed)
		c.JSON(200, parsed)
		return
	}
	c.JSON(404, gin.H{"error": "no position data"})
}

// GetAttitude returns the latest attitude for a drone.
func (m *MavlinkAPI) GetAttitude(c *gin.Context) {
	sysID, _ := strconv.Atoi(c.Param("sysid"))
	if sysID <= 0 {
		c.JSON(400, gin.H{"error": "invalid sys_id"})
		return
	}
	if cache.Available() {
		if v, err := cache.Get(fmt.Sprintf("mavlink:drone:%d:attitude", sysID)); err == nil {
			var parsed interface{}
			if json.Unmarshal([]byte(v), &parsed) == nil {
				c.JSON(200, parsed)
				return
			}
		}
	}
	var payload string
	if m.db.QueryRow(`SELECT payload FROM mavlink_telemetry WHERE sys_id=? AND msg_type='attitude'`, sysID).Scan(&payload) == nil {
		var parsed interface{}
		json.Unmarshal([]byte(payload), &parsed)
		c.JSON(200, parsed)
		return
	}
	c.JSON(404, gin.H{"error": "no attitude data"})
}

// GetBattery returns the latest battery info for a drone.
func (m *MavlinkAPI) GetBattery(c *gin.Context) {
	sysID, _ := strconv.Atoi(c.Param("sysid"))
	if sysID <= 0 {
		c.JSON(400, gin.H{"error": "invalid sys_id"})
		return
	}
	if cache.Available() {
		if v, err := cache.Get(fmt.Sprintf("mavlink:drone:%d:battery", sysID)); err == nil {
			var parsed interface{}
			if json.Unmarshal([]byte(v), &parsed) == nil {
				c.JSON(200, parsed)
				return
			}
		}
	}
	var payload string
	if m.db.QueryRow(`SELECT payload FROM mavlink_telemetry WHERE sys_id=? AND msg_type='battery'`, sysID).Scan(&payload) == nil {
		var parsed interface{}
		json.Unmarshal([]byte(payload), &parsed)
		c.JSON(200, parsed)
		return
	}
	c.JSON(404, gin.H{"error": "no battery data"})
}

// GetDroneStatus returns a combined status view for a drone.
func (m *MavlinkAPI) GetDroneStatus(c *gin.Context) {
	sysID, _ := strconv.Atoi(c.Param("sysid"))
	if sysID <= 0 {
		c.JSON(400, gin.H{"error": "invalid sys_id"})
		return
	}

	status := gin.H{"sys_id": sysID, "online": false}

	// Check online
	if cache.Available() {
		if v, err := cache.Get(fmt.Sprintf("mavlink:drone:%d:online", sysID)); err == nil && v == "1" {
			status["online"] = true
		}
	}

	// Gather all cached telemetry
	fields := map[string]string{
		"heartbeat": "heartbeat", "position": "position", "attitude": "attitude",
		"battery": "battery", "vfr_hud": "vfr_hud", "gps_raw": "gps_raw",
		"landed_state": "landed_state", "home_position": "home_position",
	}
	for label, key := range fields {
		if cache.Available() {
			if v, err := cache.Get(fmt.Sprintf("mavlink:drone:%d:%s", sysID, key)); err == nil {
				var parsed interface{}
				if json.Unmarshal([]byte(v), &parsed) == nil {
					status[label] = parsed
					continue
				}
			}
		}
		// DB fallback
		var payload string
		msgKey := key
		if key == "position" {
			msgKey = "global_position"
		}
		if m.db.QueryRow(`SELECT payload FROM mavlink_telemetry WHERE sys_id=? AND msg_type=?`, sysID, msgKey).Scan(&payload) == nil {
			var parsed interface{}
			json.Unmarshal([]byte(payload), &parsed)
			status[label] = parsed
		}
	}
	c.JSON(200, status)
}

// ListMessages returns MAVLink status text messages with pagination.
func (m *MavlinkAPI) ListMessages(c *gin.Context) {
	sysID := strings.TrimSpace(c.Query("sys_id"))
	severity := strings.TrimSpace(c.Query("severity"))
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 50
	}

	where := []string{"1=1"}
	args := []interface{}{}
	if sysID != "" {
		where = append(where, "sys_id=?")
		args = append(args, sysID)
	}
	if severity != "" {
		where = append(where, "severity=?")
		args = append(args, severity)
	}
	wc := strings.Join(where, " AND ")

	var total int
	m.db.QueryRow("SELECT COUNT(*) FROM mavlink_message_log WHERE "+wc, args...).Scan(&total)

	offset := (page - 1) * pageSize
	q := `SELECT id, sys_id, msg_type, severity, message, created_at FROM mavlink_message_log WHERE ` + wc + ` ORDER BY id DESC LIMIT ? OFFSET ?`
	qArgs := append(args, pageSize, offset)
	rows, err := m.db.Query(q, qArgs...)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	items := []gin.H{}
	for rows.Next() {
		var id, sid int
		var msgType, sev, msg, created string
		if rows.Scan(&id, &sid, &msgType, &sev, &msg, &created) == nil {
			items = append(items, gin.H{
				"id": id, "sys_id": sid, "msg_type": msgType,
				"severity": sev, "message": msg, "created_at": created,
			})
		}
	}
	c.JSON(200, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize})
}

// Overview returns a summary of all MAVLink drones.
func (m *MavlinkAPI) Overview(c *gin.Context) {
	var totalDrones, onlineDrones int
	m.db.QueryRow(`SELECT COUNT(DISTINCT sys_id) FROM mavlink_telemetry`).Scan(&totalDrones)

	// Count online (updated within 15s)
	m.db.QueryRow(`SELECT COUNT(DISTINCT sys_id) FROM mavlink_telemetry WHERE updated_at >= DATE_SUB(NOW(), INTERVAL 15 SECOND)`).Scan(&onlineDrones)

	var totalMessages int
	m.db.QueryRow(`SELECT COUNT(*) FROM mavlink_message_log`).Scan(&totalMessages)

	var recentAlerts int
	m.db.QueryRow(`SELECT COUNT(*) FROM mavlink_message_log WHERE severity IN ('emergency','alert','critical','error','warning') AND created_at >= DATE_SUB(NOW(), INTERVAL 1 HOUR)`).Scan(&recentAlerts)

	c.JSON(200, gin.H{
		"total_drones":        totalDrones,
		"online_drones":       onlineDrones,
		"total_messages":      totalMessages,
		"recent_alerts_1h":    recentAlerts,
	})
}

// StreamTelemetry provides SSE stream of real-time telemetry from Redis.
func (m *MavlinkAPI) StreamTelemetry(c *gin.Context) {
	sysID := c.DefaultQuery("sys_id", "1")

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(500, gin.H{"error": "streaming not supported"})
		return
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	ctx := c.Request.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			data := map[string]interface{}{"sys_id": sysID, "ts": time.Now().UnixMilli()}

			if cache.Available() {
				keys := []string{"position", "attitude", "battery", "vfr_hud", "heartbeat", "gps_raw", "landed_state"}
				for _, k := range keys {
					if v, err := cache.Get(fmt.Sprintf("mavlink:drone:%s:%s", sysID, k)); err == nil {
						var parsed interface{}
						if json.Unmarshal([]byte(v), &parsed) == nil {
							data[k] = parsed
						}
					}
				}
			}

			jsonData, _ := json.Marshal(data)
			fmt.Fprintf(c.Writer, "data: %s\n\n", jsonData)
			flusher.Flush()
		}
	}
}

// ---- Debug endpoints for developer console ----

// DebugFrames returns recent raw MAVLink frames from device_tcp_log.
func (m *MavlinkAPI) DebugFrames(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if limit < 1 || limit > 500 {
		limit = 100
	}
	sinceID, _ := strconv.Atoi(c.DefaultQuery("since_id", "0"))

	where := "msg_type IN ('mavlink_v1','mavlink_v2')"
	args := []interface{}{}
	if sinceID > 0 {
		where += " AND id > ?"
		args = append(args, sinceID)
	}
	args = append(args, limit)

	q := `SELECT id, agent_id, msg_type, payload, received_at FROM device_tcp_log WHERE ` + where + ` ORDER BY id DESC LIMIT ?`
	rows, err := m.db.Query(q, args...)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	items := []gin.H{}
	for rows.Next() {
		var id int
		var agentID, msgType, payload, receivedAt string
		if rows.Scan(&id, &agentID, &msgType, &payload, &receivedAt) == nil {
			var parsed interface{}
			json.Unmarshal([]byte(payload), &parsed)
			items = append(items, gin.H{
				"id": id, "agent_id": agentID, "msg_type": msgType,
				"payload": parsed, "raw_payload": payload, "received_at": receivedAt,
			})
		}
	}
	c.JSON(200, gin.H{"items": items, "count": len(items)})
}

// DebugTelemetryLog returns recent parsed telemetry entries.
func (m *MavlinkAPI) DebugTelemetryLog(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if limit < 1 || limit > 500 {
		limit = 100
	}
	sysID := c.Query("sys_id")
	msgType := c.Query("msg_type")

	where := "1=1"
	args := []interface{}{}
	if sysID != "" {
		where += " AND sys_id=?"
		args = append(args, sysID)
	}
	if msgType != "" {
		where += " AND msg_type=?"
		args = append(args, msgType)
	}
	args = append(args, limit)

	q := `SELECT sys_id, msg_type, payload, updated_at FROM mavlink_telemetry WHERE ` + where + ` ORDER BY updated_at DESC LIMIT ?`
	rows, err := m.db.Query(q, args...)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	items := []gin.H{}
	for rows.Next() {
		var sid int
		var mt, payload, updatedAt string
		if rows.Scan(&sid, &mt, &payload, &updatedAt) == nil {
			var parsed interface{}
			json.Unmarshal([]byte(payload), &parsed)
			items = append(items, gin.H{
				"sys_id": sid, "comp_id": 1, "msg_type": mt,
				"data": parsed, "raw_json": payload, "updated_at": updatedAt,
			})
		}
	}
	c.JSON(200, gin.H{"items": items, "count": len(items)})
}

// DebugStream provides an SSE stream that pushes ALL telemetry for ALL drones every 300ms.
func (m *MavlinkAPI) DebugStream(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(500, gin.H{"error": "streaming not supported"})
		return
	}

	allKeys := []string{
		"heartbeat", "position", "gps_raw", "attitude", "vfr_hud",
		"battery", "sys_status", "rc_channels", "landed_state",
		"home_position", "gps2_raw", "mission_current", "command_ack",
		"status_text", "autopilot_version",
	}

	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()

	ctx := c.Request.Context()
	seqNo := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			seqNo++
			// Gather all sys_ids from DB
			sysIDs := []int{}
			rows, err := m.db.Query(`SELECT DISTINCT sys_id FROM mavlink_telemetry`)
			if err == nil {
				for rows.Next() {
					var sid int
					if rows.Scan(&sid) == nil {
						sysIDs = append(sysIDs, sid)
					}
				}
				rows.Close()
			}

			drones := map[string]interface{}{}
			for _, sid := range sysIDs {
				drone := map[string]interface{}{"sys_id": sid, "online": false}

				// Check online from Redis
				if cache.Available() {
					if v, err := cache.Get(fmt.Sprintf("mavlink:drone:%d:online", sid)); err == nil && v == "1" {
						drone["online"] = true
					}
				}

				// Fetch all telemetry keys from Redis
				telem := map[string]interface{}{}
				for _, k := range allKeys {
					if cache.Available() {
						if v, err := cache.Get(fmt.Sprintf("mavlink:drone:%d:%s", sid, k)); err == nil {
							var parsed interface{}
							if json.Unmarshal([]byte(v), &parsed) == nil {
								telem[k] = parsed
							}
						}
					}
				}
				// If Redis had nothing, try DB
				if len(telem) == 0 {
					dbRows, err := m.db.Query(`SELECT msg_type, payload FROM mavlink_telemetry WHERE sys_id=?`, sid)
					if err == nil {
						for dbRows.Next() {
							var mt, pl string
							if dbRows.Scan(&mt, &pl) == nil {
								var parsed interface{}
								if json.Unmarshal([]byte(pl), &parsed) == nil {
									telem[mt] = parsed
								}
							}
						}
						dbRows.Close()
					}
				}
				drone["telemetry"] = telem
				drones[fmt.Sprintf("%d", sid)] = drone
			}

			// Also pull the 5 most recent raw frames
			recentFrames := []interface{}{}
			fRows, err := m.db.Query(`SELECT id, agent_id, msg_type, payload, received_at FROM device_tcp_log WHERE msg_type IN ('mavlink_v1','mavlink_v2') ORDER BY id DESC LIMIT 5`)
			if err == nil {
				for fRows.Next() {
					var id int
					var aid, mt, pl, ra string
					if fRows.Scan(&id, &aid, &mt, &pl, &ra) == nil {
						var parsed interface{}
						json.Unmarshal([]byte(pl), &parsed)
						recentFrames = append(recentFrames, map[string]interface{}{
							"id": id, "agent_id": aid, "msg_type": mt,
							"payload": parsed, "received_at": ra,
						})
					}
				}
				fRows.Close()
			}

			evt := map[string]interface{}{
				"seq":           seqNo,
				"ts":            time.Now().Format("2006-01-02 15:04:05.000"),
				"ts_unix":       time.Now().UnixMilli(),
				"drones":        drones,
				"recent_frames": recentFrames,
				"sys_ids":       sysIDs,
			}
			jsonData, _ := json.Marshal(evt)
			fmt.Fprintf(c.Writer, "data: %s\n\n", jsonData)
			flusher.Flush()
		}
	}
}
