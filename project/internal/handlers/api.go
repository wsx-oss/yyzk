package handlers

import (
	"database/sql"
	"encoding/json"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"smartcontrol/internal/monitor"
	"smartcontrol/internal/utils"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"
)

type API struct {
	db *sql.DB
}

// DevicesList returns devices filtered by optional name and protocol, ordered with matching items first
func (a *API) DevicesList(c *gin.Context) {
	name := strings.TrimSpace(c.Query("name"))
	protocol := strings.TrimSpace(strings.ToUpper(c.Query("protocol")))

	var args []any
	// 不做 WHERE 过滤，仅按匹配程度置顶
	q := "SELECT id, name, ip, protocol, port, username, auto_connect, log_enabled, description, status, created_at, updated_at, last_connected_at FROM devices"
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
	a := &API{db: database}
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

		api.POST("/logs/append", a.LogAppend)
		api.GET("/logs/list", a.LogList)

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

		api.GET("/report/perf", a.ReportPerf)

		r.GET("/api/vnc/ws", a.VNCProxyWS)
		r.GET("/api/ssh/ws", a.SSHProxyWS)

		// devices management
		api.GET("/devices", a.DevicesList)
		api.POST("/devices", a.DevicesCreate)
		api.POST("/devices/disconnect-all", a.DevicesDisconnectAll)
		api.DELETE("/devices/:id", a.DevicesDelete)
		api.POST("/devices/:id/status", a.DeviceSetStatus)

		// video sources
		api.GET("/video/list", a.VideoList)
		api.POST("/video/add", a.VideoAdd)
		api.DELETE("/video/:id", a.VideoDelete)

		// hardware items management
		api.GET("/hardware/items", a.HardwareItemsList)
		api.POST("/hardware/items", a.HardwareItemsCreate)
		api.GET("/hardware/items/stats", a.HardwareItemsStats)
		api.POST("/hardware/items/refresh", a.HardwareItemsRefresh)
		api.GET("/hardware/items/:id", a.HardwareItemsGet)
		api.PUT("/hardware/items/:id", a.HardwareItemsUpdate)
		api.DELETE("/hardware/items/:id", a.HardwareItemsDelete)

		// user stats
		api.GET("/user/stats", a.UserStatsGet)
		api.POST("/user/stats/incr_connection", a.UserStatsIncrConnection)
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

	res, err := a.db.Exec(`INSERT INTO hardware_items(name, type, ip, status, description, temperature, cpu_usage, mem_usage, network_bandwidth, detected_at, created_at, updated_at) VALUES(?,?,?,?,?,?,?,?,?,datetime('now'),datetime('now'),datetime('now'))`,
		p.Name, p.Type, p.IP, p.Status, p.Description, p.Temperature, p.CPUUsage, p.MemUsage, p.Bandwidth)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	newId, _ := res.LastInsertId()
	c.JSON(200, gin.H{"ok": true, "id": newId})
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
		sets = append(sets, "ip = ?")
		args = append(args, strings.TrimSpace(*p.IP))
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

func (a *API) HardwareItemsRefresh(c *gin.Context) {
	// Simulate refreshing hardware data: randomize temperature/cpu/mem slightly for demo
	rows, err := a.db.Query("SELECT id, temperature, cpu_usage, mem_usage FROM hardware_items")
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	type hw struct {
		id             int
		temp, cpu, mem float64
	}
	var items []hw
	for rows.Next() {
		var h hw
		if err := rows.Scan(&h.id, &h.temp, &h.cpu, &h.mem); err == nil {
			items = append(items, h)
		}
	}
	for _, h := range items {
		_, _ = a.db.Exec("UPDATE hardware_items SET detected_at = datetime('now'), updated_at = datetime('now') WHERE id = ?", h.id)
	}
	c.JSON(200, gin.H{"ok": true, "refreshed": len(items)})
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
	for {
		m, err := monitor.CollectMetrics()
		if err != nil {
			return
		}
		b, _ := json.Marshal(m)
		if err := ws.WriteMessage(websocket.TextMessage, b); err != nil {
			return
		}
		time.Sleep(1 * time.Second)
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

	var total int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM alerts`).Scan(&total)

	rows, err := a.db.Query(`SELECT id, category, severity, message, acknowledged, created_at FROM alerts ORDER BY id DESC LIMIT ? OFFSET ?`,
		pagination.PageSize, pagination.Offset)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var items []gin.H
	for rows.Next() {
		var id int
		var cat, sev, msg string
		var ack int
		var created string
		if err := rows.Scan(&id, &cat, &sev, &msg, &ack, &created); err == nil {
			items = append(items, gin.H{"id": id, "category": cat, "severity": sev, "message": msg, "acknowledged": ack == 1, "created_at": created})
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
	var p struct{ Category, Severity, Message string }
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"error": "bad json"})
		return
	}
	_, err := a.db.Exec(`INSERT INTO alerts(category, severity, message) VALUES(?,?,?)`, p.Category, p.Severity, p.Message)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (a *API) LogAppend(c *gin.Context) {
	var p struct{ Level, Message, Meta string }
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"error": "bad json"})
		return
	}
	_, err := a.db.Exec(`INSERT INTO logs(level, message, meta) VALUES(?,?,?)`, p.Level, p.Message, p.Meta)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (a *API) LogList(c *gin.Context) {
	pagination := utils.GetPagination(c)

	var total int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM logs`).Scan(&total)

	rows, err := a.db.Query(`SELECT id, level, message, meta, created_at FROM logs ORDER BY id DESC LIMIT ? OFFSET ?`,
		pagination.PageSize, pagination.Offset)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var items []gin.H
	for rows.Next() {
		var id int
		var level, message, meta, created string
		if err := rows.Scan(&id, &level, &message, &meta, &created); err == nil {
			items = append(items, gin.H{"id": id, "level": level, "message": message, "meta": meta, "created_at": created})
		}
	}
	c.JSON(200, gin.H{
		"items":     items,
		"total":     total,
		"page":      pagination.Page,
		"page_size": pagination.PageSize,
	})
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
	_, err := a.db.Exec(`UPDATE sync_status SET status=?, message=?, last_synced_at=datetime('now'), updated_at=datetime('now') WHERE id = 1`, p.Status, p.Message)
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
		TotalData         int    `json:"total_data"`
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
	res, err := a.db.Exec(`INSERT INTO sync_tasks(source, target, frequency, mode, start_time, end_time, status, sync_status_enabled, log_enabled, total_data, created_at, updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,datetime('now'),datetime('now'))`,
		p.Source, p.Target, p.Frequency, p.Mode, p.StartTime, p.EndTime, p.Status, sse, le, p.TotalData)
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
	sets := []string{"updated_at = datetime('now')"}
	args := []any{}
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
	if p.Status != nil {
		sets = append(sets, "status = ?")
		args = append(args, *p.Status)
		if *p.Status == "运行中" {
			sets = append(sets, "last_synced_at = datetime('now')")
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
		_, err := a.db.Exec(`INSERT INTO sync_tasks(source, target, frequency, mode, start_time, end_time, status, sync_status_enabled, log_enabled, total_data, created_at, updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,datetime('now'),datetime('now'))`,
			p.Source, p.Target, p.Frequency, p.Mode, p.StartTime, p.EndTime, p.Status, sse, le, p.TotalData)
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
	// Return aggregated progress across all running tasks
	var totalSynced, totalData, runningCount int
	_ = a.db.QueryRow("SELECT COALESCE(SUM(synced_data),0), COALESCE(SUM(total_data),0), COUNT(*) FROM sync_tasks WHERE status = '运行中'").Scan(&totalSynced, &totalData, &runningCount)
	progress := 0
	if totalData > 0 {
		progress = totalSynced * 100 / totalData
	}
	c.JSON(200, gin.H{"progress": progress, "synced_data": totalSynced, "total_data": totalData, "running_count": runningCount})
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

func (a *API) VideoList(c *gin.Context) {
	rows, err := a.db.Query(`SELECT id, name, url, region, clarity, status, recording, start_time, end_time, created_at FROM video_sources ORDER BY id DESC`)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var items []gin.H
	for rows.Next() {
		var id int
		var name, url, region, clarity, status, startTime, endTime, created string
		var recording int
		if err := rows.Scan(&id, &name, &url, &region, &clarity, &status, &recording, &startTime, &endTime, &created); err == nil {
			items = append(items, gin.H{"id": id, "name": name, "url": url, "region": region, "clarity": clarity, "status": status, "recording": recording == 1, "start_time": startTime, "end_time": endTime, "created_at": created})
		}
	}
	c.JSON(200, gin.H{"items": items})
}

func (a *API) VideoAdd(c *gin.Context) {
	var p struct {
		Name      string `json:"name"`
		Url       string `json:"url"`
		Region    string `json:"region"`
		Clarity   string `json:"clarity"`
		Status    string `json:"status"`
		Recording bool   `json:"recording"`
		StartTime string `json:"start_time"`
		EndTime   string `json:"end_time"`
	}
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"error": "bad json"})
		return
	}
	if p.Name == "" || p.Url == "" {
		c.JSON(400, gin.H{"error": "name and url required"})
		return
	}
	rec := 0
	if p.Recording {
		rec = 1
	}
	res, err := a.db.Exec(`INSERT INTO video_sources(name, url, region, clarity, status, recording, start_time, end_time) VALUES(?,?,?,?,?,?,?,?)`,
		p.Name, p.Url, p.Region, p.Clarity, p.Status, rec, p.StartTime, p.EndTime)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	newId, _ := res.LastInsertId()
	c.JSON(200, gin.H{"ok": true, "id": newId})
}

func (a *API) VideoDelete(c *gin.Context) {
	id := c.Param("id")
	result, err := a.db.Exec(`DELETE FROM video_sources WHERE id = ?`, id)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	c.JSON(200, gin.H{"ok": true})
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
