package handlers

import (
	"database/sql"
	"math"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// ==================== GPS / Location API ====================

// GpsDevicesList returns all GPS-tracked devices with optional filters
func (a *API) GpsDevicesList(c *gin.Context) {
	name := strings.TrimSpace(c.Query("name"))
	status := strings.TrimSpace(c.Query("status"))
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 50
	}

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

	wc := strings.Join(where, " AND ")

	var total int
	_ = a.db.QueryRow("SELECT COUNT(*) FROM gps_devices WHERE "+wc, args...).Scan(&total)

	offset := (page - 1) * pageSize
	q := `SELECT id, name, device_type, latitude, longitude, altitude, speed, heading, accuracy, status, fence_enabled, fence_lat, fence_lng, fence_radius, last_update, created_at, COALESCE(agent_id,'') FROM gps_devices WHERE ` + wc + ` ORDER BY datetime(last_update) DESC LIMIT ? OFFSET ?`
	qArgs := append(args, pageSize, offset)
	rows, err := a.db.Query(q, qArgs...)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	items := []gin.H{}
	for rows.Next() {
		var id, fenceEnabled int
		var name, devType, status, agentID string
		var lat, lng, alt, speed, heading, accuracy, fLat, fLng, fRadius float64
		var lastUpdate, created sql.NullString
		if err := rows.Scan(&id, &name, &devType, &lat, &lng, &alt, &speed, &heading, &accuracy, &status, &fenceEnabled, &fLat, &fLng, &fRadius, &lastUpdate, &created, &agentID); err == nil {
			items = append(items, gin.H{
				"id": id, "name": name, "device_type": devType,
				"latitude": lat, "longitude": lng, "altitude": alt,
				"speed": speed, "heading": heading, "accuracy": accuracy,
				"status": status, "fence_enabled": fenceEnabled == 1,
				"fence_lat": fLat, "fence_lng": fLng, "fence_radius": fRadius,
				"last_update": lastUpdate.String, "created_at": created.String,
				"agent_id": agentID,
			})
		}
	}
	c.JSON(200, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize})
}

// GpsDevicesCreate adds a new GPS-tracked device
func (a *API) GpsDevicesCreate(c *gin.Context) {
	var p struct {
		Name         string  `json:"name"`
		AgentID      string  `json:"agent_id"`
		Latitude     float64 `json:"latitude"`
		Longitude    float64 `json:"longitude"`
		Altitude     float64 `json:"altitude"`
		FenceEnabled bool    `json:"fence_enabled"`
		FenceLat     float64 `json:"fence_lat"`
		FenceLng     float64 `json:"fence_lng"`
		FenceRadius  float64 `json:"fence_radius"`
	}
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"error": "bad json"})
		return
	}
	p.Name = strings.TrimSpace(p.Name)
	p.AgentID = strings.TrimSpace(p.AgentID)
	if p.Name == "" {
		c.JSON(400, gin.H{"error": "设备名称不能为空"})
		return
	}

	fenceEn := 0
	if p.FenceEnabled {
		fenceEn = 1
	}

	res, err := a.db.Exec(
		`INSERT INTO gps_devices(name, agent_id, device_type, latitude, longitude, altitude, speed, heading, accuracy, status, fence_enabled, fence_lat, fence_lng, fence_radius, last_update) VALUES(?,?,?,?,?,?,0,0,0,'在线',?,?,?,?,CURRENT_TIMESTAMP)`,
		p.Name, p.AgentID, "无人机", p.Latitude, p.Longitude, p.Altitude, fenceEn, p.FenceLat, p.FenceLng, p.FenceRadius,
	)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	id, _ := res.LastInsertId()
	// record initial position in history
	a.db.Exec(`INSERT INTO gps_history(device_id, latitude, longitude, altitude, speed, heading) VALUES(?,?,?,?,0,0)`, id, p.Latitude, p.Longitude, p.Altitude)
	c.JSON(200, gin.H{"ok": true, "id": id})
}

// GpsDevicesGet returns a single device
func (a *API) GpsDevicesGet(c *gin.Context) {
	id := c.Param("id")
	var d struct {
		ID, FenceEnabled                                             int
		Name, DevType, Status, AgentID                               string
		Lat, Lng, Alt, Speed, Heading, Accuracy, FLat, FLng, FRadius float64
		LastUpdate, Created                                          sql.NullString
	}
	err := a.db.QueryRow(
		`SELECT id, name, device_type, latitude, longitude, altitude, speed, heading, accuracy, status, fence_enabled, fence_lat, fence_lng, fence_radius, last_update, created_at, COALESCE(agent_id,'') FROM gps_devices WHERE id=?`, id,
	).Scan(&d.ID, &d.Name, &d.DevType, &d.Lat, &d.Lng, &d.Alt, &d.Speed, &d.Heading, &d.Accuracy, &d.Status, &d.FenceEnabled, &d.FLat, &d.FLng, &d.FRadius, &d.LastUpdate, &d.Created, &d.AgentID)
	if err != nil {
		c.JSON(404, gin.H{"error": "设备不存在"})
		return
	}
	c.JSON(200, gin.H{
		"id": d.ID, "name": d.Name, "device_type": d.DevType,
		"latitude": d.Lat, "longitude": d.Lng, "altitude": d.Alt,
		"speed": d.Speed, "heading": d.Heading, "accuracy": d.Accuracy,
		"status": d.Status, "fence_enabled": d.FenceEnabled == 1,
		"fence_lat": d.FLat, "fence_lng": d.FLng, "fence_radius": d.FRadius,
		"last_update": d.LastUpdate.String, "created_at": d.Created.String,
		"agent_id": d.AgentID,
	})
}

// GpsDevicesUpdate edits a GPS device
func (a *API) GpsDevicesUpdate(c *gin.Context) {
	id := c.Param("id")
	var p struct {
		Name         string  `json:"name"`
		AgentID      string  `json:"agent_id"`
		FenceEnabled bool    `json:"fence_enabled"`
		FenceLat     float64 `json:"fence_lat"`
		FenceLng     float64 `json:"fence_lng"`
		FenceRadius  float64 `json:"fence_radius"`
	}
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"error": "bad json"})
		return
	}
	p.Name = strings.TrimSpace(p.Name)
	p.AgentID = strings.TrimSpace(p.AgentID)
	if p.Name == "" {
		c.JSON(400, gin.H{"error": "设备名称不能为空"})
		return
	}
	fenceEn := 0
	if p.FenceEnabled {
		fenceEn = 1
	}
	_, err := a.db.Exec(
		`UPDATE gps_devices SET name=?, agent_id=?, fence_enabled=?, fence_lat=?, fence_lng=?, fence_radius=? WHERE id=?`,
		p.Name, p.AgentID, fenceEn, p.FenceLat, p.FenceLng, p.FenceRadius, id,
	)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// GpsDevicesDelete removes a GPS device and its history
func (a *API) GpsDevicesDelete(c *gin.Context) {
	id := c.Param("id")
	a.db.Exec(`DELETE FROM gps_history WHERE device_id=?`, id)
	a.db.Exec(`DELETE FROM gps_fence_alerts WHERE device_id=?`, id)
	a.db.Exec(`DELETE FROM battery_records WHERE device_id=?`, id)
	a.db.Exec(`DELETE FROM battery_alerts WHERE device_id=?`, id)
	_, err := a.db.Exec(`DELETE FROM gps_devices WHERE id=?`, id)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// GpsDevicesPush updates a device's GPS position (simulates agent push)
func (a *API) GpsDevicesPush(c *gin.Context) {
	id := c.Param("id")
	var p struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
		Altitude  float64 `json:"altitude"`
		Speed     float64 `json:"speed"`
		Heading   float64 `json:"heading"`
		Accuracy  float64 `json:"accuracy"`
	}
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"error": "bad json"})
		return
	}

	_, err := a.db.Exec(
		`UPDATE gps_devices SET latitude=?, longitude=?, altitude=?, speed=?, heading=?, accuracy=?, last_update=CURRENT_TIMESTAMP WHERE id=?`,
		p.Latitude, p.Longitude, p.Altitude, p.Speed, p.Heading, p.Accuracy, id,
	)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	// record history
	a.db.Exec(`INSERT INTO gps_history(device_id, latitude, longitude, altitude, speed, heading) VALUES(?,?,?,?,?,?)`,
		id, p.Latitude, p.Longitude, p.Altitude, p.Speed, p.Heading)

	// check geofence
	var fenceEnabled int
	var fLat, fLng, fRadius float64
	var devName string
	err = a.db.QueryRow(`SELECT name, fence_enabled, fence_lat, fence_lng, fence_radius FROM gps_devices WHERE id=?`, id).Scan(&devName, &fenceEnabled, &fLat, &fLng, &fRadius)
	if err == nil && fenceEnabled == 1 && fRadius > 0 {
		dist := haversine(p.Latitude, p.Longitude, fLat, fLng)
		if dist > fRadius {
			a.db.Exec(`INSERT INTO gps_fence_alerts(device_id, device_name, latitude, longitude, fence_lat, fence_lng, fence_radius, distance, message) VALUES(?,?,?,?,?,?,?,?,?)`,
				id, devName, p.Latitude, p.Longitude, fLat, fLng, fRadius, dist,
				devName+" 已超出电子围栏范围，距离中心 "+strconv.FormatFloat(dist, 'f', 1, 64)+"m")
			// also insert into alerts table for global alerting
			a.db.Exec(`INSERT INTO alerts(category, severity, message) VALUES(?,?,?)`,
				"围栏报警", "warning", devName+" 超出电子围栏 (距离: "+strconv.FormatFloat(dist, 'f', 1, 64)+"m)")
		}
	}

	c.JSON(200, gin.H{"ok": true})
}

// GpsDevicesHistory returns position history for a device
func (a *API) GpsDevicesHistory(c *gin.Context) {
	id := c.Param("id")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "200"))
	if limit < 1 || limit > 2000 {
		limit = 200
	}
	rows, err := a.db.Query(`SELECT id, device_id, latitude, longitude, altitude, speed, heading, created_at FROM gps_history WHERE device_id=? ORDER BY datetime(created_at) DESC LIMIT ?`, id, limit)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	items := []gin.H{}
	for rows.Next() {
		var hid, did int
		var lat, lng, alt, spd, hdg float64
		var created sql.NullString
		if err := rows.Scan(&hid, &did, &lat, &lng, &alt, &spd, &hdg, &created); err == nil {
			items = append(items, gin.H{
				"id": hid, "device_id": did, "latitude": lat, "longitude": lng,
				"altitude": alt, "speed": spd, "heading": hdg, "created_at": created.String,
			})
		}
	}
	c.JSON(200, gin.H{"items": items})
}

// GpsFenceAlerts returns geofence alert records
func (a *API) GpsFenceAlerts(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 20
	}

	var total int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM gps_fence_alerts`).Scan(&total)

	offset := (page - 1) * pageSize
	rows, err := a.db.Query(`SELECT id, device_id, device_name, latitude, longitude, fence_lat, fence_lng, fence_radius, distance, message, acknowledged, created_at FROM gps_fence_alerts ORDER BY datetime(created_at) DESC LIMIT ? OFFSET ?`, pageSize, offset)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	items := []gin.H{}
	for rows.Next() {
		var aid, did, ack int
		var devName, msg string
		var lat, lng, fLat, fLng, fRadius, dist float64
		var created sql.NullString
		if err := rows.Scan(&aid, &did, &devName, &lat, &lng, &fLat, &fLng, &fRadius, &dist, &msg, &ack, &created); err == nil {
			items = append(items, gin.H{
				"id": aid, "device_id": did, "device_name": devName,
				"latitude": lat, "longitude": lng,
				"fence_lat": fLat, "fence_lng": fLng, "fence_radius": fRadius,
				"distance": dist, "message": msg, "acknowledged": ack == 1,
				"created_at": created.String,
			})
		}
	}
	c.JSON(200, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize})
}

// GpsFenceAlertAck acknowledges a fence alert
func (a *API) GpsFenceAlertAck(c *gin.Context) {
	id := c.Param("id")
	_, err := a.db.Exec(`UPDATE gps_fence_alerts SET acknowledged=1 WHERE id=?`, id)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// GpsStats returns GPS statistics
func (a *API) GpsStats(c *gin.Context) {
	var total, online, offline, fenceEnabled, alertCount int
	a.db.QueryRow(`SELECT COUNT(*) FROM gps_devices`).Scan(&total)
	a.db.QueryRow(`SELECT COUNT(*) FROM gps_devices WHERE status='在线'`).Scan(&online)
	a.db.QueryRow(`SELECT COUNT(*) FROM gps_devices WHERE status='离线'`).Scan(&offline)
	a.db.QueryRow(`SELECT COUNT(*) FROM gps_devices WHERE fence_enabled=1`).Scan(&fenceEnabled)
	a.db.QueryRow(`SELECT COUNT(*) FROM gps_fence_alerts WHERE acknowledged=0`).Scan(&alertCount)
	c.JSON(200, gin.H{
		"total": total, "online": online, "offline": offline,
		"fence_enabled": fenceEnabled, "alert_count": alertCount,
	})
}

// GpsPushByAgent accepts GPS data from a remote agent identified by agent_id (hostname).
// If no gps_device with that name exists, one is auto-created.
// This enables hw-agent on drones to push GPS data without knowing the database ID.
func (a *API) GpsPushByAgent(c *gin.Context) {
	var p struct {
		AgentID   string  `json:"agent_id"`
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
		Altitude  float64 `json:"altitude"`
		Speed     float64 `json:"speed"`
		Heading   float64 `json:"heading"`
		Accuracy  float64 `json:"accuracy"`
	}
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"error": "bad json"})
		return
	}
	p.AgentID = strings.TrimSpace(p.AgentID)
	if p.AgentID == "" {
		c.JSON(400, gin.H{"error": "agent_id required"})
		return
	}

	// Find existing gps_device: first by agent_id column, then fall back to name
	var deviceID int
	err := a.db.QueryRow(`SELECT id FROM gps_devices WHERE agent_id = ? AND agent_id != ''`, p.AgentID).Scan(&deviceID)
	if err != nil {
		// Fallback: match by name
		err = a.db.QueryRow(`SELECT id FROM gps_devices WHERE name = ?`, p.AgentID).Scan(&deviceID)
	}
	if err != nil {
		// Auto-create a new GPS device for this agent
		res, err2 := a.db.Exec(
			`INSERT INTO gps_devices(name, agent_id, device_type, latitude, longitude, altitude, speed, heading, accuracy, status, fence_enabled, last_update) VALUES(?,?,?,?,?,?,?,?,?,?,0,CURRENT_TIMESTAMP)`,
			p.AgentID, p.AgentID, "无人机", p.Latitude, p.Longitude, p.Altitude, p.Speed, p.Heading, p.Accuracy, "在线",
		)
		if err2 != nil {
			c.JSON(500, gin.H{"error": err2.Error()})
			return
		}
		newID, _ := res.LastInsertId()
		deviceID = int(newID)
		// record initial history
		a.db.Exec(`INSERT INTO gps_history(device_id, latitude, longitude, altitude, speed, heading) VALUES(?,?,?,?,?,?)`,
			deviceID, p.Latitude, p.Longitude, p.Altitude, p.Speed, p.Heading)
		c.JSON(200, gin.H{"ok": true, "id": deviceID, "created": true})
		return
	}

	// Update existing device position
	_, err = a.db.Exec(
		`UPDATE gps_devices SET latitude=?, longitude=?, altitude=?, speed=?, heading=?, accuracy=?, status='在线', last_update=CURRENT_TIMESTAMP WHERE id=?`,
		p.Latitude, p.Longitude, p.Altitude, p.Speed, p.Heading, p.Accuracy, deviceID,
	)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	// Record history
	a.db.Exec(`INSERT INTO gps_history(device_id, latitude, longitude, altitude, speed, heading) VALUES(?,?,?,?,?,?)`,
		deviceID, p.Latitude, p.Longitude, p.Altitude, p.Speed, p.Heading)

	// Check geofence
	var fenceEnabled int
	var fLat, fLng, fRadius float64
	var devName string
	err = a.db.QueryRow(`SELECT name, fence_enabled, fence_lat, fence_lng, fence_radius FROM gps_devices WHERE id=?`, deviceID).Scan(&devName, &fenceEnabled, &fLat, &fLng, &fRadius)
	if err == nil && fenceEnabled == 1 && fRadius > 0 {
		dist := haversine(p.Latitude, p.Longitude, fLat, fLng)
		if dist > fRadius {
			a.db.Exec(`INSERT INTO gps_fence_alerts(device_id, device_name, latitude, longitude, fence_lat, fence_lng, fence_radius, distance, message) VALUES(?,?,?,?,?,?,?,?,?)`,
				deviceID, devName, p.Latitude, p.Longitude, fLat, fLng, fRadius, dist,
				devName+" 已超出电子围栏范围，距离中心 "+strconv.FormatFloat(dist, 'f', 1, 64)+"m")
			a.db.Exec(`INSERT INTO alerts(category, severity, message) VALUES(?,?,?)`,
				"围栏报警", "warning", devName+" 超出电子围栏 (距离: "+strconv.FormatFloat(dist, 'f', 1, 64)+"m)")
		}
	}

	c.JSON(200, gin.H{"ok": true, "id": deviceID})
}

// haversine calculates the distance in meters between two lat/lng points
func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371000 // Earth radius in meters
	dLat := (lat2 - lat1) * math.Pi / 180
	dLon := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return R * c
}
