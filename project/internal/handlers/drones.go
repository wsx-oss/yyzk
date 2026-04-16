package handlers

import (
	"database/sql"
	"strings"

	"github.com/gin-gonic/gin"
)

// ==================== Unified Drone Registry ====================

// DronesList returns all registered drones with optional filters
func (a *API) DronesList(c *gin.Context) {
	name := strings.TrimSpace(c.Query("name"))
	status := strings.TrimSpace(c.Query("status"))
	dataActive := strings.TrimSpace(c.Query("data_active"))

	where := []string{"1=1"}
	args := []any{}
	if name != "" {
		where = append(where, "name LIKE ?")
		args = append(args, "%"+name+"%")
	}
	if status != "" {
		where = append(where, "status = ?")
		args = append(args, status)
	}
	if dataActive == "true" {
		where = append(where, "linked_gps_device_id > 0 AND linked_gps_device_id IN (SELECT id FROM gps_devices WHERE status='在线')")
	}

	wc := strings.Join(where, " AND ")

	var total int
	_ = a.db.QueryRow("SELECT COUNT(*) FROM drones WHERE "+wc, args...).Scan(&total)

	q := `SELECT id, name, model, description, ip, ssh_port, vnc_port, rdp_port, protocol, username,
		agent_id, initial_lat, initial_lng, initial_alt, fence_enabled, fence_lat, fence_lng, fence_radius,
		auto_connect, log_enabled, status, linked_device_id, linked_gps_device_id, created_at, updated_at, COALESCE(video_url,'')
		FROM drones WHERE ` + wc + ` ORDER BY datetime(created_at) DESC`
	rows, err := a.db.Query(q, args...)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	items := []gin.H{}
	for rows.Next() {
		var id, sshPort, vncPort, rdpPort, fenceEnabled, autoConn, logEn, linkedDevID, linkedGpsID int
		var name, model, desc, ip, protocol, username, agentID, status, videoURL string
		var lat, lng, alt, fLat, fLng, fRadius float64
		var created, updated sql.NullString
		if err := rows.Scan(&id, &name, &model, &desc, &ip, &sshPort, &vncPort, &rdpPort, &protocol, &username,
			&agentID, &lat, &lng, &alt, &fenceEnabled, &fLat, &fLng, &fRadius,
			&autoConn, &logEn, &status, &linkedDevID, &linkedGpsID, &created, &updated, &videoURL); err == nil {

			// check linked gps_device status (maintained by GpsPushByAgent + runOfflineDetection)
			gpsOnline := false
			gpsStatus := ""
			if linkedGpsID > 0 {
				_ = a.db.QueryRow(`SELECT status FROM gps_devices WHERE id=?`, linkedGpsID).Scan(&gpsStatus)
				gpsOnline = gpsStatus == "在线"
			}

			items = append(items, gin.H{
				"id": id, "name": name, "model": model, "description": desc,
				"ip": ip, "ssh_port": sshPort, "vnc_port": vncPort, "rdp_port": rdpPort,
				"protocol": protocol, "username": username, "agent_id": agentID,
				"initial_lat": lat, "initial_lng": lng, "initial_alt": alt,
				"fence_enabled": fenceEnabled == 1, "fence_lat": fLat, "fence_lng": fLng, "fence_radius": fRadius,
				"auto_connect": autoConn == 1, "log_enabled": logEn == 1,
				"status": status, "gps_online": gpsOnline, "gps_status": gpsStatus, "video_url": videoURL,
				"linked_device_id": linkedDevID, "linked_gps_device_id": linkedGpsID,
				"created_at": created.String, "updated_at": updated.String,
			})
		}
	}
	c.JSON(200, gin.H{"items": items, "total": total})
}

// DronesGet returns a single drone by id
func (a *API) DronesGet(c *gin.Context) {
	id := c.Param("id")
	var d struct {
		ID, SSHPort, VNCPort, RDPPort, FenceEnabled, AutoConn, LogEn, LinkedDevID, LinkedGpsID int
		Name, Model, Desc, IP, Protocol, Username, Password, AgentID, Status, VideoURL         string
		Lat, Lng, Alt, FLat, FLng, FRadius                                                     float64
		Created, Updated                                                                        sql.NullString
	}
	err := a.db.QueryRow(
		`SELECT id, name, model, description, ip, ssh_port, vnc_port, rdp_port, protocol, username, password,
		agent_id, initial_lat, initial_lng, initial_alt, fence_enabled, fence_lat, fence_lng, fence_radius,
		auto_connect, log_enabled, status, linked_device_id, linked_gps_device_id, created_at, updated_at, COALESCE(video_url,'')
		FROM drones WHERE id=?`, id,
	).Scan(&d.ID, &d.Name, &d.Model, &d.Desc, &d.IP, &d.SSHPort, &d.VNCPort, &d.RDPPort, &d.Protocol, &d.Username, &d.Password,
		&d.AgentID, &d.Lat, &d.Lng, &d.Alt, &d.FenceEnabled, &d.FLat, &d.FLng, &d.FRadius,
		&d.AutoConn, &d.LogEn, &d.Status, &d.LinkedDevID, &d.LinkedGpsID, &d.Created, &d.Updated, &d.VideoURL)
	if err != nil {
		c.JSON(404, gin.H{"error": "无人机不存在"})
		return
	}
	c.JSON(200, gin.H{
		"id": d.ID, "name": d.Name, "model": d.Model, "description": d.Desc,
		"ip": d.IP, "ssh_port": d.SSHPort, "vnc_port": d.VNCPort, "rdp_port": d.RDPPort,
		"protocol": d.Protocol, "username": d.Username, "password": d.Password, "agent_id": d.AgentID,
		"initial_lat": d.Lat, "initial_lng": d.Lng, "initial_alt": d.Alt,
		"fence_enabled": d.FenceEnabled == 1, "fence_lat": d.FLat, "fence_lng": d.FLng, "fence_radius": d.FRadius,
		"auto_connect": d.AutoConn == 1, "log_enabled": d.LogEn == 1,
		"status": d.Status, "video_url": d.VideoURL,
		"linked_device_id": d.LinkedDevID, "linked_gps_device_id": d.LinkedGpsID,
		"created_at": d.Created.String, "updated_at": d.Updated.String,
	})
}

// DronesCreate registers a new drone and auto-creates linked devices + gps_devices entries
func (a *API) DronesCreate(c *gin.Context) {
	var p struct {
		Name         string  `json:"name"`
		Model        string  `json:"model"`
		Description  string  `json:"description"`
		IP           string  `json:"ip"`
		SSHPort      int     `json:"ssh_port"`
		VNCPort      int     `json:"vnc_port"`
		RDPPort      int     `json:"rdp_port"`
		Protocol     string  `json:"protocol"`
		Username     string  `json:"username"`
		Password     string  `json:"password"`
		AgentID      string  `json:"agent_id"`
		InitialLat   float64 `json:"initial_lat"`
		InitialLng   float64 `json:"initial_lng"`
		InitialAlt   float64 `json:"initial_alt"`
		FenceEnabled bool    `json:"fence_enabled"`
		FenceLat     float64 `json:"fence_lat"`
		FenceLng     float64 `json:"fence_lng"`
		FenceRadius  float64 `json:"fence_radius"`
		AutoConnect  bool    `json:"auto_connect"`
		LogEnabled   bool    `json:"log_enabled"`
		VideoURL     string  `json:"video_url"`
	}
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"error": "bad json"})
		return
	}
	p.Name = strings.TrimSpace(p.Name)
	p.IP = strings.TrimSpace(p.IP)
	p.Protocol = strings.ToUpper(strings.TrimSpace(p.Protocol))
	p.AgentID = strings.TrimSpace(p.AgentID)
	p.VideoURL = strings.TrimSpace(p.VideoURL)
	if p.Name == "" {
		c.JSON(400, gin.H{"error": "无人机名称不能为空"})
		return
	}
	if p.IP == "" {
		c.JSON(400, gin.H{"error": "IP地址不能为空"})
		return
	}
	if p.Protocol == "" {
		p.Protocol = "SSH"
	}
	if p.Protocol != "VNC" && p.Protocol != "RDP" && p.Protocol != "SSH" {
		c.JSON(400, gin.H{"error": "协议必须是 VNC/RDP/SSH"})
		return
	}
	if p.SSHPort == 0 {
		p.SSHPort = 22
	}
	if p.VNCPort == 0 {
		p.VNCPort = 5900
	}
	if p.RDPPort == 0 {
		p.RDPPort = 3389
	}
	if p.AgentID == "" {
		p.AgentID = p.Name
	}
	if p.FenceRadius == 0 {
		p.FenceRadius = 500
	}

	fenceEn := 0
	if p.FenceEnabled {
		fenceEn = 1
	}
	ac := 0
	if p.AutoConnect {
		ac = 1
	}
	le := 0
	if p.LogEnabled {
		le = 1
	}

	// ---- Insert drone ----
	res, err := a.db.Exec(
		`INSERT INTO drones(name, model, description, ip, ssh_port, vnc_port, rdp_port, protocol, username, password,
		agent_id, initial_lat, initial_lng, initial_alt, fence_enabled, fence_lat, fence_lng, fence_radius,
		auto_connect, log_enabled, status, video_url)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.Name, p.Model, p.Description, p.IP, p.SSHPort, p.VNCPort, p.RDPPort, p.Protocol, p.Username, p.Password,
		p.AgentID, p.InitialLat, p.InitialLng, p.InitialAlt, fenceEn, p.FenceLat, p.FenceLng, p.FenceRadius,
		ac, le, "offline", p.VideoURL,
	)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	droneID, _ := res.LastInsertId()

	// ---- Auto-create linked remote device (devices table) ----
	port := p.SSHPort
	if p.Protocol == "VNC" {
		port = p.VNCPort
	} else if p.Protocol == "RDP" {
		port = p.RDPPort
	}
	devRes, err := a.db.Exec(
		`INSERT INTO devices(name, ip, protocol, port, username, password, auto_connect, log_enabled, description, status, drone_id, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,datetime('now'),datetime('now'))`,
		p.Name, p.IP, p.Protocol, port, p.Username, p.Password, ac, le,
		"[自动关联] "+p.Description, "offline", droneID,
	)
	var linkedDevID int64
	if err == nil {
		linkedDevID, _ = devRes.LastInsertId()
	}

	// ---- Auto-create linked GPS device (gps_devices table) ----
	// Use status '等待连接' and last_update=NULL so that offline detection
	// ignores this device until the Agent pushes real GPS data.
	gpsRes, err := a.db.Exec(
		`INSERT INTO gps_devices(name, agent_id, device_type, latitude, longitude, altitude, speed, heading, accuracy, status,
		fence_enabled, fence_lat, fence_lng, fence_radius, drone_id, last_update)
		VALUES(?,?,?,?,?,?,0,0,0,'等待连接',?,?,?,?,?,NULL)`,
		p.Name, p.AgentID, "无人机", p.InitialLat, p.InitialLng, p.InitialAlt,
		fenceEn, p.FenceLat, p.FenceLng, p.FenceRadius, droneID,
	)
	var linkedGpsID int64
	if err == nil {
		linkedGpsID, _ = gpsRes.LastInsertId()
		// Only record initial GPS history if coordinates were provided (non-zero)
		if p.InitialLat != 0 || p.InitialLng != 0 {
			a.db.Exec(`INSERT INTO gps_history(device_id, latitude, longitude, altitude, speed, heading) VALUES(?,?,?,?,0,0)`,
				linkedGpsID, p.InitialLat, p.InitialLng, p.InitialAlt)
		}
	}

	// ---- Update drone with linked IDs ----
	a.db.Exec(`UPDATE drones SET linked_device_id=?, linked_gps_device_id=? WHERE id=?`, linkedDevID, linkedGpsID, droneID)

	c.JSON(200, gin.H{"ok": true, "id": droneID, "linked_device_id": linkedDevID, "linked_gps_device_id": linkedGpsID})
}

// DronesUpdate edits a drone and syncs linked entries
func (a *API) DronesUpdate(c *gin.Context) {
	id := c.Param("id")
	var p struct {
		Name         string  `json:"name"`
		Model        string  `json:"model"`
		Description  string  `json:"description"`
		IP           string  `json:"ip"`
		SSHPort      int     `json:"ssh_port"`
		VNCPort      int     `json:"vnc_port"`
		RDPPort      int     `json:"rdp_port"`
		Protocol     string  `json:"protocol"`
		Username     string  `json:"username"`
		Password     string  `json:"password"`
		AgentID      string  `json:"agent_id"`
		InitialLat   float64 `json:"initial_lat"`
		InitialLng   float64 `json:"initial_lng"`
		InitialAlt   float64 `json:"initial_alt"`
		FenceEnabled bool    `json:"fence_enabled"`
		FenceLat     float64 `json:"fence_lat"`
		FenceLng     float64 `json:"fence_lng"`
		FenceRadius  float64 `json:"fence_radius"`
		AutoConnect  bool    `json:"auto_connect"`
		LogEnabled   bool    `json:"log_enabled"`
		VideoURL     string  `json:"video_url"`
	}
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"error": "bad json"})
		return
	}
	p.Name = strings.TrimSpace(p.Name)
	p.IP = strings.TrimSpace(p.IP)
	p.Protocol = strings.ToUpper(strings.TrimSpace(p.Protocol))
	p.AgentID = strings.TrimSpace(p.AgentID)
	p.VideoURL = strings.TrimSpace(p.VideoURL)
	if p.Name == "" {
		c.JSON(400, gin.H{"error": "无人机名称不能为空"})
		return
	}
	if p.Protocol == "" {
		p.Protocol = "SSH"
	}
	if p.SSHPort == 0 {
		p.SSHPort = 22
	}
	if p.VNCPort == 0 {
		p.VNCPort = 5900
	}
	if p.RDPPort == 0 {
		p.RDPPort = 3389
	}
	if p.AgentID == "" {
		p.AgentID = p.Name
	}

	fenceEn := 0
	if p.FenceEnabled {
		fenceEn = 1
	}
	ac := 0
	if p.AutoConnect {
		ac = 1
	}
	le := 0
	if p.LogEnabled {
		le = 1
	}

	// Read existing linked IDs
	var linkedDevID, linkedGpsID int
	a.db.QueryRow(`SELECT linked_device_id, linked_gps_device_id FROM drones WHERE id=?`, id).Scan(&linkedDevID, &linkedGpsID)

	// Update drone
	_, err := a.db.Exec(
		`UPDATE drones SET name=?, model=?, description=?, ip=?, ssh_port=?, vnc_port=?, rdp_port=?, protocol=?,
		username=?, password=?, agent_id=?, initial_lat=?, initial_lng=?, initial_alt=?,
		fence_enabled=?, fence_lat=?, fence_lng=?, fence_radius=?, auto_connect=?, log_enabled=?, video_url=?, updated_at=datetime('now')
		WHERE id=?`,
		p.Name, p.Model, p.Description, p.IP, p.SSHPort, p.VNCPort, p.RDPPort, p.Protocol,
		p.Username, p.Password, p.AgentID, p.InitialLat, p.InitialLng, p.InitialAlt,
		fenceEn, p.FenceLat, p.FenceLng, p.FenceRadius, ac, le, p.VideoURL, id,
	)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	// Sync linked remote device
	port := p.SSHPort
	if p.Protocol == "VNC" {
		port = p.VNCPort
	} else if p.Protocol == "RDP" {
		port = p.RDPPort
	}
	if linkedDevID > 0 {
		a.db.Exec(
			`UPDATE devices SET name=?, ip=?, protocol=?, port=?, username=?, password=?, auto_connect=?, log_enabled=?, description=?, updated_at=datetime('now') WHERE id=?`,
			p.Name, p.IP, p.Protocol, port, p.Username, p.Password, ac, le, "[自动关联] "+p.Description, linkedDevID,
		)
	}

	// Sync linked GPS device
	if linkedGpsID > 0 {
		a.db.Exec(
			`UPDATE gps_devices SET name=?, agent_id=?, fence_enabled=?, fence_lat=?, fence_lng=?, fence_radius=? WHERE id=?`,
			p.Name, p.AgentID, fenceEn, p.FenceLat, p.FenceLng, p.FenceRadius, linkedGpsID,
		)
	}

	c.JSON(200, gin.H{"ok": true})
}

// DronesDelete removes a drone and its linked entries
func (a *API) DronesDelete(c *gin.Context) {
	id := c.Param("id")

	// Read linked IDs
	var linkedDevID, linkedGpsID int
	a.db.QueryRow(`SELECT linked_device_id, linked_gps_device_id FROM drones WHERE id=?`, id).Scan(&linkedDevID, &linkedGpsID)

	// Delete linked GPS device and related data
	if linkedGpsID > 0 {
		a.db.Exec(`DELETE FROM gps_history WHERE device_id=?`, linkedGpsID)
		a.db.Exec(`DELETE FROM gps_fence_alerts WHERE device_id=?`, linkedGpsID)
		a.db.Exec(`DELETE FROM battery_records WHERE device_id=?`, linkedGpsID)
		a.db.Exec(`DELETE FROM battery_alerts WHERE device_id=?`, linkedGpsID)
		a.db.Exec(`DELETE FROM mission_logs WHERE mission_id IN (SELECT id FROM flight_missions WHERE device_id=?)`, linkedGpsID)
		a.db.Exec(`DELETE FROM flight_missions WHERE device_id=?`, linkedGpsID)
		a.db.Exec(`DELETE FROM gps_devices WHERE id=?`, linkedGpsID)
	}

	// Delete linked remote device
	if linkedDevID > 0 {
		a.db.Exec(`DELETE FROM devices WHERE id=?`, linkedDevID)
	}

	// Delete drone
	_, err := a.db.Exec(`DELETE FROM drones WHERE id=?`, id)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// DronesStats returns summary statistics
func (a *API) DronesStats(c *gin.Context) {
	var total, online, offline int
	a.db.QueryRow(`SELECT COUNT(*) FROM drones`).Scan(&total)
	a.db.QueryRow(`SELECT COUNT(*) FROM drones WHERE status='online'`).Scan(&online)
	offline = total - online

	// count drones whose linked GPS device is actually pushing data (last 30s)
	var gpsActive int
	a.db.QueryRow(`SELECT COUNT(*) FROM drones d JOIN gps_devices g ON d.linked_gps_device_id = g.id WHERE datetime(g.last_update) >= datetime('now','-30 seconds')`).Scan(&gpsActive)

	c.JSON(200, gin.H{
		"total": total, "online": online, "offline": offline, "gps_active": gpsActive,
	})
}
