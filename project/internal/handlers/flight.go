package handlers

import (
	"database/sql"
	"encoding/json"
	"log"
	"strconv"
	"strings"
	"time"
	"unicode"

	"smartcontrol/internal/amap"

	"github.com/gin-gonic/gin"
)

// ==================== Flight Mission Management API ====================

// FlightMissionsList returns flight missions with optional filters and pagination
func (a *API) FlightMissionsList(c *gin.Context) {
	name := strings.TrimSpace(c.Query("name"))
	status := strings.TrimSpace(c.Query("status"))
	target := strings.TrimSpace(c.Query("target"))
	dateFrom := strings.TrimSpace(c.Query("date_from"))
	dateTo := strings.TrimSpace(c.Query("date_to"))
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 20
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
	if target != "" {
		where = append(where, "target LIKE ?")
		args = append(args, "%"+target+"%")
	}
	if dateFrom != "" {
		where = append(where, "created_at >= ?")
		args = append(args, dateFrom)
	}
	if dateTo != "" {
		where = append(where, "created_at <= ?")
		args = append(args, dateTo+" 23:59:59")
	}

	wc := strings.Join(where, " AND ")

	// count
	var total int
	_ = a.db.QueryRow("SELECT COUNT(*) FROM flight_missions WHERE "+wc, args...).Scan(&total)

	// query — also fetch latest GPS position and battery level for each mission's device
	offset := (page - 1) * pageSize
	q := `SELECT f.id, f.name, f.route, f.target, f.estimated_duration, f.status, f.current_phase, f.progress,
		f.start_time, f.end_time, f.description, f.created_at, f.updated_at, f.device_id, COALESCE(g.name,''),
		COALESCE(g.latitude,0), COALESCE(g.longitude,0), COALESCE(g.altitude,0),
		COALESCE(g.speed,0), COALESCE(g.heading,0), COALESCE(g.status,''),
		COALESCE(g.last_update,''),
		COALESCE((SELECT level FROM battery_records WHERE device_id=f.device_id ORDER BY id DESC LIMIT 1),-1)
		FROM flight_missions f LEFT JOIN gps_devices g ON f.device_id=g.id WHERE ` + wc + ` ORDER BY datetime(f.created_at) DESC LIMIT ? OFFSET ?`
	qArgs := append(args, pageSize, offset)
	rows, err := a.db.Query(q, qArgs...)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	items := []gin.H{}
	for rows.Next() {
		var id, progress, deviceID, batteryLevel int
		var name, route, target, estDur, status, phase, desc, droneName, gpsStatus string
		var gpsLat, gpsLng, gpsAlt, gpsSpeed, gpsHeading float64
		var startTime, endTime, created, updated, gpsLastUpdate sql.NullString
		if err := rows.Scan(&id, &name, &route, &target, &estDur, &status, &phase, &progress,
			&startTime, &endTime, &desc, &created, &updated, &deviceID, &droneName,
			&gpsLat, &gpsLng, &gpsAlt, &gpsSpeed, &gpsHeading, &gpsStatus, &gpsLastUpdate,
			&batteryLevel); err == nil {
			item := gin.H{
				"id": id, "name": name, "route": route, "target": target,
				"estimated_duration": estDur, "status": status, "current_phase": phase,
				"progress": progress, "start_time": startTime.String, "end_time": endTime.String,
				"description": desc, "created_at": created.String, "updated_at": updated.String,
				"device_id": deviceID, "drone_name": droneName,
			}
			if deviceID > 0 {
				item["gps"] = gin.H{
					"latitude": gpsLat, "longitude": gpsLng, "altitude": gpsAlt,
					"speed": gpsSpeed, "heading": gpsHeading, "status": gpsStatus,
					"last_update": gpsLastUpdate.String,
				}
				if batteryLevel >= 0 {
					item["battery_level"] = batteryLevel
				}
			}
			items = append(items, item)
		}
	}
	c.JSON(200, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize})
}

// FlightMissionsCreate creates a new flight mission
func (a *API) FlightMissionsCreate(c *gin.Context) {
	var p struct {
		Name              string `json:"name"`
		Route             string `json:"route"`
		Target            string `json:"target"`
		EstimatedDuration string `json:"estimated_duration"`
		Description       string `json:"description"`
		DeviceID          int    `json:"device_id"`
		WaypointsJSON     string `json:"waypoints_json"`
	}
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"error": "bad json"})
		return
	}
	p.Name = strings.TrimSpace(p.Name)
	p.Route = strings.TrimSpace(p.Route)
	p.Target = strings.TrimSpace(p.Target)
	if p.Name == "" {
		c.JSON(400, gin.H{"error": "任务名称不能为空"})
		return
	}
	if p.Route == "" {
		c.JSON(400, gin.H{"error": "飞行路线不能为空"})
		return
	}

	// 检查任务名称是否重复
	var existCount int
	a.db.QueryRow(`SELECT COUNT(*) FROM flight_missions WHERE name=?`, p.Name).Scan(&existCount)
	if existCount > 0 {
		c.JSON(400, gin.H{"error": "任务名称已存在，请修改名称"})
		return
	}

	res, err := a.db.Exec(
		`INSERT INTO flight_missions(name, route, target, estimated_duration, description, status, current_phase, progress, device_id, waypoints_json) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		p.Name, p.Route, p.Target, p.EstimatedDuration, p.Description, "待起飞", "待命", 0, p.DeviceID, p.WaypointsJSON,
	)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	id, _ := res.LastInsertId()

	// add mission log
	a.db.Exec(`INSERT INTO mission_logs(mission_id, phase, message) VALUES(?,?,?)`, id, "创建", "任务已创建: "+p.Name)

	hub.Broadcast("flight", WSEvent{Type: "flight_created", Data: gin.H{"id": id}})
	c.JSON(200, gin.H{"ok": true, "id": id})
}

// FlightMissionsGet returns a single mission by ID, including GPS position and battery info
func (a *API) FlightMissionsGet(c *gin.Context) {
	id := c.Param("id")
	var m struct {
		ID                                               int
		Name, Route, Target, EstDur, Status, Phase, Desc string
		Progress, DeviceID                               int
		DroneName                                        string
		WaypointsJSON                                    sql.NullString
		StartTime, EndTime, Created, Updated             sql.NullString
	}
	err := a.db.QueryRow(
		`SELECT f.id, f.name, f.route, f.target, f.estimated_duration, f.status, f.current_phase, f.progress, f.start_time, f.end_time, f.description, f.created_at, f.updated_at, f.device_id, COALESCE(g.name,''), COALESCE(f.waypoints_json,'') FROM flight_missions f LEFT JOIN gps_devices g ON f.device_id=g.id WHERE f.id=?`, id,
	).Scan(&m.ID, &m.Name, &m.Route, &m.Target, &m.EstDur, &m.Status, &m.Phase, &m.Progress, &m.StartTime, &m.EndTime, &m.Desc, &m.Created, &m.Updated, &m.DeviceID, &m.DroneName, &m.WaypointsJSON)
	if err != nil {
		c.JSON(404, gin.H{"error": "任务不存在"})
		return
	}
	result := gin.H{
		"id": m.ID, "name": m.Name, "route": m.Route, "target": m.Target,
		"estimated_duration": m.EstDur, "status": m.Status, "current_phase": m.Phase,
		"progress": m.Progress, "start_time": m.StartTime.String, "end_time": m.EndTime.String,
		"description": m.Desc, "created_at": m.Created.String, "updated_at": m.Updated.String,
		"device_id": m.DeviceID, "drone_name": m.DroneName,
		"waypoints_json": m.WaypointsJSON.String,
	}

	// Attach GPS position if device is linked
	if m.DeviceID > 0 {
		var gpsLat, gpsLng, gpsAlt, gpsSpeed, gpsHeading float64
		var gpsStatus string
		var gpsLastUpdate sql.NullString
		if a.db.QueryRow(`SELECT latitude, longitude, altitude, speed, heading, status, last_update FROM gps_devices WHERE id=?`, m.DeviceID).Scan(
			&gpsLat, &gpsLng, &gpsAlt, &gpsSpeed, &gpsHeading, &gpsStatus, &gpsLastUpdate) == nil {
			result["gps"] = gin.H{
				"latitude": gpsLat, "longitude": gpsLng, "altitude": gpsAlt,
				"speed": gpsSpeed, "heading": gpsHeading, "status": gpsStatus,
				"last_update": gpsLastUpdate.String,
			}
		}
		// Attach latest battery info
		var batLevel, batHealth int
		var batVoltage, batTemp float64
		var batStatus, batRemaining string
		if a.db.QueryRow(`SELECT level, health, voltage, temperature, status, remaining_time FROM battery_records WHERE device_id=? ORDER BY id DESC LIMIT 1`, m.DeviceID).Scan(
			&batLevel, &batHealth, &batVoltage, &batTemp, &batStatus, &batRemaining) == nil {
			result["battery"] = gin.H{
				"level": batLevel, "health": batHealth, "voltage": batVoltage,
				"temperature": batTemp, "status": batStatus, "remaining_time": batRemaining,
			}
		}
	}

	c.JSON(200, result)
}

// FlightMissionsUpdate edits a flight mission
func (a *API) FlightMissionsUpdate(c *gin.Context) {
	id := c.Param("id")
	var p struct {
		Name              string `json:"name"`
		Route             string `json:"route"`
		Target            string `json:"target"`
		EstimatedDuration string `json:"estimated_duration"`
		Description       string `json:"description"`
		DeviceID          int    `json:"device_id"`
	}
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"error": "bad json"})
		return
	}
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		c.JSON(400, gin.H{"error": "任务名称不能为空"})
		return
	}

	// 检查任务名称是否与其他任务重复（排除自身）
	var existCount int
	a.db.QueryRow(`SELECT COUNT(*) FROM flight_missions WHERE name=? AND id!=?`, p.Name, id).Scan(&existCount)
	if existCount > 0 {
		c.JSON(400, gin.H{"error": "任务名称已存在，请修改名称"})
		return
	}

	_, err := a.db.Exec(
		`UPDATE flight_missions SET name=?, route=?, target=?, estimated_duration=?, description=?, device_id=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		p.Name, p.Route, p.Target, p.EstimatedDuration, p.Description, p.DeviceID, id,
	)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	hub.Broadcast("flight", WSEvent{Type: "flight_updated", Data: gin.H{"id": id}})
	c.JSON(200, gin.H{"ok": true})
}

// FlightMissionStart begins autonomous execution of a mission.
// The drone follows the planned route waypoints, then auto-returns to base.
func (a *API) FlightMissionStart(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if id <= 0 {
		c.JSON(400, gin.H{"error": "invalid mission id"})
		return
	}
	var deviceID int
	var status, route string
	err := a.db.QueryRow(`SELECT device_id, status, route FROM flight_missions WHERE id=?`, id).Scan(&deviceID, &status, &route)
	if err == sql.ErrNoRows {
		c.JSON(404, gin.H{"error": "mission not found"})
		return
	}
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	if deviceID <= 0 {
		c.JSON(400, gin.H{"error": "任务未分配无人机，请先选择执行无人机"})
		return
	}
	if status == "飞行中" || status == "返航中" {
		c.JSON(400, gin.H{"error": "任务已在执行中"})
		return
	}
	if status == "已完成" {
		c.JSON(400, gin.H{"error": "任务已完成"})
		return
	}

	// ---- Step 1: Try waypoints_json (most reliable source) ----
	var waypoints []MissionWaypoint
	var wpJSON sql.NullString
	a.db.QueryRow(`SELECT waypoints_json FROM flight_missions WHERE id=?`, id).Scan(&wpJSON)
	if wpJSON.Valid && wpJSON.String != "" && wpJSON.String != "null" && wpJSON.String != "[]" {
		type wpObj struct {
			Lat float64 `json:"lat"`
			Lng float64 `json:"lng"`
		}
		var items []wpObj
		if json.Unmarshal([]byte(wpJSON.String), &items) == nil {
			for _, w := range items {
				if w.Lat != 0 && w.Lng != 0 {
					waypoints = append(waypoints, MissionWaypoint{Lat: w.Lat, Lng: w.Lng, Alt: 110})
				}
			}
		}
		// Also try [[lng,lat],[lng,lat]] array-of-arrays format
		if len(waypoints) == 0 {
			var arrays [][]float64
			if json.Unmarshal([]byte(wpJSON.String), &arrays) == nil {
				for _, arr := range arrays {
					if len(arr) >= 2 && arr[0] != 0 && arr[1] != 0 {
						waypoints = append(waypoints, MissionWaypoint{Lat: arr[1], Lng: arr[0], Alt: 110})
					}
				}
			}
		}
	}

	// ---- Step 2: Try parsing route as coordinate pairs ----
	if len(waypoints) == 0 {
		parts := strings.Split(route, "→")
		if len(parts) < 2 {
			parts = strings.Split(route, "->")
		}
		for _, seg := range parts {
			seg = strings.TrimSpace(seg)
			if seg == "" {
				continue
			}
			coords := strings.Split(seg, ",")
			if len(coords) >= 2 {
				lat := parseFloat(strings.TrimSpace(coords[0]))
				lng := parseFloat(strings.TrimSpace(coords[1]))
				if lat != 0 && lng != 0 {
					alt := 110.0
					if len(coords) >= 3 {
						if v := parseFloat(strings.TrimSpace(coords[2])); v > 0 {
							alt = v
						}
					}
					waypoints = append(waypoints, MissionWaypoint{Lat: lat, Lng: lng, Alt: alt})
				}
			}
		}
	}

	// ---- Step 3: Route contains address text → geocode via AMap ----
	if len(waypoints) == 0 && containsChinese(route) {
		amapClient := amap.NewClient()
		if amapClient.Available() {
			parts := strings.Split(route, "→")
			if len(parts) < 2 {
				parts = strings.Split(route, "->")
			}
			seen := map[string]bool{}
			for _, seg := range parts {
				seg = strings.TrimSpace(seg)
				if seg == "" || seen[seg] {
					continue
				}
				seen[seg] = true
				candidates, err := amapClient.Geocode(seg, "")
				if err == nil && len(candidates) > 0 {
					waypoints = append(waypoints, MissionWaypoint{
						Lat: candidates[0].Lat, Lng: candidates[0].Lon, Alt: 110,
					})
					log.Printf("[FlightMission] Geocoded '%s' → (%.6f, %.6f)", seg, candidates[0].Lat, candidates[0].Lon)
				}
			}
		}
	}

	// ---- Step 4: Fallback — generate patrol pattern around drone's current position ----
	if len(waypoints) == 0 {
		var droneLat, droneLng float64
		a.db.QueryRow(`SELECT latitude, longitude FROM gps_devices WHERE id=?`, deviceID).Scan(&droneLat, &droneLng)
		if droneLat != 0 && droneLng != 0 {
			offset := 0.003 // ~300m
			waypoints = []MissionWaypoint{
				{Lat: droneLat + offset, Lng: droneLng, Alt: 110},
				{Lat: droneLat + offset, Lng: droneLng + offset, Alt: 110},
				{Lat: droneLat, Lng: droneLng + offset, Alt: 110},
				{Lat: droneLat - offset, Lng: droneLng, Alt: 110},
			}
			log.Printf("[FlightMission] Using fallback patrol pattern around drone position (%.4f, %.4f)", droneLat, droneLng)
		}
	}

	if len(waypoints) == 0 {
		c.JSON(400, gin.H{"error": "无法从航线中解析出有效的航点坐标，请确保航线包含有效地址或坐标"})
		return
	}

	// Start the mission on the feeder
	if err := StartMission(id, deviceID, waypoints); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	// Persist parsed waypoints as JSON for mission restoration after restart
	if wpBytes, err := json.Marshal(waypoints); err == nil {
		a.db.Exec(`UPDATE flight_missions SET waypoints_json=? WHERE id=?`, string(wpBytes), id)
	}

	// Update mission status
	a.db.Exec(`UPDATE flight_missions SET status='飞行中', current_phase='起飞', progress=5 WHERE id=?`, id)
	a.db.Exec(`INSERT INTO mission_logs(mission_id, phase, message) VALUES(?,?,?)`, id, "起飞", "任务开始执行，无人机起飞中")

	hub.Broadcast("flight", WSEvent{Type: "flight_started", Data: gin.H{"id": id}})
	c.JSON(200, gin.H{"ok": true, "message": "任务已启动，无人机正在起飞"})
}

func parseFloat(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func containsChinese(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// FlightMissionsDelete deletes a flight mission
func (a *API) FlightMissionsDelete(c *gin.Context) {
	id := c.Param("id")
	a.db.Exec(`DELETE FROM mission_logs WHERE mission_id=?`, id)
	_, err := a.db.Exec(`DELETE FROM flight_missions WHERE id=?`, id)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	hub.Broadcast("flight", WSEvent{Type: "flight_deleted", Data: gin.H{"id": id}})
	c.JSON(200, gin.H{"ok": true})
}

// FlightMissionsUpdatePhase updates the status/phase of a mission (state machine).
// Rule A: If the mission is bound to a device and that device is online (GPS updated
// within the last 60 seconds), manual phase changes are rejected — only agent push
// (/api/flight/missions/push) is allowed to update the phase.
func (a *API) FlightMissionsUpdatePhase(c *gin.Context) {
	id := c.Param("id")
	var p struct {
		Phase string `json:"phase"`
	}
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"error": "bad json"})
		return
	}
	p.Phase = strings.TrimSpace(p.Phase)

	// valid phases: 待命 -> 起飞 -> 巡航 -> 执行任务 -> 返航 -> 降落
	validPhases := map[string]bool{
		"待命": true, "起飞": true, "巡航": true, "执行任务": true, "返航": true, "降落": true,
	}
	if !validPhases[p.Phase] {
		c.JSON(400, gin.H{"error": "无效的飞行阶段"})
		return
	}

	// Rule A: Check if device is online — if so, reject manual phase change
	var deviceID int
	a.db.QueryRow(`SELECT device_id FROM flight_missions WHERE id=?`, id).Scan(&deviceID)
	if deviceID > 0 {
		var lastUpdate sql.NullString
		a.db.QueryRow(`SELECT last_update FROM gps_devices WHERE id=?`, deviceID).Scan(&lastUpdate)
		if lastUpdate.Valid && lastUpdate.String != "" {
			if t, err := time.Parse("2006-01-02 15:04:05", lastUpdate.String); err == nil {
				if time.Since(t) < 60*time.Second {
					c.JSON(403, gin.H{
						"error":     "该任务已绑定在线设备，飞行阶段由无人机自动上报，无法手动修改",
						"device_id": deviceID,
						"rule":      "A",
					})
					return
				}
			}
		}
	}

	// map phase to status and progress
	statusMap := map[string]string{
		"待命": "待起飞", "起飞": "飞行中", "巡航": "飞行中", "执行任务": "飞行中", "返航": "返航中", "降落": "已完成",
	}
	progressMap := map[string]int{
		"待命": 0, "起飞": 10, "巡航": 30, "执行任务": 60, "返航": 80, "降落": 100,
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	status := statusMap[p.Phase]
	progress := progressMap[p.Phase]

	updates := "SET status=?, current_phase=?, progress=?, updated_at=?"
	args := []any{status, p.Phase, progress, now}

	if p.Phase == "起飞" {
		updates += ", start_time=?"
		args = append(args, now)
	}
	if p.Phase == "降落" {
		updates += ", end_time=?"
		args = append(args, now)
	}

	args = append(args, id)
	_, err := a.db.Exec("UPDATE flight_missions "+updates+" WHERE id=?", args...)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	// add mission log
	a.db.Exec(`INSERT INTO mission_logs(mission_id, phase, message) VALUES(?,?,?)`, id, p.Phase, "阶段变更: "+p.Phase)

	hub.Broadcast("flight", WSEvent{Type: "flight_phase", Data: gin.H{"id": id, "status": status, "phase": p.Phase, "progress": progress}})
	c.JSON(200, gin.H{"ok": true, "status": status, "phase": p.Phase, "progress": progress})
}

// FlightMissionPushByAgent accepts flight mission phase updates from a remote agent.
// The agent identifies itself by agent_id (hostname). The handler finds the GPS device
// matching that name, then finds the latest non-completed mission bound to that device,
// and updates its phase/status/progress automatically.
func (a *API) FlightMissionPushByAgent(c *gin.Context) {
	var p struct {
		AgentID  string `json:"agent_id"`
		Phase    string `json:"phase"`
		Progress int    `json:"progress"`
	}
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"error": "bad json"})
		return
	}
	p.AgentID = strings.TrimSpace(p.AgentID)
	p.Phase = strings.TrimSpace(p.Phase)
	if p.AgentID == "" {
		c.JSON(400, gin.H{"error": "agent_id required"})
		return
	}

	// valid phases
	validPhases := map[string]bool{
		"待命": true, "起飞": true, "巡航": true, "执行任务": true, "返航": true, "降落": true,
	}
	if !validPhases[p.Phase] {
		c.JSON(400, gin.H{"error": "无效的飞行阶段: " + p.Phase})
		return
	}

	// Find GPS device: first by agent_id column, then fall back to name
	var deviceID int
	err := a.db.QueryRow(`SELECT id FROM gps_devices WHERE agent_id = ? AND agent_id != ''`, p.AgentID).Scan(&deviceID)
	if err != nil {
		err = a.db.QueryRow(`SELECT id FROM gps_devices WHERE name = ?`, p.AgentID).Scan(&deviceID)
	}
	if err != nil {
		c.JSON(404, gin.H{"error": "未找到匹配的设备: " + p.AgentID})
		return
	}

	// Find the latest non-completed mission bound to this device
	var missionID int
	var currentPhase string
	err = a.db.QueryRow(
		`SELECT id, current_phase FROM flight_missions WHERE device_id = ? AND status != '已完成' ORDER BY datetime(created_at) DESC LIMIT 1`,
		deviceID,
	).Scan(&missionID, &currentPhase)
	if err != nil {
		c.JSON(404, gin.H{"error": "该设备没有进行中的飞行任务"})
		return
	}

	// Only allow forward phase transitions
	phaseOrder := []string{"待命", "起飞", "巡航", "执行任务", "返航", "降落"}
	currentIdx := -1
	newIdx := -1
	for i, ph := range phaseOrder {
		if ph == currentPhase {
			currentIdx = i
		}
		if ph == p.Phase {
			newIdx = i
		}
	}
	if newIdx <= currentIdx {
		// Phase not advancing, just acknowledge without updating
		c.JSON(200, gin.H{"ok": true, "id": missionID, "phase": currentPhase, "skipped": true})
		return
	}

	// Map phase to status and progress
	statusMap := map[string]string{
		"待命": "待起飞", "起飞": "飞行中", "巡航": "飞行中", "执行任务": "飞行中", "返航": "返航中", "降落": "已完成",
	}
	progressMap := map[string]int{
		"待命": 0, "起飞": 10, "巡航": 30, "执行任务": 60, "返航": 80, "降落": 100,
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	status := statusMap[p.Phase]
	progress := progressMap[p.Phase]
	// Allow agent to override progress if provided and larger
	if p.Progress > progress {
		progress = p.Progress
	}

	updates := "SET status=?, current_phase=?, progress=?, updated_at=?"
	args := []any{status, p.Phase, progress, now}

	if p.Phase == "起飞" {
		updates += ", start_time=?"
		args = append(args, now)
	}
	if p.Phase == "降落" {
		updates += ", end_time=?"
		args = append(args, now)
	}

	args = append(args, missionID)
	_, err = a.db.Exec("UPDATE flight_missions "+updates+" WHERE id=?", args...)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	// Add mission log
	a.db.Exec(`INSERT INTO mission_logs(mission_id, phase, message) VALUES(?,?,?)`,
		missionID, p.Phase, "[Agent自动] 阶段变更: "+p.Phase)

	hub.Broadcast("flight", WSEvent{Type: "flight_phase", Data: gin.H{"id": missionID, "status": status, "phase": p.Phase, "progress": progress}})
	c.JSON(200, gin.H{"ok": true, "id": missionID, "status": status, "phase": p.Phase, "progress": progress})
}

// FlightMissionsStats returns statistics
func (a *API) FlightMissionsStats(c *gin.Context) {
	stats := gin.H{}
	var total, pending, flying, returning, completed int
	a.db.QueryRow(`SELECT COUNT(*) FROM flight_missions`).Scan(&total)
	a.db.QueryRow(`SELECT COUNT(*) FROM flight_missions WHERE status='待起飞'`).Scan(&pending)
	a.db.QueryRow(`SELECT COUNT(*) FROM flight_missions WHERE status='飞行中'`).Scan(&flying)
	a.db.QueryRow(`SELECT COUNT(*) FROM flight_missions WHERE status='返航中'`).Scan(&returning)
	a.db.QueryRow(`SELECT COUNT(*) FROM flight_missions WHERE status='已完成'`).Scan(&completed)
	stats["total"] = total
	stats["pending"] = pending
	stats["flying"] = flying
	stats["returning"] = returning
	stats["completed"] = completed

	// phase counts for phase chart
	var pStandby, pTakeoff, pCruise, pExecute, pReturn, pLand int
	a.db.QueryRow(`SELECT COUNT(*) FROM flight_missions WHERE current_phase='待命'`).Scan(&pStandby)
	a.db.QueryRow(`SELECT COUNT(*) FROM flight_missions WHERE current_phase='起飞'`).Scan(&pTakeoff)
	a.db.QueryRow(`SELECT COUNT(*) FROM flight_missions WHERE current_phase='巡航'`).Scan(&pCruise)
	a.db.QueryRow(`SELECT COUNT(*) FROM flight_missions WHERE current_phase='执行任务'`).Scan(&pExecute)
	a.db.QueryRow(`SELECT COUNT(*) FROM flight_missions WHERE current_phase='返航'`).Scan(&pReturn)
	a.db.QueryRow(`SELECT COUNT(*) FROM flight_missions WHERE current_phase='降落'`).Scan(&pLand)
	stats["phase_standby"] = pStandby
	stats["phase_takeoff"] = pTakeoff
	stats["phase_cruise"] = pCruise
	stats["phase_execute"] = pExecute
	stats["phase_return"] = pReturn
	stats["phase_land"] = pLand

	c.JSON(200, stats)
}

// FlightActiveMissions returns a compact list of active (non-completed) missions
// with both drone_id (device_id) and gps_device_id. Used by GPS page to show flight status on map markers.
func (a *API) FlightActiveMissions(c *gin.Context) {
	rows, err := a.db.Query(`SELECT fm.id, fm.name, fm.device_id, COALESCE(d.linked_gps_device_id,0), fm.status, fm.current_phase, fm.progress
		FROM flight_missions fm
		LEFT JOIN drones d ON d.id = fm.device_id
		WHERE fm.status != '已完成' AND fm.device_id > 0`)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	items := []gin.H{}
	for rows.Next() {
		var id, deviceID, gpsDeviceID, progress int
		var name, status, phase string
		if err := rows.Scan(&id, &name, &deviceID, &gpsDeviceID, &status, &phase, &progress); err == nil {
			items = append(items, gin.H{
				"id": id, "name": name, "device_id": deviceID,
				"gps_device_id": gpsDeviceID,
				"status": status, "current_phase": phase, "progress": progress,
			})
		}
	}
	c.JSON(200, gin.H{"items": items})
}

// FlightMissionsLogs returns logs for a specific mission
func (a *API) FlightMissionsLogs(c *gin.Context) {
	id := c.Param("id")
	rows, err := a.db.Query(`SELECT id, mission_id, phase, message, created_at FROM mission_logs WHERE mission_id=? ORDER BY datetime(created_at) DESC`, id)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	items := []gin.H{}
	for rows.Next() {
		var logID, missionID int
		var phase, message string
		var created sql.NullString
		if err := rows.Scan(&logID, &missionID, &phase, &message, &created); err == nil {
			items = append(items, gin.H{
				"id": logID, "mission_id": missionID, "phase": phase,
				"message": message, "created_at": created.String,
			})
		}
	}
	c.JSON(200, gin.H{"items": items})
}

// FlightMissionsImport batch imports missions from JSON
func (a *API) FlightMissionsImport(c *gin.Context) {
	var items []struct {
		Name              string `json:"name"`
		Route             string `json:"route"`
		Target            string `json:"target"`
		EstimatedDuration string `json:"estimated_duration"`
		Description       string `json:"description"`
	}
	if err := c.BindJSON(&items); err != nil {
		c.JSON(400, gin.H{"error": "bad json, expected array"})
		return
	}
	count := 0
	for _, p := range items {
		p.Name = strings.TrimSpace(p.Name)
		if p.Name == "" {
			continue
		}
		_, err := a.db.Exec(
			`INSERT INTO flight_missions(name, route, target, estimated_duration, description, status, current_phase, progress) VALUES(?,?,?,?,?,?,?,?)`,
			p.Name, p.Route, p.Target, p.EstimatedDuration, p.Description, "待起飞", "待命", 0,
		)
		if err == nil {
			count++
		}
	}
	hub.Broadcast("flight", WSEvent{Type: "flight_imported", Data: gin.H{"count": count}})
	c.JSON(200, gin.H{"ok": true, "imported": count})
}

// DroneControl handles drone takeoff/land control commands from the mission UI.
// It updates the mission phase and status accordingly, similar to FlightMissionsUpdatePhase
// but designed specifically for the UI control buttons.
func (a *API) DroneControl(c *gin.Context) {
	missionID := c.Param("id")
	var p struct {
		Action string `json:"action"` // "takeoff" or "land"
	}
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"error": "bad json"})
		return
	}
	p.Action = strings.TrimSpace(strings.ToLower(p.Action))

	var phase string
	switch p.Action {
	case "takeoff":
		phase = "起飞"
	case "land":
		phase = "降落"
	case "return":
		phase = "返航"
	default:
		c.JSON(400, gin.H{"error": "无效的控制指令，支持: takeoff, land, return"})
		return
	}

	// Check mission exists and is not completed
	var currentPhase, currentStatus string
	var deviceID int
	err := a.db.QueryRow(`SELECT current_phase, status, device_id FROM flight_missions WHERE id=?`, missionID).Scan(&currentPhase, &currentStatus, &deviceID)
	if err != nil {
		c.JSON(404, gin.H{"error": "任务不存在"})
		return
	}
	if currentStatus == "已完成" {
		c.JSON(400, gin.H{"error": "任务已完成，无法控制"})
		return
	}

	// Validate phase transition (only allow forward)
	phaseOrder := []string{"待命", "起飞", "巡航", "执行任务", "返航", "降落"}
	currentIdx := -1
	newIdx := -1
	for i, ph := range phaseOrder {
		if ph == currentPhase {
			currentIdx = i
		}
		if ph == phase {
			newIdx = i
		}
	}
	if newIdx <= currentIdx {
		c.JSON(400, gin.H{"error": "无法执行此操作: 当前阶段为 " + currentPhase})
		return
	}

	statusMap := map[string]string{
		"待命": "待起飞", "起飞": "飞行中", "巡航": "飞行中", "执行任务": "飞行中", "返航": "返航中", "降落": "已完成",
	}
	progressMap := map[string]int{
		"待命": 0, "起飞": 10, "巡航": 30, "执行任务": 60, "返航": 80, "降落": 100,
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	status := statusMap[phase]
	progress := progressMap[phase]

	updates := "SET status=?, current_phase=?, progress=?, updated_at=?"
	args := []any{status, phase, progress, now}

	if phase == "起飞" {
		updates += ", start_time=?"
		args = append(args, now)
	}
	if phase == "降落" {
		updates += ", end_time=?"
		args = append(args, now)
	}

	args = append(args, missionID)
	_, err = a.db.Exec("UPDATE flight_missions "+updates+" WHERE id=?", args...)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	// Add mission log
	actionLabel := map[string]string{"takeoff": "启动无人机", "land": "降落无人机", "return": "返航无人机"}
	a.db.Exec(`INSERT INTO mission_logs(mission_id, phase, message) VALUES(?,?,?)`,
		missionID, phase, "[控制指令] "+actionLabel[p.Action])

	hub.Broadcast("flight", WSEvent{Type: "flight_phase", Data: gin.H{"id": missionID, "status": status, "phase": phase, "progress": progress, "action": p.Action}})
	c.JSON(200, gin.H{"ok": true, "status": status, "phase": phase, "progress": progress})
}

// FlightStream is a WebSocket endpoint for real-time flight mission event push.
// Clients connect and receive events whenever flight mission data changes.
func (a *API) FlightStream(c *gin.Context) {
	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	hub.Subscribe("flight", ws)
	defer func() {
		hub.Unsubscribe("flight", ws)
		ws.Close()
	}()
	// keep connection alive by reading (handles pings/close frames)
	for {
		if _, _, err := ws.ReadMessage(); err != nil {
			break
		}
	}
}
