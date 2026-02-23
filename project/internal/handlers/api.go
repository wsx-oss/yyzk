package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"smartcontrol/internal/monitor"
	"smartcontrol/internal/syncengine"
	"smartcontrol/internal/utils"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"
)

type API struct {
	db      *sql.DB
	syncEng *syncengine.Engine
}

// DevicesList returns devices filtered by optional name and protocol, ordered with matching items first
func (a *API) DevicesList(c *gin.Context) {
	name := strings.TrimSpace(c.Query("name"))
	protocol := strings.TrimSpace(strings.ToUpper(c.Query("protocol")))

	var args []any
	// 不做 WHERE 过滤，仅按匹配程度置顶
	q := "SELECT id, name, ip, protocol, port, username, auto_connect, log_enabled, description, status, created_at, updated_at, last_connected_at FROM devices WHERE COALESCE(drone_id,0) > 0"
	// 按名称匹配、协议匹配进行权重排序，其次按创建时间倒序
	q += " ORDER BY (CASE WHEN ? != '' AND LOWER(name) LIKE LOWER(?) THEN 0 ELSE 1 END)"
	q += ", (CASE WHEN ? != '' AND UPPER(protocol) = ? THEN 0 ELSE 1 END)"
	q += ", datetime(created_at) DESC"
	args = append(args, name, "%"+name+"%", protocol, protocol)

	rows, err := a.db.Query(q, args...)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	items := []gin.H{}
	for rows.Next() {
		var id, port, autoConn, logEn int
		var n, ip, proto, username, desc, status string
		var created, updated, last sql.NullString
		if err := rows.Scan(&id, &n, &ip, &proto, &port, &username, &autoConn, &logEn, &desc, &status, &created, &updated, &last); err == nil {
			items = append(items, gin.H{
				"id": id, "name": n, "ip": ip, "protocol": proto, "port": port,
				"username": username, "auto_connect": autoConn == 1, "log_enabled": logEn == 1,
				"description": desc, "status": status,
				"created_at": created.String, "updated_at": updated.String, "last_connected_at": last.String,
			})
		}
	}
	c.JSON(200, gin.H{"items": items})
}

// DevicesCreate inserts a new device with offline status
func (a *API) DevicesCreate(c *gin.Context) {
	var p struct {
		Name        string `json:"name"`
		IP          string `json:"ip"`
		Protocol    string `json:"protocol"`
		Port        int    `json:"port"`
		Username    string `json:"username"`
		Password    string `json:"password"`
		AutoConnect bool   `json:"auto_connect"`
		LogEnabled  bool   `json:"log_enabled"`
		Description string `json:"description"`
	}
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"error": "bad json"})
		return
	}
	p.Name = strings.TrimSpace(p.Name)
	p.IP = strings.TrimSpace(p.IP)
	p.Protocol = strings.ToUpper(strings.TrimSpace(p.Protocol))
	if p.Name == "" || p.IP == "" || p.Protocol == "" {
		c.JSON(400, gin.H{"error": "name, ip, protocol required"})
		return
	}
	if p.Protocol != "VNC" && p.Protocol != "RDP" && p.Protocol != "SSH" {
		c.JSON(400, gin.H{"error": "protocol must be VNC/RDP/SSH"})
		return
	}
	// duplicate check
	var cnt int
	if err := a.db.QueryRow(`SELECT COUNT(1) FROM devices WHERE name = ? AND ip = ? AND protocol = ?`, p.Name, p.IP, p.Protocol).Scan(&cnt); err == nil && cnt > 0 {
		c.JSON(409, gin.H{"error": "device already exists"})
		return
	}
	ac := 0
	if p.AutoConnect {
		ac = 1
	}
	le := 0
	if p.LogEnabled {
		le = 1
	}
	_, err := a.db.Exec(`INSERT INTO devices(name, ip, protocol, port, username, password, auto_connect, log_enabled, description, status, created_at, updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,datetime('now'),datetime('now'))`,
		p.Name, p.IP, p.Protocol, p.Port, p.Username, p.Password, ac, le, p.Description, "offline")
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// DevicesDisconnectAll sets all online devices to offline
func (a *API) DevicesDisconnectAll(c *gin.Context) {
	_, err := a.db.Exec(`UPDATE devices SET status='offline', updated_at=datetime('now') WHERE status='online'`)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// DevicesDelete removes a device by id
func (a *API) DevicesDelete(c *gin.Context) {
	id := c.Param("id")
	if _, err := a.db.Exec(`DELETE FROM devices WHERE id = ?`, id); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// DeviceSetStatus sets device online/offline and updates times
func (a *API) DeviceSetStatus(c *gin.Context) {
	id := c.Param("id")
	var p struct{ Status string }
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"error": "bad json"})
		return
	}
	s := strings.ToLower(strings.TrimSpace(p.Status))
	if s != "online" && s != "offline" {
		c.JSON(400, gin.H{"error": "status must be online/offline"})
		return
	}
	if s == "online" {
		_, err := a.db.Exec(`UPDATE devices SET status='online', last_connected_at=datetime('now'), updated_at=datetime('now') WHERE id=?`, id)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
	} else {
		_, err := a.db.Exec(`UPDATE devices SET status='offline', updated_at=datetime('now') WHERE id=?`, id)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
	}
	c.JSON(200, gin.H{"ok": true})
}

func (a *API) userIDFromToken(c *gin.Context) (int, error) {
	token := c.GetHeader("Authorization")
	if token == "" {
		token = c.Query("token")
	}
	if token == "" {
		return 0, sql.ErrNoRows
	}
	if len(token) > 7 && token[:7] == "Bearer " {
		token = token[7:]
	}
	var uid int
	err := a.db.QueryRow(`SELECT user_id FROM sessions WHERE token = ?`, token).Scan(&uid)
	if err != nil {
		return 0, err
	}
	return uid, nil
}

// UserStatsGet returns total_connections for current user
func (a *API) UserStatsGet(c *gin.Context) {
	uid, err := a.userIDFromToken(c)
	if err != nil {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}
	var total sql.NullInt64
	_ = a.db.QueryRow(`SELECT total_connections FROM user_stats WHERE user_id = ?`, uid).Scan(&total)
	c.JSON(200, gin.H{"total_connections": total.Int64})
}

// UserStatsIncrConnection increments total_connections for current user
func (a *API) UserStatsIncrConnection(c *gin.Context) {
	uid, err := a.userIDFromToken(c)
	if err != nil {
		c.JSON(401, gin.H{"error": "unauthorized"})
		return
	}
	// ensure row exists then increment
	if _, err := a.db.Exec(`INSERT OR IGNORE INTO user_stats(user_id, total_connections) VALUES(?, 0)`, uid); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	if _, err := a.db.Exec(`UPDATE user_stats SET total_connections = total_connections + 1, updated_at=datetime('now') WHERE user_id = ?`, uid); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func RegisterRoutes(r *gin.Engine, database *sql.DB) {
	a := &API{db: database, syncEng: syncengine.New(database)}
	api := r.Group("/api")
	{
		api.GET("/metrics/snapshot", a.MetricsSnapshot)
		api.GET("/metrics/stream", a.MetricsStream)
		api.GET("/hardware/snapshot", a.HardwareSnapshot)

		api.POST("/audio/upload", a.AudioUpload)
		api.POST("/audio/add", a.AudioAdd)
		api.GET("/audio/list", a.AudioList)
		api.GET("/audio/download/:id", a.AudioDownload)
		api.DELETE("/audio/:id", a.AudioDelete)

		api.GET("/alerts/list", a.AlertsList)
		api.POST("/alerts/ack/:id", a.AlertAck)
		api.POST("/alerts/new", a.AlertNew)
		api.POST("/alerts/import", a.AlertsImport)
		api.DELETE("/alerts/clear-resolved", a.AlertsClearResolved)
		api.GET("/alerts/stats", a.AlertsStats)
		api.POST("/alerts/resolve/:id", a.AlertResolve)

		api.POST("/logs/append", a.LogAppend)
		api.GET("/logs/list", a.LogList)
		api.PUT("/logs/:id", a.LogEdit)
		api.DELETE("/logs/:id", a.LogDelete)
		api.POST("/logs/import", a.LogImport)
		api.GET("/logs/stats", a.LogStats)

		api.GET("/updates/list", a.UpdatesList)
		api.POST("/updates/add", a.UpdatesAdd)
		api.PUT("/updates/:id", a.UpdatesEdit)
		api.DELETE("/updates/:id", a.UpdatesDelete)
		api.POST("/updates/import", a.UpdatesImport)
		api.GET("/updates/stats", a.UpdatesStats)
		api.GET("/updates/check", a.UpdatesCheck)

		api.GET("/sync/status", a.SyncStatusGet)
		api.POST("/sync/status", a.SyncStatusSet)

		// sync tasks management
		api.GET("/sync/tasks", a.SyncTasksList)
		api.POST("/sync/tasks", a.SyncTasksCreate)
		api.PUT("/sync/tasks/:id", a.SyncTasksEdit)
		api.DELETE("/sync/tasks/:id", a.SyncTasksDelete)
		api.POST("/sync/tasks/import", a.SyncTasksImport)
		api.GET("/sync/tasks/stats", a.SyncTasksStats)
		api.GET("/sync/tasks/progress", a.SyncTasksProgress)
		api.GET("/sync/tasks/info", a.SyncTasksInfo)
		api.POST("/sync/tasks/:id/start", a.SyncTaskStart)
		api.POST("/sync/tasks/:id/stop", a.SyncTaskStop)
		api.POST("/sync/tasks/stop-all", a.SyncTaskStopAll)
		api.POST("/sync/tasks/check-ip", a.SyncCheckIP)
		api.GET("/sync/ping", a.SyncPing)
		api.GET("/sync/export-data", a.SyncExportData)
		api.POST("/sync/import-data", a.SyncImportData)

		api.GET("/report/perf", a.ReportPerf)
		api.GET("/report/perf-list", a.PerfReportList)
		api.POST("/report/perf-add", a.PerfReportAdd)
		api.POST("/report/perf-import", a.PerfReportImport)
		api.DELETE("/report/perf-delete/:id", a.PerfReportDelete)
		api.POST("/report/perf-collect", a.PerfCollect)

		r.GET("/api/vnc/ws", a.VNCProxyWS)
		r.GET("/api/ssh/ws", a.SSHProxyWS)

		// devices (read-only + status; create/delete managed by /drones)
		api.GET("/devices", a.DevicesList)
		api.POST("/devices/disconnect-all", a.DevicesDisconnectAll)
		api.POST("/devices/:id/status", a.DeviceSetStatus)

		// hardware items management
		api.GET("/hardware/items", a.HardwareItemsList)
		api.POST("/hardware/items", a.HardwareItemsCreate)
		api.GET("/hardware/items/stats", a.HardwareItemsStats)
		api.POST("/hardware/items/refresh", a.HardwareItemsRefresh)
		api.GET("/hardware/items/:id", a.HardwareItemsGet)
		api.PUT("/hardware/items/:id", a.HardwareItemsUpdate)
		api.DELETE("/hardware/items/:id", a.HardwareItemsDelete)
		api.POST("/hardware/items/:id/refresh", a.HardwareItemsRefreshOne)
		api.POST("/hardware/items/check-agent", a.HardwareCheckAgent)
		api.GET("/hardware/items/live", a.HardwareItemsLive)
		api.POST("/hardware/push", a.HardwarePush)

		// user stats
		api.GET("/user/stats", a.UserStatsGet)
		api.POST("/user/stats/incr_connection", a.UserStatsIncrConnection)

		// flight missions management
		api.GET("/flight/missions", a.FlightMissionsList)
		api.POST("/flight/missions", a.FlightMissionsCreate)
		api.GET("/flight/missions/stats", a.FlightMissionsStats)
		api.POST("/flight/missions/import", a.FlightMissionsImport)
		api.GET("/flight/missions/:id", a.FlightMissionsGet)
		api.PUT("/flight/missions/:id", a.FlightMissionsUpdate)
		api.DELETE("/flight/missions/:id", a.FlightMissionsDelete)
		api.POST("/flight/missions/:id/phase", a.FlightMissionsUpdatePhase)
		api.GET("/flight/missions/:id/logs", a.FlightMissionsLogs)
		api.POST("/flight/missions/push", a.FlightMissionPushByAgent)

		// LLM flight plan (smart route planning)
		api.POST("/flight/missions/plan", a.FlightPlanCreate)
		api.GET("/flight/missions/plans", a.FlightPlanList)
		api.GET("/flight/missions/plan/status", a.FlightPlanStatus)
		api.GET("/flight/missions/plan/:id", a.FlightPlanGet)
		api.POST("/flight/missions/plan/:id/adopt", a.FlightPlanAdopt)
		api.POST("/flight/missions/plan/:id/discard", a.FlightPlanDiscard)

		// AMap geocoding (address ↔ coordinates)
		api.POST("/amap/geocode", a.AmapGeocode)
		api.POST("/amap/regeocode", a.AmapRegeocode)

		// GPS / location tracking (read-only + push; create/update/delete managed by /drones)
		api.GET("/gps/devices", a.GpsDevicesList)
		api.GET("/gps/devices/stats", a.GpsStats)
		api.GET("/gps/devices/:id", a.GpsDevicesGet)
		api.POST("/gps/devices/:id/push", a.GpsDevicesPush)
		api.GET("/gps/devices/:id/history", a.GpsDevicesHistory)
		api.GET("/gps/fence-alerts", a.GpsFenceAlerts)
		api.POST("/gps/fence-alerts/:id/ack", a.GpsFenceAlertAck)
		api.POST("/gps/push", a.GpsPushByAgent)

		// Unified drone registry
		api.GET("/drones", a.DronesList)
		api.POST("/drones", a.DronesCreate)
		api.GET("/drones/stats", a.DronesStats)
		api.GET("/drones/:id", a.DronesGet)
		api.PUT("/drones/:id", a.DronesUpdate)
		api.DELETE("/drones/:id", a.DronesDelete)

		// Battery monitoring
		api.GET("/battery/records", a.BatteryRecordsList)
		api.POST("/battery/report", a.BatteryReport)
		api.GET("/battery/latest", a.BatteryLatest)
		api.GET("/battery/history/:device_id", a.BatteryHistory)
		api.GET("/battery/stats", a.BatteryStats)
		api.GET("/battery/alerts", a.BatteryAlertsList)
		api.POST("/battery/alerts/:id/ack", a.BatteryAlertAck)
		api.POST("/battery/push", a.BatteryPushByAgent)

		// Real-time WebSocket streams (event-driven push)
		r.GET("/api/gps/stream", a.GpsStream)
		r.GET("/api/battery/stream", a.BatteryStream)
		r.GET("/api/flight/stream", a.FlightStream)
	}
}

// ==================== Hardware Items API ====================

func (a *API) HardwareItemsList(c *gin.Context) {
	// Filtering parameters
	name := strings.TrimSpace(c.Query("name"))
	hwType := strings.TrimSpace(c.Query("type"))
	ip := strings.TrimSpace(c.Query("ip"))
	status := strings.TrimSpace(c.Query("status"))
	dateFrom := strings.TrimSpace(c.Query("date_from"))
	dateTo := strings.TrimSpace(c.Query("date_to"))
	tempMin := strings.TrimSpace(c.Query("temp_min"))
	tempMax := strings.TrimSpace(c.Query("temp_max"))
	cpuMin := strings.TrimSpace(c.Query("cpu_min"))
	cpuMax := strings.TrimSpace(c.Query("cpu_max"))

	where := []string{"1=1"}
	args := []any{}

	if name != "" {
		where = append(where, "LOWER(name) LIKE LOWER(?)")
		args = append(args, "%"+name+"%")
	}
	if hwType != "" && hwType != "全部" {
		where = append(where, "type = ?")
		args = append(args, hwType)
	}
	if ip != "" {
		where = append(where, "ip LIKE ?")
		args = append(args, "%"+ip+"%")
	}
	if status != "" && status != "全部" {
		where = append(where, "status = ?")
		args = append(args, status)
	}
	if dateFrom != "" {
		where = append(where, "datetime(detected_at) >= datetime(?)")
		args = append(args, dateFrom)
	}
	if dateTo != "" {
		where = append(where, "datetime(detected_at) <= datetime(?)")
		args = append(args, dateTo)
	}
	if tempMin != "" {
		if v, err := strconv.ParseFloat(tempMin, 64); err == nil {
			where = append(where, "temperature >= ?")
			args = append(args, v)
		}
	}
	if tempMax != "" {
		if v, err := strconv.ParseFloat(tempMax, 64); err == nil {
			where = append(where, "temperature <= ?")
			args = append(args, v)
		}
	}
	if cpuMin != "" {
		if v, err := strconv.ParseFloat(cpuMin, 64); err == nil {
			where = append(where, "cpu_usage >= ?")
			args = append(args, v)
		}
	}
	if cpuMax != "" {
		if v, err := strconv.ParseFloat(cpuMax, 64); err == nil {
			where = append(where, "cpu_usage <= ?")
			args = append(args, v)
		}
	}

	whereClause := strings.Join(where, " AND ")

	// Total count
	var total int
	_ = a.db.QueryRow("SELECT COUNT(*) FROM hardware_items WHERE "+whereClause, args...).Scan(&total)

	// Pagination
	pagination := utils.GetPagination(c)
	queryArgs := append(args, pagination.PageSize, pagination.Offset)

	rows, err := a.db.Query("SELECT id, name, type, ip, status, description, temperature, cpu_usage, mem_usage, network_bandwidth, detected_at, created_at, updated_at FROM hardware_items WHERE "+whereClause+" ORDER BY datetime(detected_at) DESC LIMIT ? OFFSET ?", queryArgs...)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	var items []gin.H
	for rows.Next() {
		var id int
		var name, hwType, ip, status, desc, bandwidth, detectedAt, createdAt, updatedAt string
		var temp, cpuUsage, memUsage float64
		if err := rows.Scan(&id, &name, &hwType, &ip, &status, &desc, &temp, &cpuUsage, &memUsage, &bandwidth, &detectedAt, &createdAt, &updatedAt); err == nil {
			items = append(items, gin.H{
				"id": id, "name": name, "type": hwType, "ip": ip, "status": status,
				"description": desc, "temperature": temp, "cpu_usage": cpuUsage,
				"mem_usage": memUsage, "network_bandwidth": bandwidth,
				"detected_at": detectedAt, "created_at": createdAt, "updated_at": updatedAt,
			})
		}
	}
	c.JSON(200, gin.H{"items": items, "total": total, "page": pagination.Page, "page_size": pagination.PageSize})
}

func (a *API) HardwareItemsCreate(c *gin.Context) {
	var p struct {
		Name        string  `json:"name"`
		Type        string  `json:"type"`
		IP          string  `json:"ip"`
		Status      string  `json:"status"`
		Description string  `json:"description"`
		Temperature float64 `json:"temperature"`
		CPUUsage    float64 `json:"cpu_usage"`
		MemUsage    float64 `json:"mem_usage"`
		Bandwidth   string  `json:"network_bandwidth"`
	}
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"error": "参数格式错误"})
		return
	}
	p.Name = strings.TrimSpace(p.Name)
	p.IP = strings.TrimSpace(p.IP)
	p.Type = strings.TrimSpace(p.Type)
	p.Status = strings.TrimSpace(p.Status)
	if p.Name == "" || p.IP == "" {
		c.JSON(400, gin.H{"error": "硬件名称和IP地址不能为空"})
		return
	}
	if p.Type == "" {
		p.Type = "服务器"
	}
	if p.Status == "" {
		p.Status = "在线"
	}
	if p.Bandwidth == "" {
		p.Bandwidth = "0Mbps"
	}

	// Check IP uniqueness
	var existCount int
	_ = a.db.QueryRow("SELECT COUNT(*) FROM hardware_items WHERE ip = ?", p.IP).Scan(&existCount)
	if existCount > 0 {
		c.JSON(400, gin.H{"error": "该IP地址已存在，不能重复添加"})
		return
	}

	res, err := a.db.Exec(`INSERT INTO hardware_items(name, type, ip, status, description, temperature, cpu_usage, mem_usage, network_bandwidth, detected_at, created_at, updated_at) VALUES(?,?,?,?,?,?,?,?,?,datetime('now'),datetime('now'),datetime('now'))`,
		p.Name, p.Type, p.IP, p.Status, p.Description, p.Temperature, p.CPUUsage, p.MemUsage, p.Bandwidth)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	newId, _ := res.LastInsertId()

	// Auto-probe: try to fetch real data from agent on the target IP
	agentOk, _ := a.updateHardwareFromAgent(int(newId), p.IP)
	c.JSON(200, gin.H{"ok": true, "id": newId, "agent_connected": agentOk})
}

func (a *API) HardwareItemsGet(c *gin.Context) {
	id := c.Param("id")
	var hwId int
	var name, hwType, ip, status, desc, bandwidth, detectedAt, createdAt, updatedAt string
	var temp, cpuUsage, memUsage float64
	err := a.db.QueryRow("SELECT id, name, type, ip, status, description, temperature, cpu_usage, mem_usage, network_bandwidth, detected_at, created_at, updated_at FROM hardware_items WHERE id = ?", id).Scan(
		&hwId, &name, &hwType, &ip, &status, &desc, &temp, &cpuUsage, &memUsage, &bandwidth, &detectedAt, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		c.JSON(404, gin.H{"error": "硬件不存在"})
		return
	}
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{
		"id": hwId, "name": name, "type": hwType, "ip": ip, "status": status,
		"description": desc, "temperature": temp, "cpu_usage": cpuUsage,
		"mem_usage": memUsage, "network_bandwidth": bandwidth,
		"detected_at": detectedAt, "created_at": createdAt, "updated_at": updatedAt,
	})
}

func (a *API) HardwareItemsUpdate(c *gin.Context) {
	id := c.Param("id")
	var p struct {
		Name        *string  `json:"name"`
		Type        *string  `json:"type"`
		IP          *string  `json:"ip"`
		Status      *string  `json:"status"`
		Description *string  `json:"description"`
		Temperature *float64 `json:"temperature"`
		CPUUsage    *float64 `json:"cpu_usage"`
		MemUsage    *float64 `json:"mem_usage"`
		Bandwidth   *string  `json:"network_bandwidth"`
	}
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"error": "参数格式错误"})
		return
	}

	sets := []string{"updated_at = datetime('now')"}
	args := []any{}
	if p.Name != nil {
		sets = append(sets, "name = ?")
		args = append(args, strings.TrimSpace(*p.Name))
	}
	if p.Type != nil {
		sets = append(sets, "type = ?")
		args = append(args, strings.TrimSpace(*p.Type))
	}
	if p.IP != nil {
		newIP := strings.TrimSpace(*p.IP)
		// Check IP uniqueness (exclude current record)
		var ipCount int
		_ = a.db.QueryRow("SELECT COUNT(*) FROM hardware_items WHERE ip = ? AND id != ?", newIP, id).Scan(&ipCount)
		if ipCount > 0 {
			c.JSON(400, gin.H{"error": "该IP地址已被其他硬件使用，不能重复"})
			return
		}
		sets = append(sets, "ip = ?")
		args = append(args, newIP)
	}
	if p.Status != nil {
		sets = append(sets, "status = ?")
		args = append(args, strings.TrimSpace(*p.Status))
	}
	if p.Description != nil {
		sets = append(sets, "description = ?")
		args = append(args, *p.Description)
	}
	if p.Temperature != nil {
		sets = append(sets, "temperature = ?")
		args = append(args, *p.Temperature)
	}
	if p.CPUUsage != nil {
		sets = append(sets, "cpu_usage = ?")
		args = append(args, *p.CPUUsage)
	}
	if p.MemUsage != nil {
		sets = append(sets, "mem_usage = ?")
		args = append(args, *p.MemUsage)
	}
	if p.Bandwidth != nil {
		sets = append(sets, "network_bandwidth = ?")
		args = append(args, *p.Bandwidth)
	}

	args = append(args, id)
	q := "UPDATE hardware_items SET " + strings.Join(sets, ", ") + " WHERE id = ?"
	result, err := a.db.Exec(q, args...)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		c.JSON(404, gin.H{"error": "硬件不存在"})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (a *API) HardwareItemsDelete(c *gin.Context) {
	id := c.Param("id")
	result, err := a.db.Exec("DELETE FROM hardware_items WHERE id = ?", id)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		c.JSON(404, gin.H{"error": "硬件不存在"})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (a *API) HardwareItemsStats(c *gin.Context) {
	// Status distribution
	statusRows, err := a.db.Query("SELECT status, COUNT(*) FROM hardware_items GROUP BY status")
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer statusRows.Close()
	statusDist := gin.H{}
	for statusRows.Next() {
		var s string
		var cnt int
		if err := statusRows.Scan(&s, &cnt); err == nil {
			statusDist[s] = cnt
		}
	}

	// Type distribution
	typeRows, err := a.db.Query("SELECT type, COUNT(*) FROM hardware_items GROUP BY type")
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer typeRows.Close()
	typeDist := gin.H{}
	for typeRows.Next() {
		var t string
		var cnt int
		if err := typeRows.Scan(&t, &cnt); err == nil {
			typeDist[t] = cnt
		}
	}

	var total int
	_ = a.db.QueryRow("SELECT COUNT(*) FROM hardware_items").Scan(&total)

	c.JSON(200, gin.H{"total": total, "status_distribution": statusDist, "type_distribution": typeDist})
}

// agentMetrics represents the JSON response from hw-agent
type agentMetrics struct {
	Hostname         string  `json:"hostname"`
	CPUUsage         float64 `json:"cpu_usage"`
	MemUsage         float64 `json:"mem_usage"`
	Temperature      float64 `json:"temperature"`
	NetworkBandwidth string  `json:"network_bandwidth"`
}

// fetchAgentMetrics connects to the hw-agent running on targetIP:9100 and returns real metrics
func fetchAgentMetrics(targetIP string) (*agentMetrics, error) {
	client := &http.Client{Timeout: 2 * time.Second}
	url := "http://" + targetIP + ":9100/metrics"
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("agent returned status %d", resp.StatusCode)
	}
	var m agentMetrics
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

// updateHardwareFromAgent fetches real metrics from agent and updates the database record
func (a *API) updateHardwareFromAgent(id int, ip string) (bool, string) {
	m, err := fetchAgentMetrics(ip)
	if err != nil {
		// Agent unreachable, mark as offline
		_, _ = a.db.Exec("UPDATE hardware_items SET status = '离线', detected_at = datetime('now'), updated_at = datetime('now') WHERE id = ?", id)
		return false, err.Error()
	}
	// Agent reachable, update with real data
	_, _ = a.db.Exec(`UPDATE hardware_items SET 
		status = '在线', temperature = ?, cpu_usage = ?, mem_usage = ?, network_bandwidth = ?,
		detected_at = datetime('now'), updated_at = datetime('now') WHERE id = ?`,
		m.Temperature, m.CPUUsage, m.MemUsage, m.NetworkBandwidth, id)
	return true, ""
}

func (a *API) HardwareItemsRefresh(c *gin.Context) {
	rows, err := a.db.Query("SELECT id, ip FROM hardware_items")
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	type hw struct {
		id int
		ip string
	}
	var items []hw
	for rows.Next() {
		var h hw
		if err := rows.Scan(&h.id, &h.ip); err == nil {
			items = append(items, h)
		}
	}

	// Concurrent refresh for all devices
	var wg sync.WaitGroup
	var onlineCount, offlineCount int64
	for _, h := range items {
		wg.Add(1)
		go func(id int, ip string) {
			defer wg.Done()
			ok, _ := a.updateHardwareFromAgent(id, ip)
			if ok {
				atomic.AddInt64(&onlineCount, 1)
			} else {
				atomic.AddInt64(&offlineCount, 1)
			}
		}(h.id, h.ip)
	}
	wg.Wait()
	c.JSON(200, gin.H{"ok": true, "refreshed": len(items), "online": onlineCount, "offline": offlineCount})
}

// HardwareItemsRefreshOne refreshes a single hardware item by fetching real data from its agent
func (a *API) HardwareItemsRefreshOne(c *gin.Context) {
	id := c.Param("id")
	var hwId int
	var ip string
	err := a.db.QueryRow("SELECT id, ip FROM hardware_items WHERE id = ?", id).Scan(&hwId, &ip)
	if err == sql.ErrNoRows {
		c.JSON(404, gin.H{"error": "硬件不存在"})
		return
	}
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	ok, errMsg := a.updateHardwareFromAgent(hwId, ip)
	if ok {
		c.JSON(200, gin.H{"ok": true, "status": "在线", "message": "数据已从Agent采集更新"})
	} else {
		c.JSON(200, gin.H{"ok": false, "status": "离线", "message": "Agent不可达: " + errMsg})
	}
}

// HardwareCheckAgent pre-checks if the agent is reachable on the given IP before adding
func (a *API) HardwareCheckAgent(c *gin.Context) {
	var p struct {
		IP string `json:"ip"`
	}
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"ok": false, "message": "参数格式错误"})
		return
	}
	p.IP = strings.TrimSpace(p.IP)
	if p.IP == "" {
		c.JSON(400, gin.H{"ok": false, "message": "IP地址不能为空"})
		return
	}
	m, err := fetchAgentMetrics(p.IP)
	if err != nil {
		c.JSON(200, gin.H{"ok": false, "message": "无法连接到目标设备的Agent（" + p.IP + ":9100），请确保目标设备已运行hw-agent程序且9100端口已放行"})
		return
	}
	// Check if all metrics are zero (invalid agent data)
	if m.CPUUsage == 0 && m.MemUsage == 0 && m.Temperature == 0 && m.NetworkBandwidth == "0B" {
		c.JSON(200, gin.H{"ok": false, "message": "Agent返回的数据全部为零，可能是无效设备"})
		return
	}
	c.JSON(200, gin.H{"ok": true, "message": "Agent连接成功", "metrics": m})
}

// HardwareItemsLive fetches real-time metrics from all agents concurrently and returns
// the merged result directly. This bypasses the DB write→read round-trip for minimal latency.
// DB is updated asynchronously in the background so persistent data stays fresh.
func (a *API) HardwareItemsLive(c *gin.Context) {
	rows, err := a.db.Query("SELECT id, name, type, ip, description FROM hardware_items")
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	type hwItem struct {
		ID          int    `json:"id"`
		Name        string `json:"name"`
		Type        string `json:"type"`
		IP          string `json:"ip"`
		Description string `json:"description"`
	}
	var items []hwItem
	for rows.Next() {
		var h hwItem
		if err := rows.Scan(&h.ID, &h.Name, &h.Type, &h.IP, &h.Description); err == nil {
			items = append(items, h)
		}
	}

	type liveResult struct {
		ID               int     `json:"id"`
		Name             string  `json:"name"`
		Type             string  `json:"type"`
		IP               string  `json:"ip"`
		Status           string  `json:"status"`
		Temperature      float64 `json:"temperature"`
		CPUUsage         float64 `json:"cpu_usage"`
		MemUsage         float64 `json:"mem_usage"`
		NetworkBandwidth string  `json:"network_bandwidth"`
		DetectedAt       string  `json:"detected_at"`
	}

	results := make([]liveResult, len(items))
	var wg sync.WaitGroup
	for i, h := range items {
		wg.Add(1)
		go func(idx int, item hwItem) {
			defer wg.Done()
			r := liveResult{
				ID:   item.ID,
				Name: item.Name,
				Type: item.Type,
				IP:   item.IP,
			}
			m, err := fetchAgentMetrics(item.IP)
			if err != nil {
				r.Status = "离线"
				r.NetworkBandwidth = "0B/s"
				r.DetectedAt = time.Now().UTC().Format("2006-01-02T15:04:05Z")
				// Update DB status asynchronously
				go func() {
					_, _ = a.db.Exec("UPDATE hardware_items SET status='离线', detected_at=datetime('now'), updated_at=datetime('now') WHERE id=?", item.ID)
				}()
			} else {
				r.Status = "在线"
				r.Temperature = m.Temperature
				r.CPUUsage = m.CPUUsage
				r.MemUsage = m.MemUsage
				r.NetworkBandwidth = m.NetworkBandwidth
				r.DetectedAt = time.Now().UTC().Format("2006-01-02T15:04:05Z")
				// Update DB asynchronously
				go func() {
					_, _ = a.db.Exec(`UPDATE hardware_items SET status='在线', temperature=?, cpu_usage=?, mem_usage=?, network_bandwidth=?, detected_at=datetime('now'), updated_at=datetime('now') WHERE id=?`,
						m.Temperature, m.CPUUsage, m.MemUsage, m.NetworkBandwidth, item.ID)
				}()
			}
			results[idx] = r
		}(i, h)
	}
	wg.Wait()
	c.JSON(200, gin.H{"items": results})
}

// HardwarePush receives metrics actively pushed from a remote agent (e.g. drone).
// The agent identifies itself via agent_id. If a matching hardware_item (by agent_id stored in ip field)
// exists, it is updated; otherwise a new record is auto-created.
func (a *API) HardwarePush(c *gin.Context) {
	var p struct {
		AgentID          string  `json:"agent_id"`
		Hostname         string  `json:"hostname"`
		CPUUsage         float64 `json:"cpu_usage"`
		MemUsage         float64 `json:"mem_usage"`
		Temperature      float64 `json:"temperature"`
		NetworkBandwidth string  `json:"network_bandwidth"`
		OS               string  `json:"os"`
		CPUModel         string  `json:"cpu_model"`
		CPUCores         int     `json:"cpu_cores"`
		MemTotalMB       uint64  `json:"mem_total_mb"`
		Uptime           uint64  `json:"uptime"`
		Timestamp        int64   `json:"timestamp"`
	}
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"ok": false, "message": "参数格式错误"})
		return
	}
	if p.AgentID == "" {
		c.JSON(400, gin.H{"ok": false, "message": "agent_id 不能为空"})
		return
	}

	// Try to find existing hardware_item by agent_id (stored in ip field)
	var id int
	err := a.db.QueryRow("SELECT id FROM hardware_items WHERE ip = ?", p.AgentID).Scan(&id)
	if err == sql.ErrNoRows {
		// Auto-create a new hardware item for this agent
		name := p.Hostname
		if name == "" {
			name = p.AgentID
		}
		res, err := a.db.Exec(`INSERT INTO hardware_items(name, type, ip, status, description, temperature, cpu_usage, mem_usage, network_bandwidth, detected_at, created_at, updated_at)
			VALUES(?, '无人机', ?, '在线', ?, ?, ?, ?, ?, datetime('now'), datetime('now'), datetime('now'))`,
			name, p.AgentID,
			fmt.Sprintf("OS:%s CPU:%s Cores:%d Mem:%dMB", p.OS, p.CPUModel, p.CPUCores, p.MemTotalMB),
			p.Temperature, p.CPUUsage, p.MemUsage, p.NetworkBandwidth)
		if err != nil {
			c.JSON(500, gin.H{"ok": false, "message": "创建设备失败: " + err.Error()})
			return
		}
		newID, _ := res.LastInsertId()
		log.Printf("[Push] Auto-created hardware_item id=%d for agent %s (%s)", newID, p.AgentID, name)
		c.JSON(200, gin.H{"ok": true, "message": "设备已自动注册", "id": newID})
		return
	} else if err != nil {
		c.JSON(500, gin.H{"ok": false, "message": err.Error()})
		return
	}

	// Update existing record
	desc := fmt.Sprintf("OS:%s CPU:%s Cores:%d Mem:%dMB", p.OS, p.CPUModel, p.CPUCores, p.MemTotalMB)
	_, err = a.db.Exec(`UPDATE hardware_items SET
		status = '在线', temperature = ?, cpu_usage = ?, mem_usage = ?, network_bandwidth = ?,
		description = ?, detected_at = datetime('now'), updated_at = datetime('now') WHERE id = ?`,
		p.Temperature, p.CPUUsage, p.MemUsage, p.NetworkBandwidth, desc, id)
	if err != nil {
		c.JSON(500, gin.H{"ok": false, "message": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true, "id": id})
}

func (a *API) MetricsSnapshot(c *gin.Context) {
	m, err := monitor.CollectMetrics()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, m)
}

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

func (a *API) MetricsStream(c *gin.Context) {
	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer ws.Close()

	// Read goroutine to detect client disconnect / close frames promptly
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	}()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			m, err := monitor.CollectMetrics()
			if err != nil {
				return
			}
			b, _ := json.Marshal(m)
			if err := ws.WriteMessage(websocket.TextMessage, b); err != nil {
				return
			}
		}
	}
}

func (a *API) HardwareSnapshot(c *gin.Context) {
	hs, err := monitor.HardwareInfo()
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, hs)
}

func saveUploadedFile(fh *multipart.FileHeader, dstDir string) (string, int64, error) {
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return "", 0, err
	}
	f, err := fh.Open()
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	name := time.Now().Format("20060102_150405") + "_" + filepath.Base(fh.Filename)
	path := filepath.Join(dstDir, name)
	out, err := os.Create(path)
	if err != nil {
		return "", 0, err
	}
	defer out.Close()
	n, err := io.Copy(out, f)
	return path, n, err
}

func (a *API) AudioUpload(c *gin.Context) {
	fh, err := c.FormFile("file")
	if err != nil {
		c.JSON(400, gin.H{"error": "missing file"})
		return
	}
	durationStr := c.PostForm("duration")
	var duration float64
	if d, err := strconv.ParseFloat(durationStr, 64); err == nil {
		duration = d
	}
	path, size, err := saveUploadedFile(fh, filepath.Join("data", "recordings"))
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	userId := c.PostForm("user_id")
	interactType := c.PostForm("interact_type")
	content := c.PostForm("content")
	clarity := c.PostForm("clarity")
	result := c.PostForm("result")
	scoreStr := c.PostForm("score")
	score := 4
	if s, err := strconv.Atoi(scoreStr); err == nil {
		score = s
	}
	tags := c.PostForm("tags")
	remark := c.PostForm("remark")
	interactTime := c.PostForm("interact_time")
	res, err := a.db.Exec(`INSERT INTO recordings(filename, mime, duration, size, user_id, interact_type, content, clarity, result, score, tags, remark, interact_time) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		path, fh.Header.Get("Content-Type"), duration, size, userId, interactType, content, clarity, result, score, tags, remark, interactTime)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	newId, _ := res.LastInsertId()
	c.JSON(200, gin.H{"ok": true, "id": newId})
}

func (a *API) AudioAdd(c *gin.Context) {
	var p struct {
		UserId       string `json:"user_id"`
		InteractType string `json:"interact_type"`
		Content      string `json:"content"`
		Clarity      string `json:"clarity"`
		Result       string `json:"result"`
		Score        int    `json:"score"`
		Tags         string `json:"tags"`
		Remark       string `json:"remark"`
		InteractTime string `json:"interact_time"`
	}
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"error": "bad json"})
		return
	}
	if p.Score == 0 {
		p.Score = 4
	}
	res, err := a.db.Exec(`INSERT INTO recordings(filename, mime, duration, size, user_id, interact_type, content, clarity, result, score, tags, remark, interact_time) VALUES('','',0,0,?,?,?,?,?,?,?,?,?)`,
		p.UserId, p.InteractType, p.Content, p.Clarity, p.Result, p.Score, p.Tags, p.Remark, p.InteractTime)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	newId, _ := res.LastInsertId()
	c.JSON(200, gin.H{"ok": true, "id": newId})
}

func (a *API) AudioList(c *gin.Context) {
	pagination := utils.GetPagination(c)

	// Get total count
	var total int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM recordings`).Scan(&total)

	rows, err := a.db.Query(`SELECT id, filename, mime, duration, size, user_id, interact_type, content, clarity, result, score, tags, remark, interact_time, created_at FROM recordings ORDER BY id DESC LIMIT ? OFFSET ?`,
		pagination.PageSize, pagination.Offset)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var items []gin.H
	for rows.Next() {
		var id int
		var filename, mime, userId, iType, content, clarity, result, tags, remark, iTime, created string
		var duration float64
		var size int64
		var score int
		if err := rows.Scan(&id, &filename, &mime, &duration, &size, &userId, &iType, &content, &clarity, &result, &score, &tags, &remark, &iTime, &created); err == nil {
			items = append(items, gin.H{"id": id, "filename": filepath.Base(filename), "mime": mime, "duration": duration, "size": size, "user_id": userId, "interact_type": iType, "content": content, "clarity": clarity, "result": result, "score": score, "tags": tags, "remark": remark, "interact_time": iTime, "created_at": created})
		}
	}
	c.JSON(200, gin.H{
		"items":     items,
		"total":     total,
		"page":      pagination.Page,
		"page_size": pagination.PageSize,
	})
}

func (a *API) AudioDownload(c *gin.Context) {
	id := c.Param("id")
	var filename string
	err := a.db.QueryRow(`SELECT filename FROM recordings WHERE id = ?`, id).Scan(&filename)
	if err != nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	c.FileAttachment(filename, filepath.Base(filename))
}

func (a *API) AudioDelete(c *gin.Context) {
	id := c.Param("id")
	var filename string
	err := a.db.QueryRow(`SELECT filename FROM recordings WHERE id = ?`, id).Scan(&filename)
	if err == sql.ErrNoRows {
		c.JSON(404, gin.H{"error": "recording not found"})
		return
	}
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	// Delete from database
	_, err = a.db.Exec(`DELETE FROM recordings WHERE id = ?`, id)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	// Delete file from disk
	if err := os.Remove(filename); err != nil && !os.IsNotExist(err) {
		// Log error but don't fail the request
		c.JSON(200, gin.H{"ok": true, "warning": "file deletion failed"})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (a *API) AlertsList(c *gin.Context) {
	pagination := utils.GetPagination(c)

	// Filtering
	category := strings.TrimSpace(c.Query("category"))
	severity := strings.TrimSpace(c.Query("severity"))
	priority := strings.TrimSpace(c.Query("priority"))
	status := strings.TrimSpace(c.Query("status"))
	device := strings.TrimSpace(c.Query("device"))
	keyword := strings.TrimSpace(c.Query("keyword"))
	dateFrom := strings.TrimSpace(c.Query("date_from"))
	dateTo := strings.TrimSpace(c.Query("date_to"))

	where := []string{"1=1"}
	args := []any{}
	if category != "" && category != "全部" {
		where = append(where, "category = ?")
		args = append(args, category)
	}
	if severity != "" && severity != "全部" {
		where = append(where, "severity = ?")
		args = append(args, severity)
	}
	if priority != "" && priority != "全部" {
		where = append(where, "priority = ?")
		args = append(args, priority)
	}
	if status != "" && status != "全部" {
		where = append(where, "status = ?")
		args = append(args, status)
	}
	if device != "" {
		where = append(where, "LOWER(device) LIKE LOWER(?)")
		args = append(args, "%"+device+"%")
	}
	if keyword != "" {
		where = append(where, "(LOWER(message) LIKE LOWER(?) OR LOWER(description) LIKE LOWER(?))")
		args = append(args, "%"+keyword+"%", "%"+keyword+"%")
	}
	if dateFrom != "" {
		where = append(where, "alert_time >= ?")
		args = append(args, dateFrom)
	}
	if dateTo != "" {
		where = append(where, "alert_time <= ?")
		args = append(args, dateTo)
	}

	whereClause := strings.Join(where, " AND ")

	var total int
	_ = a.db.QueryRow("SELECT COUNT(*) FROM alerts WHERE "+whereClause, args...).Scan(&total)

	queryArgs := append(args, pagination.PageSize, pagination.Offset)
	rows, err := a.db.Query("SELECT id, category, severity, message, acknowledged, created_at, alert_time, priority, device, description, status FROM alerts WHERE "+whereClause+" ORDER BY id DESC LIMIT ? OFFSET ?", queryArgs...)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var items []gin.H
	for rows.Next() {
		var id, ack int
		var cat, sev, msg, created string
		var alertTime, pri, dev, desc, st sql.NullString
		if err := rows.Scan(&id, &cat, &sev, &msg, &ack, &created, &alertTime, &pri, &dev, &desc, &st); err == nil {
			items = append(items, gin.H{
				"id": id, "category": cat, "severity": sev, "message": msg,
				"acknowledged": ack == 1, "created_at": created,
				"alert_time": alertTime.String, "priority": pri.String,
				"device": dev.String, "description": desc.String, "status": st.String,
			})
		}
	}
	c.JSON(200, gin.H{
		"items":     items,
		"total":     total,
		"page":      pagination.Page,
		"page_size": pagination.PageSize,
	})
}

func (a *API) AlertAck(c *gin.Context) {
	id := c.Param("id")
	_, err := a.db.Exec(`UPDATE alerts SET acknowledged = 1 WHERE id = ?`, id)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (a *API) AlertNew(c *gin.Context) {
	var p struct {
		Category    string `json:"category"`
		Severity    string `json:"severity"`
		Message     string `json:"message"`
		AlertTime   string `json:"alert_time"`
		Priority    string `json:"priority"`
		Device      string `json:"device"`
		Description string `json:"description"`
		Status      string `json:"status"`
	}
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"error": "bad json"})
		return
	}
	if p.Priority == "" {
		p.Priority = "中"
	}
	if p.Status == "" {
		p.Status = "未解决"
	}
	if p.AlertTime == "" {
		p.AlertTime = time.Now().Format("2006-01-02 15:04")
	}
	_, err := a.db.Exec(`INSERT INTO alerts(category, severity, message, alert_time, priority, device, description, status) VALUES(?,?,?,?,?,?,?,?)`,
		p.Category, p.Severity, p.Message, p.AlertTime, p.Priority, p.Device, p.Description, p.Status)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (a *API) AlertsImport(c *gin.Context) {
	var items []struct {
		Category    string `json:"category"`
		Severity    string `json:"severity"`
		Message     string `json:"message"`
		AlertTime   string `json:"alert_time"`
		Priority    string `json:"priority"`
		Device      string `json:"device"`
		Description string `json:"description"`
		Status      string `json:"status"`
	}
	if err := c.BindJSON(&items); err != nil {
		c.JSON(400, gin.H{"error": "参数格式错误，需要JSON数组"})
		return
	}
	imported := 0
	for _, p := range items {
		if p.Priority == "" {
			p.Priority = "中"
		}
		if p.Status == "" {
			p.Status = "未解决"
		}
		if p.AlertTime == "" {
			p.AlertTime = time.Now().Format("2006-01-02 15:04")
		}
		_, err := a.db.Exec(`INSERT INTO alerts(category, severity, message, alert_time, priority, device, description, status) VALUES(?,?,?,?,?,?,?,?)`,
			p.Category, p.Severity, p.Message, p.AlertTime, p.Priority, p.Device, p.Description, p.Status)
		if err == nil {
			imported++
		}
	}
	c.JSON(200, gin.H{"ok": true, "imported": imported})
}

func (a *API) AlertsClearResolved(c *gin.Context) {
	result, err := a.db.Exec(`DELETE FROM alerts WHERE status = '已解决'`)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	affected, _ := result.RowsAffected()
	c.JSON(200, gin.H{"ok": true, "deleted": affected})
}

func (a *API) AlertsStats(c *gin.Context) {
	// Category distribution
	catRows, err := a.db.Query("SELECT category, COUNT(*) FROM alerts GROUP BY category")
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer catRows.Close()
	catDist := gin.H{}
	for catRows.Next() {
		var cat string
		var cnt int
		if err := catRows.Scan(&cat, &cnt); err == nil {
			catDist[cat] = cnt
		}
	}
	// Priority distribution
	priRows, err := a.db.Query("SELECT priority, COUNT(*) FROM alerts GROUP BY priority")
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer priRows.Close()
	priDist := gin.H{}
	for priRows.Next() {
		var pri string
		var cnt int
		if err := priRows.Scan(&pri, &cnt); err == nil {
			priDist[pri] = cnt
		}
	}
	var total int
	_ = a.db.QueryRow("SELECT COUNT(*) FROM alerts").Scan(&total)
	var resolved int
	_ = a.db.QueryRow("SELECT COUNT(*) FROM alerts WHERE status = '已解决'").Scan(&resolved)
	var unresolved int
	_ = a.db.QueryRow("SELECT COUNT(*) FROM alerts WHERE status != '已解决'").Scan(&unresolved)
	c.JSON(200, gin.H{
		"total":                 total,
		"resolved":              resolved,
		"unresolved":            unresolved,
		"category_distribution": catDist,
		"priority_distribution": priDist,
	})
}

func (a *API) AlertResolve(c *gin.Context) {
	id := c.Param("id")
	_, err := a.db.Exec(`UPDATE alerts SET status = '已解决', acknowledged = 1 WHERE id = ?`, id)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (a *API) LogAppend(c *gin.Context) {
	var p struct {
		Level      string `json:"level"`
		Message    string `json:"message"`
		Meta       string `json:"meta"`
		OpType     string `json:"op_type"`
		Operator   string `json:"operator"`
		OpTime     string `json:"op_time"`
		OpResult   string `json:"op_result"`
		DeviceName string `json:"device_name"`
		LogStatus  string `json:"log_status"`
		Detail     string `json:"detail"`
	}
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"error": "bad json"})
		return
	}
	if p.LogStatus == "" {
		p.LogStatus = "启用"
	}
	if p.OpTime == "" {
		p.OpTime = time.Now().Format("2006-01-02 15:04")
	}
	_, err := a.db.Exec(`INSERT INTO logs(level, message, meta, op_type, operator, op_time, op_result, device_name, log_status, detail) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		p.Level, p.Message, p.Meta, p.OpType, p.Operator, p.OpTime, p.OpResult, p.DeviceName, p.LogStatus, p.Detail)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (a *API) LogList(c *gin.Context) {
	pagination := utils.GetPagination(c)

	opType := strings.TrimSpace(c.Query("op_type"))
	operator := strings.TrimSpace(c.Query("operator"))
	dateFrom := strings.TrimSpace(c.Query("date_from"))
	dateTo := strings.TrimSpace(c.Query("date_to"))
	opResult := strings.TrimSpace(c.Query("op_result"))
	deviceName := strings.TrimSpace(c.Query("device_name"))
	logStatus := strings.TrimSpace(c.Query("log_status"))

	where := []string{"1=1"}
	args := []any{}
	if opType != "" && opType != "全部" {
		where = append(where, "op_type = ?")
		args = append(args, opType)
	}
	if operator != "" {
		where = append(where, "LOWER(operator) LIKE LOWER(?)")
		args = append(args, "%"+operator+"%")
	}
	if dateFrom != "" {
		where = append(where, "op_time >= ?")
		args = append(args, dateFrom)
	}
	if dateTo != "" {
		where = append(where, "op_time <= ?")
		args = append(args, dateTo)
	}
	if opResult != "" && opResult != "全部" {
		where = append(where, "op_result = ?")
		args = append(args, opResult)
	}
	if deviceName != "" {
		where = append(where, "LOWER(device_name) LIKE LOWER(?)")
		args = append(args, "%"+deviceName+"%")
	}
	if logStatus == "启用" || logStatus == "禁用" {
		where = append(where, "log_status = ?")
		args = append(args, logStatus)
	}

	whereClause := strings.Join(where, " AND ")

	var total int
	_ = a.db.QueryRow("SELECT COUNT(*) FROM logs WHERE "+whereClause, args...).Scan(&total)

	queryArgs := append(args, pagination.PageSize, pagination.Offset)
	rows, err := a.db.Query("SELECT id, level, message, meta, created_at, op_type, operator, op_time, op_result, device_name, log_status, detail FROM logs WHERE "+whereClause+" ORDER BY id DESC LIMIT ? OFFSET ?", queryArgs...)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var items []gin.H
	for rows.Next() {
		var id int
		var level, message, meta, created string
		var opT, oper, opTm, opRes, devN, logSt, det sql.NullString
		if err := rows.Scan(&id, &level, &message, &meta, &created, &opT, &oper, &opTm, &opRes, &devN, &logSt, &det); err == nil {
			items = append(items, gin.H{
				"id": id, "level": level, "message": message, "meta": meta, "created_at": created,
				"op_type": opT.String, "operator": oper.String, "op_time": opTm.String,
				"op_result": opRes.String, "device_name": devN.String,
				"log_status": logSt.String, "detail": det.String,
			})
		}
	}
	c.JSON(200, gin.H{"items": items, "total": total, "page": pagination.Page, "page_size": pagination.PageSize})
}

func (a *API) LogEdit(c *gin.Context) {
	id := c.Param("id")
	var p struct {
		OpType     string `json:"op_type"`
		Operator   string `json:"operator"`
		OpTime     string `json:"op_time"`
		OpResult   string `json:"op_result"`
		DeviceName string `json:"device_name"`
		LogStatus  string `json:"log_status"`
		Detail     string `json:"detail"`
	}
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"error": "参数格式错误"})
		return
	}
	_, err := a.db.Exec(`UPDATE logs SET op_type=?, operator=?, op_time=?, op_result=?, device_name=?, log_status=?, detail=? WHERE id=?`,
		p.OpType, p.Operator, p.OpTime, p.OpResult, p.DeviceName, p.LogStatus, p.Detail, id)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (a *API) LogDelete(c *gin.Context) {
	id := c.Param("id")
	result, err := a.db.Exec("DELETE FROM logs WHERE id = ?", id)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		c.JSON(404, gin.H{"error": "记录不存在"})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (a *API) LogImport(c *gin.Context) {
	var items []struct {
		OpType     string `json:"op_type"`
		Operator   string `json:"operator"`
		OpTime     string `json:"op_time"`
		OpResult   string `json:"op_result"`
		DeviceName string `json:"device_name"`
		LogStatus  string `json:"log_status"`
		Detail     string `json:"detail"`
	}
	if err := c.BindJSON(&items); err != nil {
		c.JSON(400, gin.H{"error": "参数格式错误，需要JSON数组"})
		return
	}
	imported := 0
	for _, p := range items {
		if p.LogStatus == "" {
			p.LogStatus = "启用"
		}
		if p.OpTime == "" {
			p.OpTime = time.Now().Format("2006-01-02 15:04")
		}
		_, err := a.db.Exec(`INSERT INTO logs(level, message, meta, op_type, operator, op_time, op_result, device_name, log_status, detail) VALUES('info','','',?,?,?,?,?,?,?)`,
			p.OpType, p.Operator, p.OpTime, p.OpResult, p.DeviceName, p.LogStatus, p.Detail)
		if err == nil {
			imported++
		}
	}
	c.JSON(200, gin.H{"ok": true, "imported": imported})
}

func (a *API) LogStats(c *gin.Context) {
	// Op type distribution
	typeRows, err := a.db.Query("SELECT op_type, COUNT(*) FROM logs WHERE op_type != '' GROUP BY op_type")
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer typeRows.Close()
	typeDist := gin.H{}
	for typeRows.Next() {
		var t string
		var cnt int
		if err := typeRows.Scan(&t, &cnt); err == nil && t != "" {
			typeDist[t] = cnt
		}
	}
	// Op result distribution
	resRows, err := a.db.Query("SELECT op_result, COUNT(*) FROM logs WHERE op_result != '' GROUP BY op_result")
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer resRows.Close()
	resDist := gin.H{}
	for resRows.Next() {
		var r string
		var cnt int
		if err := resRows.Scan(&r, &cnt); err == nil && r != "" {
			resDist[r] = cnt
		}
	}
	var total int
	_ = a.db.QueryRow("SELECT COUNT(*) FROM logs").Scan(&total)
	c.JSON(200, gin.H{"total": total, "type_distribution": typeDist, "result_distribution": resDist})
}

// ==================== Software Updates API ====================

func (a *API) updatesReadRow(rows *sql.Rows) (gin.H, error) {
	var id, autoUpdate, forceUpdate int
	var name, version, desc, status, uType, size, publishDate, createdAt sql.NullString
	var updatedAt sql.NullString
	if err := rows.Scan(&id, &name, &version, &desc, &status, &uType, &size, &autoUpdate, &forceUpdate, &publishDate, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	return gin.H{
		"id": id, "name": name.String, "version": version.String, "description": desc.String,
		"status": status.String, "type": uType.String, "size": size.String,
		"auto_update": autoUpdate == 1, "force_update": forceUpdate == 1,
		"publish_date": publishDate.String, "created_at": createdAt.String, "updated_at": updatedAt.String,
	}, nil
}

func (a *API) UpdatesList(c *gin.Context) {
	// Filtering
	name := strings.TrimSpace(c.Query("name"))
	version := strings.TrimSpace(c.Query("version"))
	uType := strings.TrimSpace(c.Query("type"))
	size := strings.TrimSpace(c.Query("size"))
	status := strings.TrimSpace(c.Query("status"))
	publishDate := strings.TrimSpace(c.Query("publish_date"))
	autoUpdate := strings.TrimSpace(c.Query("auto_update"))
	forceUpdate := strings.TrimSpace(c.Query("force_update"))

	where := []string{"1=1"}
	args := []any{}
	if name != "" {
		where = append(where, "LOWER(name) LIKE LOWER(?)")
		args = append(args, "%"+name+"%")
	}
	if version != "" {
		where = append(where, "LOWER(version) LIKE LOWER(?)")
		args = append(args, "%"+version+"%")
	}
	if uType != "" && uType != "全部" {
		where = append(where, "type = ?")
		args = append(args, uType)
	}
	if size != "" {
		where = append(where, "LOWER(size) LIKE LOWER(?)")
		args = append(args, "%"+size+"%")
	}
	if status != "" && status != "全部" {
		where = append(where, "status = ?")
		args = append(args, status)
	}
	if publishDate != "" {
		where = append(where, "publish_date >= ?")
		args = append(args, publishDate)
	}
	if autoUpdate == "1" {
		where = append(where, "auto_update = 1")
	} else if autoUpdate == "0" {
		where = append(where, "auto_update = 0")
	}
	if forceUpdate == "1" {
		where = append(where, "force_update = 1")
	} else if forceUpdate == "0" {
		where = append(where, "force_update = 0")
	}

	whereClause := strings.Join(where, " AND ")
	var total int
	_ = a.db.QueryRow("SELECT COUNT(*) FROM updates WHERE "+whereClause, args...).Scan(&total)

	pagination := utils.GetPagination(c)
	queryArgs := append(args, pagination.PageSize, pagination.Offset)

	rows, err := a.db.Query("SELECT id, name, version, description, status, type, size, auto_update, force_update, publish_date, created_at, updated_at FROM updates WHERE "+whereClause+" ORDER BY id DESC LIMIT ? OFFSET ?", queryArgs...)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var items []gin.H
	for rows.Next() {
		if item, err := a.updatesReadRow(rows); err == nil {
			items = append(items, item)
		}
	}
	c.JSON(200, gin.H{"items": items, "total": total, "page": pagination.Page, "page_size": pagination.PageSize})
}

func (a *API) UpdatesAdd(c *gin.Context) {
	var p struct {
		Name        string `json:"name"`
		Version     string `json:"version"`
		Description string `json:"description"`
		Status      string `json:"status"`
		Type        string `json:"type"`
		Size        string `json:"size"`
		AutoUpdate  bool   `json:"auto_update"`
		ForceUpdate bool   `json:"force_update"`
		PublishDate string `json:"publish_date"`
	}
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"error": "参数格式错误"})
		return
	}
	p.Name = strings.TrimSpace(p.Name)
	p.Version = strings.TrimSpace(p.Version)
	if p.Name == "" {
		c.JSON(400, gin.H{"error": "更新名称不能为空"})
		return
	}
	if p.Version == "" {
		c.JSON(400, gin.H{"error": "版本号不能为空"})
		return
	}
	if p.Status == "" {
		p.Status = "待发布"
	}
	if p.Type == "" {
		p.Type = "功能更新"
	}
	au := 0
	if p.AutoUpdate {
		au = 1
	}
	fu := 0
	if p.ForceUpdate {
		fu = 1
	}
	res, err := a.db.Exec(`INSERT INTO updates(name, version, description, status, type, size, auto_update, force_update, publish_date, created_at, updated_at) VALUES(?,?,?,?,?,?,?,?,?,datetime('now'),datetime('now'))`,
		p.Name, p.Version, p.Description, p.Status, p.Type, p.Size, au, fu, p.PublishDate)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	newId, _ := res.LastInsertId()
	c.JSON(200, gin.H{"ok": true, "id": newId})
}

func (a *API) UpdatesEdit(c *gin.Context) {
	id := c.Param("id")
	var p struct {
		Name        *string `json:"name"`
		Version     *string `json:"version"`
		Description *string `json:"description"`
		Status      *string `json:"status"`
		Type        *string `json:"type"`
		Size        *string `json:"size"`
		AutoUpdate  *bool   `json:"auto_update"`
		ForceUpdate *bool   `json:"force_update"`
		PublishDate *string `json:"publish_date"`
	}
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"error": "参数格式错误"})
		return
	}
	sets := []string{"updated_at = datetime('now')"}
	args := []any{}
	if p.Name != nil {
		sets = append(sets, "name = ?")
		args = append(args, strings.TrimSpace(*p.Name))
	}
	if p.Version != nil {
		sets = append(sets, "version = ?")
		args = append(args, strings.TrimSpace(*p.Version))
	}
	if p.Description != nil {
		sets = append(sets, "description = ?")
		args = append(args, *p.Description)
	}
	if p.Status != nil {
		sets = append(sets, "status = ?")
		args = append(args, strings.TrimSpace(*p.Status))
	}
	if p.Type != nil {
		sets = append(sets, "type = ?")
		args = append(args, strings.TrimSpace(*p.Type))
	}
	if p.Size != nil {
		sets = append(sets, "size = ?")
		args = append(args, strings.TrimSpace(*p.Size))
	}
	if p.AutoUpdate != nil {
		v := 0
		if *p.AutoUpdate {
			v = 1
		}
		sets = append(sets, "auto_update = ?")
		args = append(args, v)
	}
	if p.ForceUpdate != nil {
		v := 0
		if *p.ForceUpdate {
			v = 1
		}
		sets = append(sets, "force_update = ?")
		args = append(args, v)
	}
	if p.PublishDate != nil {
		sets = append(sets, "publish_date = ?")
		args = append(args, *p.PublishDate)
	}
	args = append(args, id)
	result, err := a.db.Exec("UPDATE updates SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		c.JSON(404, gin.H{"error": "更新记录不存在"})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (a *API) UpdatesDelete(c *gin.Context) {
	id := c.Param("id")
	result, err := a.db.Exec("DELETE FROM updates WHERE id = ?", id)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		c.JSON(404, gin.H{"error": "更新记录不存在"})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (a *API) UpdatesImport(c *gin.Context) {
	var items []struct {
		Name        string `json:"name"`
		Version     string `json:"version"`
		Description string `json:"description"`
		Status      string `json:"status"`
		Type        string `json:"type"`
		Size        string `json:"size"`
		AutoUpdate  bool   `json:"auto_update"`
		ForceUpdate bool   `json:"force_update"`
		PublishDate string `json:"publish_date"`
	}
	if err := c.BindJSON(&items); err != nil {
		c.JSON(400, gin.H{"error": "参数格式错误，需要JSON数组"})
		return
	}
	imported := 0
	for _, p := range items {
		p.Name = strings.TrimSpace(p.Name)
		p.Version = strings.TrimSpace(p.Version)
		if p.Name == "" || p.Version == "" {
			continue
		}
		if p.Status == "" {
			p.Status = "待发布"
		}
		if p.Type == "" {
			p.Type = "功能更新"
		}
		au := 0
		if p.AutoUpdate {
			au = 1
		}
		fu := 0
		if p.ForceUpdate {
			fu = 1
		}
		_, err := a.db.Exec(`INSERT INTO updates(name, version, description, status, type, size, auto_update, force_update, publish_date, created_at, updated_at) VALUES(?,?,?,?,?,?,?,?,?,datetime('now'),datetime('now'))`,
			p.Name, p.Version, p.Description, p.Status, p.Type, p.Size, au, fu, p.PublishDate)
		if err == nil {
			imported++
		}
	}
	c.JSON(200, gin.H{"ok": true, "imported": imported})
}

func (a *API) UpdatesStats(c *gin.Context) {
	// Status distribution
	statusRows, err := a.db.Query("SELECT status, COUNT(*) FROM updates GROUP BY status")
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer statusRows.Close()
	statusDist := gin.H{}
	for statusRows.Next() {
		var s string
		var cnt int
		if err := statusRows.Scan(&s, &cnt); err == nil {
			statusDist[s] = cnt
		}
	}
	// Type distribution
	typeRows, err := a.db.Query("SELECT type, COUNT(*) FROM updates GROUP BY type")
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer typeRows.Close()
	typeDist := gin.H{}
	for typeRows.Next() {
		var t string
		var cnt int
		if err := typeRows.Scan(&t, &cnt); err == nil {
			typeDist[t] = cnt
		}
	}
	var total int
	_ = a.db.QueryRow("SELECT COUNT(*) FROM updates").Scan(&total)
	c.JSON(200, gin.H{"total": total, "status_distribution": statusDist, "type_distribution": typeDist})
}

func (a *API) UpdatesCheck(c *gin.Context) {
	currentVersion := strings.TrimSpace(c.Query("current"))
	if currentVersion == "" {
		currentVersion = "0.0.0"
	}
	var latestVersion sql.NullString
	err := a.db.QueryRow(`SELECT version FROM updates ORDER BY id DESC LIMIT 1`).Scan(&latestVersion)
	if err != nil || !latestVersion.Valid || latestVersion.String == "" {
		c.JSON(200, gin.H{"latest_version": currentVersion, "has_update": false})
		return
	}
	hasUpdate := latestVersion.String != currentVersion
	c.JSON(200, gin.H{"latest_version": latestVersion.String, "has_update": hasUpdate})
}

func (a *API) SyncStatusGet(c *gin.Context) {
	var status, message, last, updated sql.NullString
	row := a.db.QueryRow(`SELECT status, message, last_synced_at, updated_at FROM sync_status WHERE id = 1`)
	_ = row.Scan(&status, &message, &last, &updated)
	c.JSON(200, gin.H{"status": status.String, "message": message.String, "last_synced_at": last.String, "updated_at": updated.String})
}

func (a *API) SyncStatusSet(c *gin.Context) {
	var p struct{ Status, Message string }
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"error": "bad json"})
		return
	}
	syncNow := time.Now().Format("2006-01-02 15:04:05")
	_, err := a.db.Exec(`UPDATE sync_status SET status=?, message=?, last_synced_at=?, updated_at=? WHERE id = 1`, p.Status, p.Message, syncNow, syncNow)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// ==================== Sync Tasks API ====================

func (a *API) syncTaskReadRow(rows *sql.Rows) (gin.H, error) {
	var id, syncStatusEnabled, logEnabled, progress, syncedData, totalData int
	var source, target, frequency, mode, startTime, endTime, status sql.NullString
	var successRate, avgDuration float64
	var lastSyncedAt, createdAt, updatedAt sql.NullString
	if err := rows.Scan(&id, &source, &target, &frequency, &mode, &startTime, &endTime, &status,
		&syncStatusEnabled, &logEnabled, &progress, &syncedData, &totalData,
		&successRate, &avgDuration, &lastSyncedAt, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	return gin.H{
		"id": id, "source": source.String, "target": target.String,
		"frequency": frequency.String, "mode": mode.String,
		"start_time": startTime.String, "end_time": endTime.String,
		"status": status.String, "sync_status_enabled": syncStatusEnabled == 1,
		"log_enabled": logEnabled == 1, "progress": progress,
		"synced_data": syncedData, "total_data": totalData,
		"success_rate": successRate, "avg_duration": avgDuration,
		"last_synced_at": lastSyncedAt.String, "created_at": createdAt.String, "updated_at": updatedAt.String,
	}, nil
}

func (a *API) SyncTasksList(c *gin.Context) {
	source := strings.TrimSpace(c.Query("source"))
	target := strings.TrimSpace(c.Query("target"))
	frequency := strings.TrimSpace(c.Query("frequency"))
	mode := strings.TrimSpace(c.Query("mode"))
	status := strings.TrimSpace(c.Query("status"))
	startTime := strings.TrimSpace(c.Query("start_time"))
	endTime := strings.TrimSpace(c.Query("end_time"))
	syncStatusEnabled := strings.TrimSpace(c.Query("sync_status_enabled"))
	logEnabled := strings.TrimSpace(c.Query("log_enabled"))

	where := []string{"1=1"}
	args := []any{}
	if source != "" {
		where = append(where, "LOWER(source) LIKE LOWER(?)")
		args = append(args, "%"+source+"%")
	}
	if target != "" {
		where = append(where, "LOWER(target) LIKE LOWER(?)")
		args = append(args, "%"+target+"%")
	}
	if frequency != "" && frequency != "全部" {
		where = append(where, "frequency = ?")
		args = append(args, frequency)
	}
	if mode != "" && mode != "全部" {
		where = append(where, "mode = ?")
		args = append(args, mode)
	}
	if status != "" && status != "全部" {
		where = append(where, "status = ?")
		args = append(args, status)
	}
	if startTime != "" {
		where = append(where, "start_time >= ?")
		args = append(args, startTime)
	}
	if endTime != "" {
		where = append(where, "end_time <= ?")
		args = append(args, endTime)
	}
	if syncStatusEnabled == "1" {
		where = append(where, "sync_status_enabled = 1")
	} else if syncStatusEnabled == "0" {
		where = append(where, "sync_status_enabled = 0")
	}
	if logEnabled == "1" {
		where = append(where, "log_enabled = 1")
	} else if logEnabled == "0" {
		where = append(where, "log_enabled = 0")
	}

	whereClause := strings.Join(where, " AND ")
	var total int
	_ = a.db.QueryRow("SELECT COUNT(*) FROM sync_tasks WHERE "+whereClause, args...).Scan(&total)

	pagination := utils.GetPagination(c)
	queryArgs := append(args, pagination.PageSize, pagination.Offset)

	rows, err := a.db.Query("SELECT id, source, target, frequency, mode, start_time, end_time, status, sync_status_enabled, log_enabled, progress, synced_data, total_data, success_rate, avg_duration, last_synced_at, created_at, updated_at FROM sync_tasks WHERE "+whereClause+" ORDER BY id DESC LIMIT ? OFFSET ?", queryArgs...)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var items []gin.H
	for rows.Next() {
		if item, err := a.syncTaskReadRow(rows); err == nil {
			items = append(items, item)
		}
	}
	c.JSON(200, gin.H{"items": items, "total": total, "page": pagination.Page, "page_size": pagination.PageSize})
}

func (a *API) SyncTasksCreate(c *gin.Context) {
	var p struct {
		Source            string `json:"source"`
		Target            string `json:"target"`
		Frequency         string `json:"frequency"`
		Mode              string `json:"mode"`
		StartTime         string `json:"start_time"`
		EndTime           string `json:"end_time"`
		Status            string `json:"status"`
		SyncStatusEnabled bool   `json:"sync_status_enabled"`
		LogEnabled        bool   `json:"log_enabled"`
	}
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"error": "参数格式错误"})
		return
	}
	p.Source = strings.TrimSpace(p.Source)
	p.Target = strings.TrimSpace(p.Target)
	if p.Source == "" {
		c.JSON(400, gin.H{"error": "数据源不能为空"})
		return
	}
	if p.Target == "" {
		c.JSON(400, gin.H{"error": "目标地址不能为空"})
		return
	}
	if p.Source == p.Target {
		c.JSON(400, gin.H{"error": "数据源和目标地址不能相同"})
		return
	}
	// Validate start_time < end_time if both provided
	if p.StartTime != "" && p.EndTime != "" {
		if p.StartTime >= p.EndTime {
			c.JSON(400, gin.H{"error": "开始时间必须小于结束时间"})
			return
		}
		// Validate that the time interval is at least one sync frequency
		freqDur := syncengine.ParseFrequencyDuration(p.Frequency)
		startT, errS := time.Parse("2006-01-02 15:04", p.StartTime)
		endT, errE := time.Parse("2006-01-02 15:04", p.EndTime)
		if errS == nil && errE == nil {
			if endT.Sub(startT) < freqDur {
				c.JSON(400, gin.H{"error": fmt.Sprintf("开始时间和结束时间的间隔必须大于同步频率（%s）", p.Frequency)})
				return
			}
		}
	}
	if p.Frequency == "" {
		p.Frequency = "5分钟"
	}
	if p.Mode == "" {
		p.Mode = "全量同步"
	}
	if p.Status == "" {
		p.Status = "待启动"
	}
	totalTables := len(syncengine.SyncableTables)
	sse := 0
	if p.SyncStatusEnabled {
		sse = 1
	}
	le := 0
	if p.LogEnabled {
		le = 1
	}
	nowLocal := time.Now().Format("2006-01-02 15:04:05")
	res, err := a.db.Exec(`INSERT INTO sync_tasks(source, target, frequency, mode, start_time, end_time, status, sync_status_enabled, log_enabled, total_data, created_at, updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.Source, p.Target, p.Frequency, p.Mode, p.StartTime, p.EndTime, p.Status, sse, le, totalTables, nowLocal, nowLocal)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	newId, _ := res.LastInsertId()
	c.JSON(200, gin.H{"ok": true, "id": newId})
}

func (a *API) SyncTasksEdit(c *gin.Context) {
	id := c.Param("id")
	var p struct {
		Source            *string  `json:"source"`
		Target            *string  `json:"target"`
		Frequency         *string  `json:"frequency"`
		Mode              *string  `json:"mode"`
		StartTime         *string  `json:"start_time"`
		EndTime           *string  `json:"end_time"`
		Status            *string  `json:"status"`
		SyncStatusEnabled *bool    `json:"sync_status_enabled"`
		LogEnabled        *bool    `json:"log_enabled"`
		Progress          *int     `json:"progress"`
		SyncedData        *int     `json:"synced_data"`
		TotalData         *int     `json:"total_data"`
		SuccessRate       *float64 `json:"success_rate"`
		AvgDuration       *float64 `json:"avg_duration"`
	}
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"error": "参数格式错误"})
		return
	}
	nowLocal := time.Now().Format("2006-01-02 15:04:05")
	sets := []string{"updated_at = ?"}
	args := []any{nowLocal}
	if p.Source != nil {
		sets = append(sets, "source = ?")
		args = append(args, strings.TrimSpace(*p.Source))
	}
	if p.Target != nil {
		sets = append(sets, "target = ?")
		args = append(args, strings.TrimSpace(*p.Target))
	}
	if p.Frequency != nil {
		sets = append(sets, "frequency = ?")
		args = append(args, *p.Frequency)
	}
	if p.Mode != nil {
		sets = append(sets, "mode = ?")
		args = append(args, *p.Mode)
	}
	if p.StartTime != nil {
		sets = append(sets, "start_time = ?")
		args = append(args, *p.StartTime)
	}
	if p.EndTime != nil {
		sets = append(sets, "end_time = ?")
		args = append(args, *p.EndTime)
	}
	// Validate start_time < end_time when both are being set
	if p.StartTime != nil && p.EndTime != nil && *p.StartTime != "" && *p.EndTime != "" {
		if *p.StartTime >= *p.EndTime {
			c.JSON(400, gin.H{"error": "开始时间必须小于结束时间"})
			return
		}
	}
	if p.Status != nil {
		sets = append(sets, "status = ?")
		args = append(args, *p.Status)
		if *p.Status == "运行中" {
			sets = append(sets, "last_synced_at = ?")
			args = append(args, nowLocal)
		}
	}
	if p.SyncStatusEnabled != nil {
		v := 0
		if *p.SyncStatusEnabled {
			v = 1
		}
		sets = append(sets, "sync_status_enabled = ?")
		args = append(args, v)
	}
	if p.LogEnabled != nil {
		v := 0
		if *p.LogEnabled {
			v = 1
		}
		sets = append(sets, "log_enabled = ?")
		args = append(args, v)
	}
	if p.Progress != nil {
		sets = append(sets, "progress = ?")
		args = append(args, *p.Progress)
	}
	if p.SyncedData != nil {
		sets = append(sets, "synced_data = ?")
		args = append(args, *p.SyncedData)
	}
	if p.TotalData != nil {
		sets = append(sets, "total_data = ?")
		args = append(args, *p.TotalData)
	}
	if p.SuccessRate != nil {
		sets = append(sets, "success_rate = ?")
		args = append(args, *p.SuccessRate)
	}
	if p.AvgDuration != nil {
		sets = append(sets, "avg_duration = ?")
		args = append(args, *p.AvgDuration)
	}
	args = append(args, id)
	result, err := a.db.Exec("UPDATE sync_tasks SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		c.JSON(404, gin.H{"error": "同步任务不存在"})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (a *API) SyncTasksDelete(c *gin.Context) {
	id := c.Param("id")
	result, err := a.db.Exec("DELETE FROM sync_tasks WHERE id = ?", id)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		c.JSON(404, gin.H{"error": "同步任务不存在"})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (a *API) SyncTasksImport(c *gin.Context) {
	var items []struct {
		Source            string `json:"source"`
		Target            string `json:"target"`
		Frequency         string `json:"frequency"`
		Mode              string `json:"mode"`
		StartTime         string `json:"start_time"`
		EndTime           string `json:"end_time"`
		Status            string `json:"status"`
		SyncStatusEnabled bool   `json:"sync_status_enabled"`
		LogEnabled        bool   `json:"log_enabled"`
		TotalData         int    `json:"total_data"`
	}
	if err := c.BindJSON(&items); err != nil {
		c.JSON(400, gin.H{"error": "参数格式错误，需要JSON数组"})
		return
	}
	imported := 0
	for _, p := range items {
		p.Source = strings.TrimSpace(p.Source)
		p.Target = strings.TrimSpace(p.Target)
		if p.Source == "" || p.Target == "" {
			continue
		}
		if p.Frequency == "" {
			p.Frequency = "5分钟"
		}
		if p.Mode == "" {
			p.Mode = "全量同步"
		}
		if p.Status == "" {
			p.Status = "待启动"
		}
		if p.TotalData <= 0 {
			p.TotalData = 1000
		}
		sse := 0
		if p.SyncStatusEnabled {
			sse = 1
		}
		le := 0
		if p.LogEnabled {
			le = 1
		}
		importNow := time.Now().Format("2006-01-02 15:04:05")
		_, err := a.db.Exec(`INSERT INTO sync_tasks(source, target, frequency, mode, start_time, end_time, status, sync_status_enabled, log_enabled, total_data, created_at, updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			p.Source, p.Target, p.Frequency, p.Mode, p.StartTime, p.EndTime, p.Status, sse, le, p.TotalData, importNow, importNow)
		if err == nil {
			imported++
		}
	}
	c.JSON(200, gin.H{"ok": true, "imported": imported})
}

func (a *API) SyncTasksStats(c *gin.Context) {
	// Status distribution
	statusRows, err := a.db.Query("SELECT status, COUNT(*) FROM sync_tasks GROUP BY status")
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer statusRows.Close()
	statusDist := gin.H{}
	for statusRows.Next() {
		var s string
		var cnt int
		if err := statusRows.Scan(&s, &cnt); err == nil {
			statusDist[s] = cnt
		}
	}
	// Mode distribution
	modeRows, err := a.db.Query("SELECT mode, COUNT(*) FROM sync_tasks GROUP BY mode")
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer modeRows.Close()
	modeDist := gin.H{}
	for modeRows.Next() {
		var m string
		var cnt int
		if err := modeRows.Scan(&m, &cnt); err == nil {
			modeDist[m] = cnt
		}
	}
	var total int
	_ = a.db.QueryRow("SELECT COUNT(*) FROM sync_tasks").Scan(&total)
	c.JSON(200, gin.H{"total": total, "status_distribution": statusDist, "mode_distribution": modeDist})
}

func (a *API) SyncTasksProgress(c *gin.Context) {
	// Return per-task progress from engine + DB, aggregated with equal weight
	rows, err := a.db.Query("SELECT id, status, progress, synced_data, total_data FROM sync_tasks")
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	var taskProgresses []gin.H
	totalProgress := 0
	taskCount := 0
	totalSynced := 0
	totalData := 0
	runningCount := 0

	for rows.Next() {
		var id, progress, syncedData, tData int
		var status string
		if err := rows.Scan(&id, &status, &progress, &syncedData, &tData); err != nil {
			continue
		}
		// Override with live engine state if running
		if state := a.syncEng.GetState(id); state != nil && state.Running {
			progress = state.Progress
			syncedData = state.SyncedTables
			tData = state.TotalTables
			runningCount++
		} else if status == "运行中" {
			runningCount++
		}
		taskProgresses = append(taskProgresses, gin.H{
			"id": id, "status": status, "progress": progress,
			"synced_data": syncedData, "total_data": tData,
		})
		totalProgress += progress
		totalSynced += syncedData
		totalData += tData
		taskCount++
	}

	avgProgress := 0
	if taskCount > 0 {
		avgProgress = totalProgress / taskCount
	}
	c.JSON(200, gin.H{
		"progress":      avgProgress,
		"synced_data":   totalSynced,
		"total_data":    totalData,
		"running_count": runningCount,
		"task_count":    taskCount,
		"tasks":         taskProgresses,
	})
}

func (a *API) SyncTasksInfo(c *gin.Context) {
	// Aggregated sync info
	var lastSynced sql.NullString
	_ = a.db.QueryRow("SELECT MAX(last_synced_at) FROM sync_tasks WHERE last_synced_at IS NOT NULL").Scan(&lastSynced)
	var avgSuccessRate, avgDuration float64
	var total int
	_ = a.db.QueryRow("SELECT COUNT(*), COALESCE(AVG(success_rate),0), COALESCE(AVG(avg_duration),0) FROM sync_tasks").Scan(&total, &avgSuccessRate, &avgDuration)
	c.JSON(200, gin.H{
		"last_synced_at": lastSynced.String,
		"success_rate":   avgSuccessRate,
		"avg_duration":   avgDuration,
		"total":          total,
	})
}

// SyncTaskStart starts a single sync task
func (a *API) SyncTaskStart(c *gin.Context) {
	id := c.Param("id")
	var source, target, mode, frequency, status string
	var endTime sql.NullString
	err := a.db.QueryRow("SELECT source, target, mode, frequency, status, end_time FROM sync_tasks WHERE id = ?", id).Scan(&source, &target, &mode, &frequency, &status, &endTime)
	if err != nil {
		c.JSON(404, gin.H{"error": "同步任务不存在"})
		return
	}
	if status == "运行中" {
		c.JSON(400, gin.H{"error": "任务已在运行中"})
		return
	}
	idInt, _ := strconv.Atoi(id)
	if err := a.syncEng.StartTask(idInt, source, target, mode, frequency, endTime.String); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	nowLocal := time.Now().Format("2006-01-02 15:04:05")
	_, _ = a.db.Exec("UPDATE sync_tasks SET status = '运行中', last_synced_at = ?, updated_at = ? WHERE id = ?", nowLocal, nowLocal, id)
	c.JSON(200, gin.H{"ok": true, "message": "同步任务已启动"})
}

// SyncTaskStop stops a single sync task
func (a *API) SyncTaskStop(c *gin.Context) {
	id := c.Param("id")
	idInt, _ := strconv.Atoi(id)
	a.syncEng.StopTask(idInt)
	nowLocal := time.Now().Format("2006-01-02 15:04:05")
	_, _ = a.db.Exec("UPDATE sync_tasks SET status = '已停止', updated_at = ? WHERE id = ?", nowLocal, id)
	c.JSON(200, gin.H{"ok": true, "message": "同步任务已停止"})
}

// SyncTaskStopAll stops all running sync tasks
func (a *API) SyncTaskStopAll(c *gin.Context) {
	stopped := a.syncEng.StopAll()
	nowLocal := time.Now().Format("2006-01-02 15:04:05")
	_, _ = a.db.Exec("UPDATE sync_tasks SET status = '已停止', updated_at = ? WHERE status = '运行中'", nowLocal)
	c.JSON(200, gin.H{"ok": true, "stopped": stopped, "message": fmt.Sprintf("已停止 %d 个同步任务", stopped)})
}

// SyncCheckIP validates that a device IP is reachable and running smartcontrol
func (a *API) SyncCheckIP(c *gin.Context) {
	var p struct {
		IP string `json:"ip"`
	}
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"ok": false, "message": "参数格式错误"})
		return
	}
	p.IP = strings.TrimSpace(p.IP)
	if p.IP == "" {
		c.JSON(400, gin.H{"ok": false, "message": "IP地址不能为空"})
		return
	}
	if err := syncengine.CheckIP(p.IP); err != nil {
		c.JSON(200, gin.H{"ok": false, "message": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true, "message": "设备连接成功"})
}

// SyncPing is a health check endpoint that remote devices call to verify connectivity
func (a *API) SyncPing(c *gin.Context) {
	c.JSON(200, gin.H{"ok": true, "time": time.Now().Format(time.RFC3339)})
}

// SyncExportData exports all syncable tables from the local database
func (a *API) SyncExportData(c *gin.Context) {
	since := strings.TrimSpace(c.Query("since"))
	payload, err := syncengine.ExportLocalData(a.db, since)
	if err != nil {
		c.JSON(500, gin.H{"error": "导出数据失败: " + err.Error()})
		return
	}
	c.JSON(200, payload)
}

// SyncImportData imports data from a remote device into the local database
func (a *API) SyncImportData(c *gin.Context) {
	mode := strings.TrimSpace(c.Query("mode"))
	if mode == "" {
		mode = "full"
	}
	var payload syncengine.ExportPayload
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(400, gin.H{"ok": false, "message": "数据格式错误"})
		return
	}
	synced, total, err := syncengine.ImportData(a.db, &payload, mode)
	if err != nil {
		c.JSON(500, gin.H{"ok": false, "message": "导入失败: " + err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true, "synced": synced, "total": total, "message": fmt.Sprintf("成功同步 %d/%d 张表", synced, total)})
}

func (a *API) ReportPerf(c *gin.Context) {
	m, _ := monitor.CollectMetrics()
	var cntLogs, cntAlerts, cntCrit, cntWarn int
	_ = a.db.QueryRow(`SELECT COUNT(1) FROM logs`).Scan(&cntLogs)
	_ = a.db.QueryRow(`SELECT COUNT(1) FROM alerts`).Scan(&cntAlerts)
	_ = a.db.QueryRow(`SELECT COUNT(1) FROM alerts WHERE severity = 'critical'`).Scan(&cntCrit)
	_ = a.db.QueryRow(`SELECT COUNT(1) FROM alerts WHERE severity = 'warning'`).Scan(&cntWarn)
	summary := gin.H{
		"timestamp":        time.Now().Format(time.RFC3339),
		"metrics":          m,
		"logs_count":       cntLogs,
		"alerts_count":     cntAlerts,
		"alerts_breakdown": gin.H{"critical": cntCrit, "warning": cntWarn},
		"notes":            "本报告基于当前实时指标与历史事件计数，供快速评估使用",
	}
	c.JSON(200, summary)
}

func (a *API) PerfReportList(c *gin.Context) {
	pagination := utils.GetPagination(c)
	moduleName := strings.TrimSpace(c.Query("module_name"))
	userId := strings.TrimSpace(c.Query("user_id"))
	analysisType := strings.TrimSpace(c.Query("analysis_type"))
	dateFrom := strings.TrimSpace(c.Query("date_from"))
	dateTo := strings.TrimSpace(c.Query("date_to"))
	sortBy := strings.TrimSpace(c.Query("sort_by"))
	sortOrder := strings.TrimSpace(c.Query("sort_order"))
	dataFilter := strings.TrimSpace(c.Query("data_filter"))

	where := []string{"1=1"}
	args := []any{}
	if moduleName != "" {
		where = append(where, "LOWER(module_name) LIKE LOWER(?)")
		args = append(args, "%"+moduleName+"%")
	}
	if userId != "" {
		where = append(where, "LOWER(user_id) LIKE LOWER(?)")
		args = append(args, "%"+userId+"%")
	}
	if analysisType != "" && analysisType != "全部" {
		where = append(where, "analysis_type = ?")
		args = append(args, analysisType)
	}
	if dateFrom != "" {
		where = append(where, "analysis_time >= ?")
		args = append(args, dateFrom)
	}
	if dateTo != "" {
		where = append(where, "analysis_time <= ?")
		args = append(args, dateTo)
	}
	whereClause := strings.Join(where, " AND ")

	var total int
	_ = a.db.QueryRow("SELECT COUNT(*) FROM perf_reports WHERE "+whereClause, args...).Scan(&total)

	orderCol := "id"
	switch sortBy {
	case "response_time":
		orderCol = "response_time"
	case "throughput":
		orderCol = "throughput"
	case "error_rate":
		orderCol = "error_rate"
	case "analysis_time":
		orderCol = "analysis_time"
	}
	order := "DESC"
	if sortOrder == "asc" || sortOrder == "升序" {
		order = "ASC"
	}

	limit := pagination.PageSize
	offset := pagination.Offset
	if dataFilter == "前10条数据" {
		limit = 10
		offset = 0
		order = "ASC"
	} else if dataFilter == "后10条数据" {
		limit = 10
		offset = 0
		order = "DESC"
	}

	queryArgs := append(args, limit, offset)
	rows, err := a.db.Query("SELECT id, analysis_time, module_name, user_id, response_time, throughput, error_rate, description, analysis_type, created_at FROM perf_reports WHERE "+whereClause+" ORDER BY "+orderCol+" "+order+" LIMIT ? OFFSET ?", queryArgs...)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var items []gin.H
	for rows.Next() {
		var id int
		var analysisTime, modName, uid, respTime, tp, errRate, desc, aType, created string
		if err := rows.Scan(&id, &analysisTime, &modName, &uid, &respTime, &tp, &errRate, &desc, &aType, &created); err == nil {
			items = append(items, gin.H{
				"id": id, "analysis_time": analysisTime, "module_name": modName,
				"user_id": uid, "response_time": respTime, "throughput": tp,
				"error_rate": errRate, "description": desc, "analysis_type": aType,
				"created_at": created,
			})
		}
	}
	c.JSON(200, gin.H{"items": items, "total": total, "page": pagination.Page, "page_size": pagination.PageSize})
}

func (a *API) PerfReportAdd(c *gin.Context) {
	var p struct {
		AnalysisTime string `json:"analysis_time"`
		ModuleName   string `json:"module_name"`
		UserID       string `json:"user_id"`
		ResponseTime string `json:"response_time"`
		Throughput   string `json:"throughput"`
		ErrorRate    string `json:"error_rate"`
		Description  string `json:"description"`
		AnalysisType string `json:"analysis_type"`
	}
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"error": "参数格式错误"})
		return
	}
	if p.ModuleName == "" {
		c.JSON(400, gin.H{"error": "模块名称不能为空"})
		return
	}
	if p.AnalysisType == "" {
		p.AnalysisType = "整体性能"
	}
	if p.AnalysisTime == "" {
		p.AnalysisTime = time.Now().Format("2006-01-02 15:04")
	}
	res, err := a.db.Exec(`INSERT INTO perf_reports(analysis_time, module_name, user_id, response_time, throughput, error_rate, description, analysis_type) VALUES(?,?,?,?,?,?,?,?)`,
		p.AnalysisTime, p.ModuleName, p.UserID, p.ResponseTime, p.Throughput, p.ErrorRate, p.Description, p.AnalysisType)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	newId, _ := res.LastInsertId()
	c.JSON(200, gin.H{"ok": true, "id": newId})
}

func (a *API) PerfReportImport(c *gin.Context) {
	var items []struct {
		AnalysisTime string `json:"analysis_time"`
		ModuleName   string `json:"module_name"`
		UserID       string `json:"user_id"`
		ResponseTime string `json:"response_time"`
		Throughput   string `json:"throughput"`
		ErrorRate    string `json:"error_rate"`
		Description  string `json:"description"`
		AnalysisType string `json:"analysis_type"`
	}
	if err := c.BindJSON(&items); err != nil {
		c.JSON(400, gin.H{"error": "参数格式错误，需要JSON数组"})
		return
	}
	imported := 0
	for _, p := range items {
		if p.ModuleName == "" {
			continue
		}
		if p.AnalysisType == "" {
			p.AnalysisType = "整体性能"
		}
		if p.AnalysisTime == "" {
			p.AnalysisTime = time.Now().Format("2006-01-02 15:04")
		}
		_, err := a.db.Exec(`INSERT INTO perf_reports(analysis_time, module_name, user_id, response_time, throughput, error_rate, description, analysis_type) VALUES(?,?,?,?,?,?,?,?)`,
			p.AnalysisTime, p.ModuleName, p.UserID, p.ResponseTime, p.Throughput, p.ErrorRate, p.Description, p.AnalysisType)
		if err == nil {
			imported++
		}
	}
	c.JSON(200, gin.H{"ok": true, "imported": imported})
}

func (a *API) PerfReportDelete(c *gin.Context) {
	id := c.Param("id")
	result, err := a.db.Exec("DELETE FROM perf_reports WHERE id = ?", id)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		c.JSON(404, gin.H{"error": "记录不存在"})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// PerfCollect automatically probes each module API to collect performance data
func (a *API) PerfCollect(c *gin.Context) {
	// Get the token from the incoming request for internal API calls
	token := c.GetHeader("Authorization")

	// Define modules and their probe endpoints
	type probeTarget struct {
		Module   string
		Endpoint string
	}
	targets := []probeTarget{
		{"远程桌面控制", "/api/devices"},
		{"语音交互", "/api/audio/list"},
		{"异常报警", "/api/alerts/list"},
		{"操作日志", "/api/logs/list"},
		{"软件更新", "/api/updates/list"},
		{"数据同步", "/api/sync/tasks"},
		{"视频监控", "/api/video/list"},
		{"硬件检测", "/api/hardware/items"},
		{"性能分析", "/api/report/perf-list"},
		{"用户统计", "/api/user/stats"},
	}

	// Determine base URL from the request
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	baseURL := scheme + "://" + c.Request.Host

	var body struct {
		UserID string `json:"user_id"`
	}
	_ = c.BindJSON(&body)

	client := &http.Client{Timeout: 10 * time.Second}
	now := time.Now().Format("2006-01-02 15:04")
	userId := strings.TrimSpace(body.UserID)
	if userId == "" {
		userId = "system"
	}

	inserted := 0
	var results []gin.H

	for _, t := range targets {
		url := baseURL + t.Endpoint + "?page_size=1"

		// Probe multiple times for throughput estimation
		const probeCount = 3
		var totalMs float64
		errCount := 0

		for i := 0; i < probeCount; i++ {
			req, err := http.NewRequest("GET", url, nil)
			if err != nil {
				errCount++
				continue
			}
			if token != "" {
				req.Header.Set("Authorization", token)
			}
			start := time.Now()
			resp, err := client.Do(req)
			elapsed := time.Since(start).Milliseconds()
			if err != nil {
				errCount++
				totalMs += float64(elapsed)
				continue
			}
			resp.Body.Close()
			if resp.StatusCode >= 400 {
				errCount++
			}
			totalMs += float64(elapsed)
		}

		avgMs := totalMs / float64(probeCount)
		errRate := float64(errCount) / float64(probeCount) * 100
		// Estimate throughput: requests per second based on average response time
		throughput := 0.0
		if avgMs > 0 {
			throughput = 1000.0 / avgMs
		}

		respTimeStr := fmt.Sprintf("%.0fms", avgMs)
		throughputStr := fmt.Sprintf("%.0f req/s", throughput)
		errRateStr := fmt.Sprintf("%.1f%%", errRate)

		_, err := a.db.Exec(`INSERT INTO perf_reports(analysis_time, module_name, user_id, response_time, throughput, error_rate, description, analysis_type) VALUES(?,?,?,?,?,?,?,?)`,
			now, t.Module, userId, respTimeStr, throughputStr, errRateStr,
			fmt.Sprintf("自动采集 - 探测接口: %s, 采样%d次", t.Endpoint, probeCount),
			"整体性能")
		if err == nil {
			inserted++
		}

		results = append(results, gin.H{
			"module":        t.Module,
			"response_time": respTimeStr,
			"throughput":    throughputStr,
			"error_rate":    errRateStr,
		})
	}

	c.JSON(200, gin.H{"ok": true, "collected": inserted, "results": results})
}

func (a *API) VNCProxyWS(c *gin.Context) {
	target := c.Query("target")
	if target == "" {
		target = "127.0.0.1:5900"
	}
	conn, err := net.Dial("tcp", target)
	if err != nil {
		c.Status(502)
		return
	}
	defer conn.Close()
	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer ws.Close()
	done := make(chan struct{})
	go func() {
		buf := make([]byte, 8192)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				_ = ws.WriteMessage(websocket.BinaryMessage, buf[:n])
			}
			if err != nil {
				close(done)
				return
			}
		}
	}()
	go func() {
		for {
			mt, data, err := ws.ReadMessage()
			if err != nil {
				conn.Close()
				return
			}
			if len(data) > 0 {
				_, _ = conn.Write(data)
			}
			_ = mt // not used distinction; always forward data
		}
	}()
	<-done
}

// SSHProxyWS upgrades to WebSocket, connects to target via SSH, and bridges terminal I/O
func (a *API) SSHProxyWS(c *gin.Context) {
	host := c.Query("host")
	port := c.Query("port")
	user := c.Query("user")
	pass := c.Query("pass")
	if host == "" {
		host = "127.0.0.1"
	}
	if port == "" {
		port = "22"
	}
	if user == "" {
		c.JSON(400, gin.H{"error": "user required"})
		return
	}

	config := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.Password(pass)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	addr := net.JoinHostPort(host, port)
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		c.String(502, "SSH dial failed: %v", err)
		return
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		c.String(502, "SSH session failed: %v", err)
		return
	}
	defer session.Close()

	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer ws.Close()

	stdinPipe, _ := session.StdinPipe()
	stdoutPipe, _ := session.StdoutPipe()
	stderrPipe, _ := session.StderrPipe()

	// request PTY
	modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}
	if err := session.RequestPty("xterm-256color", 40, 120, modes); err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("PTY request failed: "+err.Error()))
		return
	}
	if err := session.Shell(); err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("Shell failed: "+err.Error()))
		return
	}

	done := make(chan struct{})

	// stdout -> ws
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stdoutPipe.Read(buf)
			if n > 0 {
				ws.WriteMessage(websocket.TextMessage, buf[:n])
			}
			if err != nil {
				close(done)
				return
			}
		}
	}()
	// stderr -> ws
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stderrPipe.Read(buf)
			if n > 0 {
				ws.WriteMessage(websocket.TextMessage, buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	// ws -> stdin (handle resize messages too)
	go func() {
		for {
			_, data, err := ws.ReadMessage()
			if err != nil {
				stdinPipe.Close()
				return
			}
			if len(data) > 0 && data[0] == '\x01' {
				// resize message: \x01 + JSON {"cols":N,"rows":N}
				var sz struct {
					Cols int `json:"cols"`
					Rows int `json:"rows"`
				}
				if json.Unmarshal(data[1:], &sz) == nil && sz.Cols > 0 && sz.Rows > 0 {
					session.WindowChange(sz.Rows, sz.Cols)
				}
			} else {
				stdinPipe.Write(data)
			}
		}
	}()

	<-done
}
