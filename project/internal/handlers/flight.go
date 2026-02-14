package handlers

import (
	"database/sql"
	"strconv"
	"strings"
	"time"

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

	// query
	offset := (page - 1) * pageSize
	q := "SELECT id, name, route, target, estimated_duration, status, current_phase, progress, start_time, end_time, description, created_at, updated_at FROM flight_missions WHERE " + wc + " ORDER BY datetime(created_at) DESC LIMIT ? OFFSET ?"
	qArgs := append(args, pageSize, offset)
	rows, err := a.db.Query(q, qArgs...)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	items := []gin.H{}
	for rows.Next() {
		var id, progress int
		var name, route, target, estDur, status, phase, desc string
		var startTime, endTime, created, updated sql.NullString
		if err := rows.Scan(&id, &name, &route, &target, &estDur, &status, &phase, &progress, &startTime, &endTime, &desc, &created, &updated); err == nil {
			items = append(items, gin.H{
				"id": id, "name": name, "route": route, "target": target,
				"estimated_duration": estDur, "status": status, "current_phase": phase,
				"progress": progress, "start_time": startTime.String, "end_time": endTime.String,
				"description": desc, "created_at": created.String, "updated_at": updated.String,
			})
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

	res, err := a.db.Exec(
		`INSERT INTO flight_missions(name, route, target, estimated_duration, description, status, current_phase, progress) VALUES(?,?,?,?,?,?,?,?)`,
		p.Name, p.Route, p.Target, p.EstimatedDuration, p.Description, "待起飞", "待命", 0,
	)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	id, _ := res.LastInsertId()

	// add mission log
	a.db.Exec(`INSERT INTO mission_logs(mission_id, phase, message) VALUES(?,?,?)`, id, "创建", "任务已创建: "+p.Name)

	c.JSON(200, gin.H{"ok": true, "id": id})
}

// FlightMissionsGet returns a single mission by ID
func (a *API) FlightMissionsGet(c *gin.Context) {
	id := c.Param("id")
	var m struct {
		ID                                               int
		Name, Route, Target, EstDur, Status, Phase, Desc string
		Progress                                         int
		StartTime, EndTime, Created, Updated             sql.NullString
	}
	err := a.db.QueryRow(
		`SELECT id, name, route, target, estimated_duration, status, current_phase, progress, start_time, end_time, description, created_at, updated_at FROM flight_missions WHERE id=?`, id,
	).Scan(&m.ID, &m.Name, &m.Route, &m.Target, &m.EstDur, &m.Status, &m.Phase, &m.Progress, &m.StartTime, &m.EndTime, &m.Desc, &m.Created, &m.Updated)
	if err != nil {
		c.JSON(404, gin.H{"error": "任务不存在"})
		return
	}
	c.JSON(200, gin.H{
		"id": m.ID, "name": m.Name, "route": m.Route, "target": m.Target,
		"estimated_duration": m.EstDur, "status": m.Status, "current_phase": m.Phase,
		"progress": m.Progress, "start_time": m.StartTime.String, "end_time": m.EndTime.String,
		"description": m.Desc, "created_at": m.Created.String, "updated_at": m.Updated.String,
	})
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
	_, err := a.db.Exec(
		`UPDATE flight_missions SET name=?, route=?, target=?, estimated_duration=?, description=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		p.Name, p.Route, p.Target, p.EstimatedDuration, p.Description, id,
	)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
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
	c.JSON(200, gin.H{"ok": true})
}

// FlightMissionsUpdatePhase updates the status/phase of a mission (state machine)
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

	c.JSON(200, gin.H{"ok": true, "status": status, "phase": p.Phase, "progress": progress})
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
	c.JSON(200, gin.H{"ok": true, "imported": count})
}
