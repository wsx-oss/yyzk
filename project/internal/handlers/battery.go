package handlers

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

func (a *API) isDeviceOnlineForAnomaly(deviceID int) bool {
	var gpsStatus string
	var droneStatus sql.NullString
	err := a.db.QueryRow(`
		SELECT COALESCE(g.status,''), d.status
		FROM gps_devices g
		LEFT JOIN drones d ON d.linked_gps_device_id = g.id
		WHERE g.id = ?
	`, deviceID).Scan(&gpsStatus, &droneStatus)
	if err != nil {
		return false
	}
	if gpsStatus != "在线" {
		return false
	}
	if droneStatus.Valid && strings.EqualFold(strings.TrimSpace(droneStatus.String), "offline") {
		return false
	}
	return true
}

// ==================== Battery Monitoring API ====================

// BatteryRecordsList returns battery records with optional filters and pagination
func (a *API) BatteryRecordsList(c *gin.Context) {
	deviceID := strings.TrimSpace(c.Query("device_id"))
	status := strings.TrimSpace(c.Query("status"))
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 50
	}

	where := []string{"device_id IN (SELECT id FROM gps_devices WHERE COALESCE(drone_id,0)>0)"}
	args := []any{}
	if deviceID != "" {
		where = append(where, "device_id = ?")
		args = append(args, deviceID)
	}
	if status != "" {
		where = append(where, "status = ?")
		args = append(args, status)
	}

	wc := strings.Join(where, " AND ")

	var total int
	_ = a.db.QueryRow("SELECT COUNT(*) FROM battery_records WHERE "+wc, args...).Scan(&total)

	offset := (page - 1) * pageSize
	q := `SELECT id, device_id, device_name, voltage, current_val, level, temperature, health, status, charge_cycles, remaining_time, created_at FROM battery_records WHERE ` + wc + ` ORDER BY datetime(created_at) DESC LIMIT ? OFFSET ?`
	qArgs := append(args, pageSize, offset)
	rows, err := a.db.Query(q, qArgs...)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	items := []gin.H{}
	for rows.Next() {
		var id, deviceId, level, health, cycles int
		var deviceName, status, remaining, created string
		var voltage, currentVal, temperature float64
		if err := rows.Scan(&id, &deviceId, &deviceName, &voltage, &currentVal, &level, &temperature, &health, &status, &cycles, &remaining, &created); err == nil {
			items = append(items, gin.H{
				"id": id, "device_id": deviceId, "device_name": deviceName,
				"voltage": voltage, "current_val": currentVal, "level": level,
				"temperature": temperature, "health": health, "status": status,
				"charge_cycles": cycles, "remaining_time": remaining, "created_at": created,
			})
		}
	}
	c.JSON(200, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize})
}

// BatteryReport pushes a battery status report for a drone
func (a *API) BatteryReport(c *gin.Context) {
	var p struct {
		DeviceID      int     `json:"device_id"`
		Voltage       float64 `json:"voltage"`
		CurrentVal    float64 `json:"current_val"`
		Level         int     `json:"level"`
		Temperature   float64 `json:"temperature"`
		Health        int     `json:"health"`
		ChargeCycles  int     `json:"charge_cycles"`
		RemainingTime string  `json:"remaining_time"`
	}
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"error": "bad json"})
		return
	}
	if p.DeviceID == 0 {
		c.JSON(400, gin.H{"error": "请选择无人机设备"})
		return
	}

	// get device name
	var deviceName string
	err := a.db.QueryRow(`SELECT name FROM gps_devices WHERE id=?`, p.DeviceID).Scan(&deviceName)
	if err != nil {
		c.JSON(400, gin.H{"error": "无人机设备不存在"})
		return
	}

	// determine status (level=-1 means unknown — treat as normal)
	status := "正常"
	if p.Level >= 0 && p.Level <= 10 {
		status = "严重不足"
	} else if p.Level >= 0 && p.Level <= 20 {
		status = "电量低"
	} else if p.Temperature >= 50 {
		status = "温度过高"
	} else if p.Health <= 50 {
		status = "健康度低"
	}

	res, err := a.db.Exec(
		`INSERT INTO battery_records(device_id, device_name, voltage, current_val, level, temperature, health, status, charge_cycles, remaining_time) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		p.DeviceID, deviceName, p.Voltage, p.CurrentVal, p.Level, p.Temperature, p.Health, status, p.ChargeCycles, p.RemainingTime,
	)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	id, _ := res.LastInsertId()

	canAnomaly := a.isDeviceOnlineForAnomaly(p.DeviceID)

	// auto-generate alerts for abnormal conditions (skip when level is unknown/-1)
	if canAnomaly && p.Level >= 0 && p.Level <= 20 {
		msg := fmt.Sprintf("无人机[%s]电量低: %d%%", deviceName, p.Level)
		alertType := "电量低"
		if p.Level <= 10 {
			msg = fmt.Sprintf("无人机[%s]电量严重不足: %d%%，请立即返航！", deviceName, p.Level)
			alertType = "电量严重不足"
		}
		a.db.Exec(`INSERT INTO battery_alerts(device_id, device_name, level, voltage, temperature, alert_type, message) VALUES(?,?,?,?,?,?,?)`,
			p.DeviceID, deviceName, p.Level, p.Voltage, p.Temperature, alertType, msg)
		insertAlertDedup(a.db, "电池报警", "critical", msg)
	}
	if canAnomaly && p.Temperature >= 50 {
		msg := fmt.Sprintf("无人机[%s]电池温度过高: %.1f°C", deviceName, p.Temperature)
		a.db.Exec(`INSERT INTO battery_alerts(device_id, device_name, level, voltage, temperature, alert_type, message) VALUES(?,?,?,?,?,?,?)`,
			p.DeviceID, deviceName, p.Level, p.Voltage, p.Temperature, "温度过高", msg)
		insertAlertDedup(a.db, "电池报警", "warning", msg)
	}
	if canAnomaly && p.Health <= 50 {
		msg := fmt.Sprintf("无人机[%s]电池健康度低: %d%%，建议更换电池", deviceName, p.Health)
		a.db.Exec(`INSERT INTO battery_alerts(device_id, device_name, level, voltage, temperature, alert_type, message) VALUES(?,?,?,?,?,?,?)`,
			p.DeviceID, deviceName, p.Level, p.Voltage, p.Temperature, "健康度低", msg)
		insertAlertDedup(a.db, "电池报警", "warning", msg)
	}

	ThrottledBroadcast("battery", WSEvent{Type: "battery_update", Data: gin.H{"device_id": p.DeviceID, "status": status}})
	c.JSON(200, gin.H{"ok": true, "id": id})
}

// BatteryLatest returns the latest battery record for each drone
func (a *API) BatteryLatest(c *gin.Context) {
	rows, err := a.db.Query(`
		SELECT b.id, b.device_id, b.device_name, b.voltage, b.current_val, b.level, b.temperature, b.health, b.status, b.charge_cycles, b.remaining_time, b.created_at
		FROM battery_records b
		INNER JOIN (SELECT device_id, MAX(id) as max_id FROM battery_records GROUP BY device_id) latest ON b.id = latest.max_id
		INNER JOIN gps_devices g ON b.device_id = g.id AND COALESCE(g.drone_id,0) > 0
		ORDER BY b.device_name
	`)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	items := []gin.H{}
	for rows.Next() {
		var id, deviceId, level, health, cycles int
		var deviceName, status, remaining, created string
		var voltage, currentVal, temperature float64
		if err := rows.Scan(&id, &deviceId, &deviceName, &voltage, &currentVal, &level, &temperature, &health, &status, &cycles, &remaining, &created); err == nil {
			items = append(items, gin.H{
				"id": id, "device_id": deviceId, "device_name": deviceName,
				"voltage": voltage, "current_val": currentVal, "level": level,
				"temperature": temperature, "health": health, "status": status,
				"charge_cycles": cycles, "remaining_time": remaining, "created_at": created,
			})
		}
	}
	c.JSON(200, gin.H{"items": items})
}

// BatteryHistory returns battery history for a specific drone
func (a *API) BatteryHistory(c *gin.Context) {
	deviceID := c.Param("device_id")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit < 1 || limit > 500 {
		limit = 50
	}

	rows, err := a.db.Query(
		`SELECT id, device_id, device_name, voltage, current_val, level, temperature, health, status, charge_cycles, remaining_time, created_at FROM battery_records WHERE device_id=? ORDER BY datetime(created_at) DESC LIMIT ?`,
		deviceID, limit,
	)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	items := []gin.H{}
	for rows.Next() {
		var id, deviceId, level, health, cycles int
		var deviceName, status, remaining, created string
		var voltage, currentVal, temperature float64
		if err := rows.Scan(&id, &deviceId, &deviceName, &voltage, &currentVal, &level, &temperature, &health, &status, &cycles, &remaining, &created); err == nil {
			items = append(items, gin.H{
				"id": id, "device_id": deviceId, "device_name": deviceName,
				"voltage": voltage, "current_val": currentVal, "level": level,
				"temperature": temperature, "health": health, "status": status,
				"charge_cycles": cycles, "remaining_time": remaining, "created_at": created,
			})
		}
	}
	c.JSON(200, gin.H{"items": items})
}

// BatteryStats returns battery monitoring statistics
func (a *API) BatteryStats(c *gin.Context) {
	stats := gin.H{}

	// count drones that have battery records (only drone-linked devices)
	var totalDrones int
	a.db.QueryRow(`SELECT COUNT(DISTINCT b.device_id) FROM battery_records b INNER JOIN gps_devices g ON b.device_id=g.id WHERE COALESCE(g.drone_id,0)>0`).Scan(&totalDrones)
	stats["total_drones"] = totalDrones

	// latest record per drone-linked device
	latestJoin := `battery_records b INNER JOIN (SELECT device_id, MAX(id) as max_id FROM battery_records GROUP BY device_id) l ON b.id=l.max_id INNER JOIN gps_devices g ON b.device_id=g.id AND COALESCE(g.drone_id,0)>0`

	// count by status (latest record per device)
	var normal, low, critical, tempHigh, healthLow int
	a.db.QueryRow(`SELECT COUNT(*) FROM ` + latestJoin + ` WHERE b.status='正常'`).Scan(&normal)
	a.db.QueryRow(`SELECT COUNT(*) FROM ` + latestJoin + ` WHERE b.status='电量低'`).Scan(&low)
	a.db.QueryRow(`SELECT COUNT(*) FROM ` + latestJoin + ` WHERE b.status='严重不足'`).Scan(&critical)
	a.db.QueryRow(`SELECT COUNT(*) FROM ` + latestJoin + ` WHERE b.status='温度过高'`).Scan(&tempHigh)
	a.db.QueryRow(`SELECT COUNT(*) FROM ` + latestJoin + ` WHERE b.status='健康度低'`).Scan(&healthLow)
	stats["normal"] = normal
	stats["low"] = low
	stats["critical"] = critical
	stats["temp_high"] = tempHigh
	stats["health_low"] = healthLow

	// average level and health across latest records
	var avgLevel, avgHealth sql.NullFloat64
	a.db.QueryRow(`SELECT AVG(b.level), AVG(b.health) FROM `+latestJoin).Scan(&avgLevel, &avgHealth)
	stats["avg_level"] = int(avgLevel.Float64)
	stats["avg_health"] = int(avgHealth.Float64)

	// unacknowledged alerts
	var alertCount int
	a.db.QueryRow(`SELECT COUNT(*) FROM battery_alerts WHERE acknowledged=0`).Scan(&alertCount)
	stats["alert_count"] = alertCount

	c.JSON(200, stats)
}

// BatteryAlertsList returns battery alerts with pagination
func (a *API) BatteryAlertsList(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 20
	}

	var total int
	_ = a.db.QueryRow("SELECT COUNT(*) FROM battery_alerts").Scan(&total)

	offset := (page - 1) * pageSize
	rows, err := a.db.Query(
		`SELECT id, device_id, device_name, level, voltage, temperature, alert_type, message, acknowledged, created_at FROM battery_alerts ORDER BY datetime(created_at) DESC LIMIT ? OFFSET ?`,
		pageSize, offset,
	)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	items := []gin.H{}
	for rows.Next() {
		var id, deviceId, level, ack int
		var deviceName, alertType, message, created string
		var voltage, temperature float64
		if err := rows.Scan(&id, &deviceId, &deviceName, &level, &voltage, &temperature, &alertType, &message, &ack, &created); err == nil {
			items = append(items, gin.H{
				"id": id, "device_id": deviceId, "device_name": deviceName,
				"level": level, "voltage": voltage, "temperature": temperature,
				"alert_type": alertType, "message": message,
				"acknowledged": ack == 1, "created_at": created,
			})
		}
	}
	c.JSON(200, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize})
}

// BatteryPushByAgent accepts battery data from a remote agent identified by agent_id (hostname).
// It finds the gps_device by name matching agent_id, then inserts a battery record and auto-generates alerts.
func (a *API) BatteryPushByAgent(c *gin.Context) {
	var p struct {
		AgentID       string  `json:"agent_id"`
		Voltage       float64 `json:"voltage"`
		CurrentVal    float64 `json:"current_val"`
		Level         int     `json:"level"`
		Temperature   float64 `json:"temperature"`
		Health        int     `json:"health"`
		ChargeCycles  int     `json:"charge_cycles"`
		RemainingTime string  `json:"remaining_time"`
	}
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"error": "bad json"})
		return
	}
	agentID := strings.TrimSpace(p.AgentID)
	if agentID == "" {
		c.JSON(400, gin.H{"error": "agent_id required"})
		return
	}

	// Validate battery data ranges
	if p.Level < -1 {
		p.Level = 0
	}
	if p.Level > 100 {
		p.Level = 100
	}
	if p.Voltage < 0 {
		p.Voltage = 0
	}
	if p.Health < 0 {
		p.Health = 0
	}
	if p.Health > 100 {
		p.Health = 100
	}

	// Find gps_device: first by agent_id column, then fall back to name
	var deviceID int
	var deviceName string
	err := a.db.QueryRow(`SELECT id, name FROM gps_devices WHERE agent_id = ? AND agent_id != ''`, agentID).Scan(&deviceID, &deviceName)
	if err != nil {
		err = a.db.QueryRow(`SELECT id, name FROM gps_devices WHERE name = ?`, agentID).Scan(&deviceID, &deviceName)
	}
	if err != nil {
		// No GPS device registered yet for this agent; skip silently
		c.JSON(200, gin.H{"ok": false, "message": "GPS设备未注册，请先推送GPS数据"})
		return
	}

	// Determine status (level=-1 means unknown, e.g. NMEA mode — treat as normal)
	status := "正常"
	if p.Level >= 0 && p.Level <= 10 {
		status = "严重不足"
	} else if p.Level >= 0 && p.Level <= 20 {
		status = "电量低"
	} else if p.Temperature >= 50 {
		status = "温度过高"
	} else if p.Health <= 50 {
		status = "健康度低"
	}

	res, err := a.db.Exec(
		`INSERT INTO battery_records(device_id, device_name, voltage, current_val, level, temperature, health, status, charge_cycles, remaining_time) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		deviceID, deviceName, p.Voltage, p.CurrentVal, p.Level, p.Temperature, p.Health, status, p.ChargeCycles, p.RemainingTime,
	)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	id, _ := res.LastInsertId()

	canAnomaly := a.isDeviceOnlineForAnomaly(deviceID)

	// Auto-generate alerts for abnormal conditions (skip when level is unknown/-1)
	if canAnomaly && p.Level >= 0 && p.Level <= 20 {
		msg := fmt.Sprintf("无人机[%s]电量低: %d%%", deviceName, p.Level)
		alertType := "电量低"
		if p.Level <= 10 {
			msg = fmt.Sprintf("无人机[%s]电量严重不足: %d%%，请立即返航！", deviceName, p.Level)
			alertType = "电量严重不足"
		}
		a.db.Exec(`INSERT INTO battery_alerts(device_id, device_name, level, voltage, temperature, alert_type, message) VALUES(?,?,?,?,?,?,?)`,
			deviceID, deviceName, p.Level, p.Voltage, p.Temperature, alertType, msg)
		insertAlertDedup(a.db, "电池报警", "critical", msg)
	}
	if canAnomaly && p.Temperature >= 50 {
		msg := fmt.Sprintf("无人机[%s]电池温度过高: %.1f°C", deviceName, p.Temperature)
		a.db.Exec(`INSERT INTO battery_alerts(device_id, device_name, level, voltage, temperature, alert_type, message) VALUES(?,?,?,?,?,?,?)`,
			deviceID, deviceName, p.Level, p.Voltage, p.Temperature, "温度过高", msg)
		insertAlertDedup(a.db, "电池报警", "warning", msg)
	}
	if canAnomaly && p.Health <= 50 {
		msg := fmt.Sprintf("无人机[%s]电池健康度低: %d%%，建议更换电池", deviceName, p.Health)
		a.db.Exec(`INSERT INTO battery_alerts(device_id, device_name, level, voltage, temperature, alert_type, message) VALUES(?,?,?,?,?,?,?)`,
			deviceID, deviceName, p.Level, p.Voltage, p.Temperature, "健康度低", msg)
		insertAlertDedup(a.db, "电池报警", "warning", msg)
	}

	ThrottledBroadcast("battery", WSEvent{Type: "battery_update", Data: gin.H{"device_id": deviceID, "status": status}})
	c.JSON(200, gin.H{"ok": true, "id": id})
}

// BatteryAlertAck acknowledges a battery alert
func (a *API) BatteryAlertAck(c *gin.Context) {
	id := c.Param("id")
	_, err := a.db.Exec(`UPDATE battery_alerts SET acknowledged=1 WHERE id=?`, id)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	hub.Broadcast("battery", WSEvent{Type: "battery_alert_ack"})
	c.JSON(200, gin.H{"ok": true})
}

// BatteryStream is a WebSocket endpoint for real-time battery event push.
// Clients connect and receive events whenever battery data changes.
func (a *API) BatteryStream(c *gin.Context) {
	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	hub.Subscribe("battery", ws)
	defer func() {
		hub.Unsubscribe("battery", ws)
		ws.Close()
	}()
	// keep connection alive by reading (handles pings/close frames)
	for {
		if _, _, err := ws.ReadMessage(); err != nil {
			break
		}
	}
}
