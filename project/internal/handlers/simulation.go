package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"smartcontrol/internal/db"
	"smartcontrol/internal/simulation"

	"github.com/gin-gonic/gin"
)

// SimAPI handles all simulation management endpoints.
type SimAPI struct {
	db      *db.DB
	engine  *simulation.Engine
	trainer *simulation.RLTrainer
}

// SimEngineRef is the global simulation engine reference.
var SimEngineRef *simulation.Engine

// SimTrainerRef is the global RL trainer reference.
var SimTrainerRef *simulation.RLTrainer

// NewSimAPI creates a new simulation API handler.
func NewSimAPI(database *db.DB, engine *simulation.Engine, trainer *simulation.RLTrainer) *SimAPI {
	return &SimAPI{db: database, engine: engine, trainer: trainer}
}

// RegisterSimulationRoutes registers all simulation-related API endpoints.
func RegisterSimulationRoutes(r *gin.Engine, database *db.DB, engine *simulation.Engine, trainer *simulation.RLTrainer) {
	api := NewSimAPI(database, engine, trainer)
	sim := r.Group("/api/sim")
	{
		// Engine metrics
		sim.GET("/metrics", api.EngineMetrics)

		// Batch management
		sim.POST("/batches", api.BatchCreate)
		sim.GET("/batches", api.BatchList)
		sim.GET("/batches/:id", api.BatchGet)
		sim.POST("/batches/:id/start", api.BatchStart)
		sim.POST("/batches/:id/stop", api.BatchStop)
		sim.DELETE("/batches/:id", api.BatchDelete)

		// Instance management
		sim.GET("/instances", api.InstanceList)
		sim.POST("/instances", api.InstanceCreate)
		sim.GET("/instances/:id", api.InstanceGet)
		sim.GET("/instances/:id/telemetry", api.InstanceTelemetry)
		sim.POST("/instances/:id/start", api.InstanceStart)
		sim.POST("/instances/:id/stop", api.InstanceStop)
		sim.DELETE("/instances/:id", api.InstanceDelete)
		sim.PUT("/instances/:id/config", api.InstanceUpdateConfig)

		// Anomaly injection
		sim.POST("/instances/:id/anomaly", api.InjectInstanceAnomaly)
		sim.POST("/batches/:id/anomaly", api.InjectBatchAnomaly)
		sim.DELETE("/instances/:id/anomalies", api.ClearInstanceAnomalies)

		// Events and telemetry history
		sim.GET("/events", api.EventList)
		sim.GET("/telemetry-log", api.TelemetryLogList)

		// Data export
		sim.GET("/export", api.ExportData)

		// RL training
		sim.POST("/rl/start", api.RLStart)
		sim.POST("/rl/stop", api.RLStop)
		sim.GET("/rl/status", api.RLStatus)
		sim.GET("/rl/eval", api.RLEvalResults)
		sim.GET("/rl/history", api.RLTrainingHistory)
		sim.GET("/rl/export-policy", api.RLExportPolicy)

		// Live positions for simulation map
		sim.GET("/positions", api.LivePositions)
		sim.GET("/stats", api.SimStats)

		// Real-time WebSocket stream
		r.GET("/api/sim/stream", api.SimStream)
	}
}

// ==================== Engine Metrics ====================

func (s *SimAPI) EngineMetrics(c *gin.Context) {
	m := s.engine.Metrics()
	c.JSON(200, m)
}

// ==================== Batch Management ====================

func (s *SimAPI) BatchCreate(c *gin.Context) {
	var p struct {
		Name        string                 `json:"name"`
		Count       int                    `json:"count"`
		Model       string                 `json:"model"`
		Mission     simulation.MissionType `json:"mission"`
		CenterLat   float64                `json:"center_lat"`
		CenterLng   float64                `json:"center_lng"`
		SpreadM     float64                `json:"spread_m"`
		CruiseSpeed float64                `json:"cruise_speed"`
		MaxAlt      float64                `json:"max_alt"`
		LoopRoute   bool                   `json:"loop_route"`
		Waypoints   []simulation.Waypoint  `json:"waypoints"`
		AutoStart   bool                   `json:"auto_start"`
	}
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"error": "请求格式错误: " + err.Error()})
		return
	}
	if p.Count <= 0 {
		c.JSON(400, gin.H{"error": "无人机数量必须大于0"})
		return
	}
	if p.Count > 500 {
		c.JSON(400, gin.H{"error": "单批次最多500台"})
		return
	}
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		p.Name = fmt.Sprintf("仿真批次-%s", time.Now().Format("20060102-150405"))
	}
	// Check for duplicate batch name
	existingBatches := s.engine.ListBatches()
	for _, eb := range existingBatches {
		if eb.Name == p.Name {
			c.JSON(400, gin.H{"error": "批次名称 \"" + p.Name + "\" 已存在，请使用不同的名称"})
			return
		}
	}

	if p.SpreadM <= 0 {
		p.SpreadM = 500
	}
	// Default center: Zhengzhou University main campus
	if p.CenterLat == 0 && p.CenterLng == 0 {
		p.CenterLat = 34.7930
		p.CenterLng = 113.6636
	}

	batchCfg := simulation.BatchConfig{
		Name:        p.Name,
		Count:       p.Count,
		Model:       p.Model,
		Mission:     p.Mission,
		CenterLat:   p.CenterLat,
		CenterLng:   p.CenterLng,
		SpreadM:     p.SpreadM,
		CruiseSpeed: p.CruiseSpeed,
		MaxAlt:      p.MaxAlt,
		LoopRoute:   p.LoopRoute,
		Waypoints:   p.Waypoints,
	}

	ids, err := s.engine.CreateBatch(batchCfg)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	// Persist batch to DB
	wpJSON, _ := json.Marshal(p.Waypoints)
	loopInt := 0
	if p.LoopRoute {
		loopInt = 1
	}
	batchID := ""
	if len(ids) > 0 {
		// Extract batch ID from first instance ID: SIM-batchID-001
		parts := strings.Split(ids[0], "-")
		if len(parts) >= 3 {
			batchID = strings.Join(parts[1:len(parts)-1], "-")
		}
	}
	s.db.Exec(`INSERT INTO sim_batches(id, name, count, model, center_lat, center_lng, spread_m, cruise_speed, max_alt, loop_route, waypoints_json, status) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		batchID, p.Name, p.Count, p.Model, p.CenterLat, p.CenterLng, p.SpreadM, p.CruiseSpeed, p.MaxAlt, loopInt, string(wpJSON), "created")

	// Persist each instance to DB
	for _, instID := range ids {
		inst, ok := s.engine.GetInstance(instID)
		if ok {
			snap := inst.Snapshot()
			cfgJSON, _ := json.Marshal(snap.Config)
			s.db.Exec(`INSERT OR IGNORE INTO sim_instances(id, batch_id, name, model, state, flight_phase, task_status, lat, lng, alt, battery_level, battery_voltage, battery_temp, battery_health, config_json) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				snap.Config.ID, snap.Config.BatchID, snap.Config.Name, snap.Config.Model,
				string(snap.State), string(snap.Phase), string(snap.TaskStatus),
				snap.GPS.Latitude, snap.GPS.Longitude, snap.GPS.Altitude,
				snap.Battery.Level, snap.Battery.Voltage, snap.Battery.Temperature, snap.Battery.Health,
				string(cfgJSON))
		}
	}

	// Auto-start if requested
	if p.AutoStart && batchID != "" {
		started, _ := s.engine.StartBatch(batchID)
		s.db.Exec(`UPDATE sim_batches SET status='running' WHERE id=?`, batchID)
		log.Printf("[SimAPI] batch %s auto-started %d instances", batchID, started)
	}

	hub.Broadcast("sim", WSEvent{Type: "batch_created", Data: gin.H{"batch_id": batchID, "count": len(ids)}})
	c.JSON(200, gin.H{"ok": true, "batch_id": batchID, "instance_ids": ids, "count": len(ids)})
}

func (s *SimAPI) BatchList(c *gin.Context) {
	batches := s.engine.ListBatches()
	items := make([]gin.H, 0, len(batches))
	for _, b := range batches {
		// Count instances by state
		instances := s.engine.ListInstances(b.ID)
		running, stopped, failed := 0, 0, 0
		for _, inst := range instances {
			switch inst.State {
			case simulation.StateRunning:
				running++
			case simulation.StateStopped:
				stopped++
			case simulation.StateFailed:
				failed++
			}
		}
		items = append(items, gin.H{
			"id": b.ID, "name": b.Name, "count": b.Count, "model": b.Model,
			"center_lat": b.CenterLat, "center_lng": b.CenterLng,
			"spread_m": b.SpreadM, "cruise_speed": b.CruiseSpeed,
			"max_alt": b.MaxAlt, "loop_route": b.LoopRoute,
			"created_at": b.CreatedAt.Format("2006-01-02 15:04:05"),
			"running":    running, "stopped": stopped, "failed": failed,
			"total_instances": len(instances),
		})
	}
	c.JSON(200, gin.H{"items": items, "total": len(items)})
}

func (s *SimAPI) BatchGet(c *gin.Context) {
	id := c.Param("id")
	b, ok := s.engine.GetBatch(id)
	if !ok {
		c.JSON(404, gin.H{"error": "批次不存在"})
		return
	}
	instances := s.engine.ListInstances(id)
	c.JSON(200, gin.H{
		"batch":     b,
		"instances": instances,
		"total":     len(instances),
	})
}

func (s *SimAPI) BatchStart(c *gin.Context) {
	id := c.Param("id")
	started, err := s.engine.StartBatch(id)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	s.db.Exec(`UPDATE sim_batches SET status='running' WHERE id=?`, id)
	hub.Broadcast("sim", WSEvent{Type: "batch_started", Data: gin.H{"batch_id": id, "started": started}})
	c.JSON(200, gin.H{"ok": true, "started": started})
}

func (s *SimAPI) BatchStop(c *gin.Context) {
	id := c.Param("id")
	stopped, err := s.engine.StopBatch(id)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	s.db.Exec(`UPDATE sim_batches SET status='stopped' WHERE id=?`, id)
	hub.Broadcast("sim", WSEvent{Type: "batch_stopped", Data: gin.H{"batch_id": id, "stopped": stopped}})
	c.JSON(200, gin.H{"ok": true, "stopped": stopped})
}

func (s *SimAPI) BatchDelete(c *gin.Context) {
	id := c.Param("id")
	deleted, err := s.engine.DeleteBatch(id)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	s.db.Exec(`DELETE FROM sim_batches WHERE id=?`, id)
	s.db.Exec(`DELETE FROM sim_instances WHERE batch_id=?`, id)
	hub.Broadcast("sim", WSEvent{Type: "batch_deleted", Data: gin.H{"batch_id": id, "deleted": deleted}})
	c.JSON(200, gin.H{"ok": true, "deleted": deleted})
}

// ==================== Instance Management ====================

func (s *SimAPI) InstanceList(c *gin.Context) {
	batchID := strings.TrimSpace(c.Query("batch_id"))
	stateFilter := strings.TrimSpace(c.Query("state"))
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 50
	}

	all := s.engine.ListInstances(batchID)

	// Filter by state
	var filtered []simulation.InstanceSnapshot
	for _, inst := range all {
		if stateFilter != "" && string(inst.State) != stateFilter {
			continue
		}
		filtered = append(filtered, inst)
	}

	total := len(filtered)
	start := (page - 1) * pageSize
	end := start + pageSize
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}

	items := make([]gin.H, 0, end-start)
	for _, inst := range filtered[start:end] {
		items = append(items, snapshotToJSON(inst))
	}

	c.JSON(200, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize})
}

func (s *SimAPI) InstanceCreate(c *gin.Context) {
	var p struct {
		Name        string                `json:"name"`
		Model       string                `json:"model"`
		BatchID     string                `json:"batch_id"`
		InitialLat  float64               `json:"initial_lat"`
		InitialLng  float64               `json:"initial_lng"`
		InitialAlt  float64               `json:"initial_alt"`
		CruiseSpeed float64               `json:"cruise_speed"`
		MaxAlt      float64               `json:"max_alt"`
		LoopRoute   bool                  `json:"loop_route"`
		Waypoints   []simulation.Waypoint `json:"waypoints"`
	}
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"error": "请求格式错误"})
		return
	}
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		p.Name = fmt.Sprintf("模拟机-%d", time.Now().UnixMilli())
	}

	cfg := simulation.InstanceConfig{
		ID:          fmt.Sprintf("SIM-single-%d", time.Now().UnixMilli()),
		Name:        p.Name,
		Model:       p.Model,
		BatchID:     p.BatchID,
		InitialLat:  p.InitialLat,
		InitialLng:  p.InitialLng,
		InitialAlt:  p.InitialAlt,
		Waypoints:   p.Waypoints,
		CruiseSpeed: p.CruiseSpeed,
		MaxAlt:      p.MaxAlt,
		BatteryFull: 100,
		LoopRoute:   p.LoopRoute,
	}

	if err := s.engine.CreateInstance(cfg); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	// Persist to DB
	cfgJSON, _ := json.Marshal(cfg)
	s.db.Exec(`INSERT OR IGNORE INTO sim_instances(id, batch_id, name, model, state, lat, lng, alt, config_json) VALUES(?,?,?,?,?,?,?,?,?)`,
		cfg.ID, cfg.BatchID, cfg.Name, cfg.Model, "created", cfg.InitialLat, cfg.InitialLng, cfg.InitialAlt, string(cfgJSON))

	c.JSON(200, gin.H{"ok": true, "id": cfg.ID})
}

func (s *SimAPI) InstanceGet(c *gin.Context) {
	id := c.Param("id")
	inst, ok := s.engine.GetInstance(id)
	if !ok {
		c.JSON(404, gin.H{"error": "实例不存在"})
		return
	}
	snap := inst.Snapshot()
	c.JSON(200, snapshotToJSON(snap))
}

func (s *SimAPI) InstanceTelemetry(c *gin.Context) {
	id := c.Param("id")
	inst, ok := s.engine.GetInstance(id)
	if !ok {
		c.JSON(404, gin.H{"error": "实例不存在"})
		return
	}
	t := inst.Telemetry()
	c.JSON(200, gin.H{
		"device_id":    t.DeviceID,
		"timestamp":    t.Timestamp.Format("2006-01-02 15:04:05"),
		"gps":          t.GPS,
		"battery":      t.Battery,
		"flight_phase": t.FlightPhase,
		"task_status":  t.TaskStatus,
		"anomalies":    t.Anomalies,
	})
}

func (s *SimAPI) InstanceStart(c *gin.Context) {
	id := c.Param("id")
	if err := s.engine.StartInstance(id); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	hub.Broadcast("sim", WSEvent{Type: "instance_started", Data: gin.H{"id": id}})
	c.JSON(200, gin.H{"ok": true})
}

func (s *SimAPI) InstanceStop(c *gin.Context) {
	id := c.Param("id")
	if err := s.engine.StopInstance(id); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	hub.Broadcast("sim", WSEvent{Type: "instance_stopped", Data: gin.H{"id": id}})
	c.JSON(200, gin.H{"ok": true})
}

func (s *SimAPI) InstanceDelete(c *gin.Context) {
	id := c.Param("id")
	if err := s.engine.DeleteInstance(id); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	s.db.Exec(`DELETE FROM sim_instances WHERE id=?`, id)
	s.db.Exec(`DELETE FROM sim_events WHERE instance_id=?`, id)
	hub.Broadcast("sim", WSEvent{Type: "instance_deleted", Data: gin.H{"id": id}})
	c.JSON(200, gin.H{"ok": true})
}

func (s *SimAPI) InstanceUpdateConfig(c *gin.Context) {
	id := c.Param("id")
	var p struct {
		CruiseSpeed float64 `json:"cruise_speed"`
		MaxAlt      float64 `json:"max_alt"`
		LoopRoute   bool    `json:"loop_route"`
	}
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"error": "请求格式错误"})
		return
	}
	if err := s.engine.UpdateInstanceConfig(id, p.CruiseSpeed, p.MaxAlt, p.LoopRoute); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// ==================== Anomaly Injection ====================

func (s *SimAPI) InjectInstanceAnomaly(c *gin.Context) {
	id := c.Param("id")
	var p struct {
		Type     string `json:"type"`     // low_battery, flight_deviation, comm_lost, temp_high, temp_low
		Level    string `json:"level"`    // 提示, 警告, 紧急故障
		Duration int    `json:"duration"` // seconds, 0=permanent until cleared
	}
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"error": "请求格式错误"})
		return
	}

	cfg := simulation.AnomalyConfig{
		Type:     simulation.AnomalyType(p.Type),
		Level:    simulation.AlertLevel(p.Level),
		Duration: time.Duration(p.Duration) * time.Second,
	}

	if err := s.engine.InjectAnomaly(id, cfg); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	// Record event to DB
	s.db.Exec(`INSERT INTO sim_events(instance_id, event_type, level, message) VALUES(?,?,?,?)`,
		id, string(cfg.Type), string(cfg.Level), fmt.Sprintf("注入异常: %s (等级: %s)", cfg.Type, cfg.Level))

	hub.Broadcast("sim", WSEvent{Type: "anomaly_injected", Data: gin.H{"id": id, "anomaly_type": p.Type, "level": p.Level}})
	c.JSON(200, gin.H{"ok": true})
}

func (s *SimAPI) InjectBatchAnomaly(c *gin.Context) {
	batchID := c.Param("id")
	var p struct {
		Type     string  `json:"type"`
		Level    string  `json:"level"`
		Duration int     `json:"duration"`
		Percent  float64 `json:"percent"` // percentage of instances to affect
	}
	if err := c.BindJSON(&p); err != nil {
		c.JSON(400, gin.H{"error": "请求格式错误"})
		return
	}
	if p.Percent <= 0 {
		p.Percent = 100
	}

	cfg := simulation.AnomalyConfig{
		Type:     simulation.AnomalyType(p.Type),
		Level:    simulation.AlertLevel(p.Level),
		Duration: time.Duration(p.Duration) * time.Second,
	}

	count, err := s.engine.InjectBatchAnomaly(batchID, cfg, p.Percent)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	s.db.Exec(`INSERT INTO sim_events(instance_id, event_type, level, message) VALUES(?,?,?,?)`,
		batchID, string(cfg.Type), string(cfg.Level), fmt.Sprintf("批量注入异常: %s, 影响 %d 台 (%.0f%%)", cfg.Type, count, p.Percent))

	hub.Broadcast("sim", WSEvent{Type: "batch_anomaly_injected", Data: gin.H{"batch_id": batchID, "count": count}})
	c.JSON(200, gin.H{"ok": true, "injected": count})
}

func (s *SimAPI) ClearInstanceAnomalies(c *gin.Context) {
	id := c.Param("id")
	if err := s.engine.ClearInstanceAnomalies(id); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// ==================== Events & Telemetry Log ====================

func (s *SimAPI) EventList(c *gin.Context) {
	instanceID := strings.TrimSpace(c.Query("instance_id"))
	eventType := strings.TrimSpace(c.Query("event_type"))
	filter := strings.TrimSpace(c.Query("filter"))
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
	if instanceID != "" {
		where = append(where, "instance_id = ?")
		args = append(args, instanceID)
	}
	if eventType != "" {
		where = append(where, "event_type = ?")
		args = append(args, eventType)
	}
	if filter == "anomaly" {
		where = append(where, "event_type != 'state_change'")
	}
	wc := strings.Join(where, " AND ")

	var total int
	s.db.QueryRow("SELECT COUNT(*) FROM sim_events WHERE "+wc, args...).Scan(&total)

	offset := (page - 1) * pageSize
	rows, err := s.db.Query(`SELECT id, instance_id, event_type, level, message, detail_json, created_at FROM sim_events WHERE `+wc+` ORDER BY datetime(created_at) DESC LIMIT ? OFFSET ?`, append(args, pageSize, offset)...)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	items := []gin.H{}
	for rows.Next() {
		var id int
		var instID, evtType, level, msg, detail, created string
		if rows.Scan(&id, &instID, &evtType, &level, &msg, &detail, &created) == nil {
			items = append(items, gin.H{
				"id": id, "instance_id": instID, "event_type": evtType,
				"level": level, "message": msg, "detail_json": detail, "created_at": created,
			})
		}
	}
	c.JSON(200, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize})
}

func (s *SimAPI) TelemetryLogList(c *gin.Context) {
	instanceID := strings.TrimSpace(c.Query("instance_id"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "200"))
	if limit < 1 || limit > 2000 {
		limit = 200
	}
	if instanceID == "" {
		c.JSON(400, gin.H{"error": "instance_id required"})
		return
	}

	rows, err := s.db.Query(`SELECT id, instance_id, lat, lng, alt, speed, heading, battery_level, flight_phase, created_at FROM sim_telemetry_log WHERE instance_id=? ORDER BY datetime(created_at) DESC LIMIT ?`, instanceID, limit)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	items := []gin.H{}
	for rows.Next() {
		var id, batLvl int
		var instID, phase, created string
		var lat, lng, alt, speed, heading float64
		if rows.Scan(&id, &instID, &lat, &lng, &alt, &speed, &heading, &batLvl, &phase, &created) == nil {
			items = append(items, gin.H{
				"id": id, "instance_id": instID,
				"lat": lat, "lng": lng, "alt": alt, "speed": speed, "heading": heading,
				"battery_level": batLvl, "flight_phase": phase, "created_at": created,
			})
		}
	}
	c.JSON(200, gin.H{"items": items})
}

// ==================== Data Export ====================

func (s *SimAPI) ExportData(c *gin.Context) {
	batchID := strings.TrimSpace(c.Query("batch_id"))
	instances := s.engine.ListInstances(batchID)
	telemetry := s.engine.GetAllTelemetry()
	metrics := s.engine.Metrics()

	c.JSON(200, gin.H{
		"metrics":     metrics,
		"instances":   instances,
		"telemetry":   telemetry,
		"exported_at": time.Now().Format("2006-01-02 15:04:05"),
	})
}

// ==================== RL Training ====================

func (s *SimAPI) RLStart(c *gin.Context) {
	var p struct {
		EvalInterval int `json:"eval_interval"`
	}
	c.BindJSON(&p)
	if p.EvalInterval <= 0 {
		p.EvalInterval = 100
	}
	s.trainer.StartTraining(p.EvalInterval)
	c.JSON(200, gin.H{"ok": true, "message": "RL训练已启动"})
}

func (s *SimAPI) RLStop(c *gin.Context) {
	s.trainer.StopTraining()
	c.JSON(200, gin.H{"ok": true, "message": "RL训练已停止"})
}

func (s *SimAPI) RLStatus(c *gin.Context) {
	metrics := s.trainer.TrainingMetrics()
	c.JSON(200, metrics)
}

func (s *SimAPI) RLEvalResults(c *gin.Context) {
	results := s.trainer.GetEvalResults()
	c.JSON(200, gin.H{"items": results, "total": len(results)})
}

func (s *SimAPI) RLTrainingHistory(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if limit < 1 || limit > 1000 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT id, episode, avg_reward, route_efficiency, safety_score, energy_score, task_completion, anomaly_score, epsilon, created_at FROM rl_training_log ORDER BY datetime(created_at) DESC LIMIT ?`, limit)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	items := []gin.H{}
	for rows.Next() {
		var id, episode int
		var avgReward, route, safety, energy, task, anomaly, eps float64
		var created string
		if rows.Scan(&id, &episode, &avgReward, &route, &safety, &energy, &task, &anomaly, &eps, &created) == nil {
			items = append(items, gin.H{
				"id": id, "episode": episode, "avg_reward": avgReward,
				"route_efficiency": route, "safety_score": safety, "energy_score": energy,
				"task_completion": task, "anomaly_score": anomaly, "epsilon": eps,
				"created_at": created,
			})
		}
	}
	c.JSON(200, gin.H{"items": items, "total": len(items)})
}

// ==================== WebSocket Stream ====================

func (s *SimAPI) SimStream(c *gin.Context) {
	filter := c.Query("filter")
	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	topic := "sim"
	if filter == "anomaly" {
		topic = "sim_anomaly"
	}
	hub.Subscribe(topic, ws)
	defer func() {
		hub.Unsubscribe(topic, ws)
		ws.Close()
	}()
	for {
		if _, _, err := ws.ReadMessage(); err != nil {
			break
		}
	}
}

// ==================== Helpers ====================

func snapshotToJSON(snap simulation.InstanceSnapshot) gin.H {
	anomalies := make([]gin.H, 0, len(snap.Anomalies))
	for _, a := range snap.Anomalies {
		anomalies = append(anomalies, gin.H{
			"type": a.Type, "level": a.Level, "message": a.Message,
			"start_time": a.StartTime.Format("2006-01-02 15:04:05"),
			"active":     a.Active,
		})
	}
	// Build waypoints summary for map display
	wps := make([]gin.H, 0, len(snap.Config.Waypoints))
	for _, wp := range snap.Config.Waypoints {
		wps = append(wps, gin.H{"lat": wp.Lat, "lng": wp.Lng, "alt": wp.Alt, "action": wp.Action, "name": wp.Name})
	}

	return gin.H{
		"id":           snap.Config.ID,
		"name":         snap.Config.Name,
		"model":        snap.Config.Model,
		"batch_id":     snap.Config.BatchID,
		"mission":      snap.Config.Mission,
		"mission_desc": snap.Config.MissionDesc,
		"state":        snap.State,
		"flight_phase": snap.Phase,
		"task_status":  snap.TaskStatus,
		"gps": gin.H{
			"latitude": snap.GPS.Latitude, "longitude": snap.GPS.Longitude,
			"altitude": snap.GPS.Altitude, "speed": snap.GPS.Speed,
			"heading": snap.GPS.Heading, "accuracy": snap.GPS.Accuracy,
		},
		"battery": gin.H{
			"level": snap.Battery.Level, "voltage": snap.Battery.Voltage,
			"current": snap.Battery.Current, "temperature": snap.Battery.Temperature,
			"health": snap.Battery.Health, "status": snap.Battery.Status,
			"charge_cycles": snap.Battery.ChargeCycles,
		},
		"waypoints":        wps,
		"waypoint_idx":     snap.WaypointIdx,
		"loop_count":       snap.LoopCount,
		"total_flight_sec": snap.TotalFlightSec,
		"anomalies":        anomalies,
		"last_update":      snap.LastUpdate.Format("2006-01-02 15:04:05"),
		"created_at":       snap.CreatedAt.Format("2006-01-02 15:04:05"),
		"error_msg":        snap.ErrorMsg,
	}
}

// ==================== Live Positions (for simulation map) ====================

func (s *SimAPI) LivePositions(c *gin.Context) {
	telemetry := s.engine.GetAllTelemetry()
	items := make([]gin.H, 0, len(telemetry))
	for _, t := range telemetry {
		hasAnomaly := false
		for _, a := range t.Anomalies {
			if a.Active {
				hasAnomaly = true
				break
			}
		}
		// Get waypoints from instance config
		var waypoints []gin.H
		inst, ok := s.engine.GetInstance(t.DeviceID)
		if ok {
			cfg := inst.Config()
			for _, wp := range cfg.Waypoints {
				waypoints = append(waypoints, gin.H{"lat": wp.Lat, "lng": wp.Lng, "alt": wp.Alt, "name": wp.Name, "action": wp.Action})
			}
		}
		items = append(items, gin.H{
			"id":             t.DeviceID,
			"lat":            t.GPS.Latitude,
			"lng":            t.GPS.Longitude,
			"alt":            t.GPS.Altitude,
			"speed":          t.GPS.Speed,
			"heading":        t.GPS.Heading,
			"battery":        t.Battery.Level,
			"phase":          t.FlightPhase,
			"task":           t.TaskStatus,
			"anomaly":        hasAnomaly,
			"waypoints":      waypoints,
			"route_progress": math.Round(t.RouteProgress*1000) / 1000,
		})
	}
	c.JSON(200, gin.H{"items": items, "count": len(items)})
}

// ==================== RL Policy Export ====================

func (s *SimAPI) RLExportPolicy(c *gin.Context) {
	metrics := s.trainer.TrainingMetrics()
	evalResults := s.trainer.GetEvalResults()

	c.JSON(200, gin.H{
		"description":       "强化学习策略导出 — 可用于实机部署的决策参数",
		"training_episodes": metrics["episodes"],
		"avg_reward":        metrics["avg_reward"],
		"best_reward":       metrics["best_reward"],
		"epsilon":           metrics["epsilon"],
		"policy_type":       "tabular_q_learning",
		"action_space": []gin.H{
			{"id": 0, "name": "adjust_heading", "desc": "调整航向角（±45°）"},
			{"id": 1, "name": "adjust_speed", "desc": "调整巡航速度（0.3x~1.5x）"},
			{"id": 2, "name": "adjust_altitude", "desc": "调整飞行高度（±30m）"},
			{"id": 3, "name": "goto_waypoint", "desc": "跳转至下一航点"},
			{"id": 4, "name": "return_home", "desc": "触发返航"},
			{"id": 5, "name": "hover", "desc": "悬停等待"},
			{"id": 6, "name": "emergency_land", "desc": "紧急降落"},
		},
		"learned_rules": []gin.H{
			{"condition": "电量<20% + 任何异常", "recommended_action": "return_home / emergency_land", "confidence": "高"},
			{"condition": "碰撞距离<15m", "recommended_action": "adjust_heading / adjust_altitude", "confidence": "高"},
			{"condition": "偏航异常", "recommended_action": "adjust_heading / goto_waypoint", "confidence": "中"},
			{"condition": "温度异常", "recommended_action": "return_home / adjust_altitude", "confidence": "中"},
			{"condition": "通信失联", "recommended_action": "return_home / hover", "confidence": "高"},
			{"condition": "正常巡航", "recommended_action": "goto_waypoint / adjust_speed", "confidence": "中"},
		},
		"deployment_guide": "1. 导出Q表策略文件(data/rl_policy.json); 2. 在实机控制器中加载策略; 3. 根据当前无人机状态离散化后查表获取最优动作; 4. 通过MAVLink/SDK执行动作指令",
		"eval_history":     evalResults,
	})
}

// ==================== Simulation Statistics (Stage-8 Dashboard) ====================

func (s *SimAPI) SimStats(c *gin.Context) {
	// Optional batch filter
	batchID := c.Query("batch_id")

	// Live snapshots
	instances := s.engine.ListInstances(batchID)
	var telemetry []simulation.TelemetrySnapshot
	if batchID != "" {
		telemetry = s.engine.GetTelemetryForBatch(batchID)
	} else {
		telemetry = s.engine.GetAllTelemetry()
	}

	totalInstances := len(instances)
	runningInstances := 0
	anomalyCount := 0
	batteryRiskCount := 0
	completedTasks := 0

	missionDist := map[string]int{}
	taskStatusDist := map[string]int{}
	alertTypeDist := map[string]int{}

	for _, inst := range instances {
		if inst.State == simulation.StateRunning {
			runningInstances++
		}
		mission := string(inst.Config.Mission)
		if mission == "" {
			mission = "unknown"
		}
		missionDist[mission]++
		taskStatusDist[string(inst.TaskStatus)]++
		if inst.TaskStatus == simulation.TaskCompleted {
			completedTasks++
		}
	}

	// Use real-time telemetry for battery risk, anomaly count, and route progress
	var totalRouteProgress float64
	batteryHealthy := 0 // >60%
	batteryWarning := 0 // 20%-60%
	batteryDanger := 0  // <20%
	totalBattery := 0
	batterySum := 0

	type batteryItem struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Level int    `json:"level"`
	}
	batteryRanking := make([]batteryItem, 0, len(telemetry))

	for _, t := range telemetry {
		if t.Battery.Level <= 20 {
			batteryRiskCount++
			batteryDanger++
		} else if t.Battery.Level <= 60 {
			batteryWarning++
		} else {
			batteryHealthy++
		}
		totalBattery++
		batterySum += t.Battery.Level
		totalRouteProgress += t.RouteProgress
		for _, a := range t.Anomalies {
			if a.Active {
				anomalyCount++
				break
			}
		}
		// Find instance name for battery ranking
		instName := t.DeviceID
		for _, inst := range instances {
			if inst.Config.ID == t.DeviceID {
				instName = inst.Config.Name
				break
			}
		}
		batteryRanking = append(batteryRanking, batteryItem{
			ID:    t.DeviceID,
			Name:  instName,
			Level: t.Battery.Level,
		})
	}
	avgBattery := 0
	if totalBattery > 0 {
		avgBattery = batterySum / totalBattery
	}

	// Task completion rate: use average route progress from running drones (continuous 0-1)
	// This gives a meaningful percentage even while drones are mid-mission
	taskCompletionRate := 0.0
	if len(telemetry) > 0 {
		taskCompletionRate = totalRouteProgress / float64(len(telemetry))
	} else if totalInstances > 0 {
		taskCompletionRate = float64(completedTasks) / float64(totalInstances)
	}
	avgRouteProgress := taskCompletionRate

	// Build set of instance IDs for batch filtering in DB queries
	batchInstanceIDs := map[string]struct{}{}
	if batchID != "" {
		for _, inst := range instances {
			batchInstanceIDs[inst.Config.ID] = struct{}{}
		}
	}

	// Time-series: running trend / battery risk trend from recent telemetry logs
	type riskCounter struct {
		total int
		risk  int
	}
	runningSetByMinute := map[string]map[string]struct{}{}
	batteryRiskByMinute := map[string]riskCounter{}

	rows1, err := s.db.Query(`SELECT instance_id, battery_level, flight_phase, created_at FROM sim_telemetry_log ORDER BY datetime(created_at) DESC LIMIT 5000`)
	if err == nil {
		defer rows1.Close()
		for rows1.Next() {
			var instID, phase, created string
			var battery int
			if rows1.Scan(&instID, &battery, &phase, &created) != nil {
				continue
			}
			// Filter by batch if specified
			if batchID != "" {
				if _, ok := batchInstanceIDs[instID]; !ok {
					continue
				}
			}
			if len(created) < 16 {
				continue
			}
			minuteKey := created[:16]

			if _, ok := runningSetByMinute[minuteKey]; !ok {
				runningSetByMinute[minuteKey] = map[string]struct{}{}
			}
			if phase != string(simulation.PhaseIdle) {
				runningSetByMinute[minuteKey][instID] = struct{}{}
			}

			rc := batteryRiskByMinute[minuteKey]
			rc.total++
			if battery <= 20 {
				rc.risk++
			}
			batteryRiskByMinute[minuteKey] = rc
		}
	}

	// Time-series: anomaly trend + alert type distribution from recent events
	anomalyByMinute := map[string]int{}
	rows2, err := s.db.Query(`SELECT event_type, created_at, instance_id FROM sim_events ORDER BY datetime(created_at) DESC LIMIT 3000`)
	if err == nil {
		defer rows2.Close()
		for rows2.Next() {
			var eventType, created, evtInstID string
			if rows2.Scan(&eventType, &created, &evtInstID) != nil {
				continue
			}
			// Filter by batch if specified
			if batchID != "" {
				if _, ok := batchInstanceIDs[evtInstID]; !ok {
					continue
				}
			}
			alertTypeDist[eventType]++
			if eventType == "state_change" {
				continue
			}
			if len(created) < 16 {
				continue
			}
			minuteKey := created[:16]
			anomalyByMinute[minuteKey]++
		}
	}

	// Backfill current minute with live active anomaly count so the big-screen
	// anomaly series responds even when anomalies are active but no new event row is written.
	nowMinuteKey := time.Now().Format("2006-01-02 15:04")
	if anomalyCount > anomalyByMinute[nowMinuteKey] {
		anomalyByMinute[nowMinuteKey] = anomalyCount
	}

	// RL curve from persisted history
	rlEpisodes := make([]int, 0, 100)
	rlRewards := make([]float64, 0, 100)
	rlEpsilons := make([]float64, 0, 100)
	rows3, err := s.db.Query(`SELECT episode, avg_reward, epsilon FROM rl_training_log ORDER BY episode ASC LIMIT 500`)
	if err == nil {
		defer rows3.Close()
		for rows3.Next() {
			var ep int
			var reward, eps float64
			if rows3.Scan(&ep, &reward, &eps) != nil {
				continue
			}
			rlEpisodes = append(rlEpisodes, ep)
			rlRewards = append(rlRewards, reward)
			rlEpsilons = append(rlEpsilons, eps)
		}
	}

	buildSortedSeries := func(source map[string]int) ([]string, []int) {
		keys := make([]string, 0, len(source))
		for k := range source {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if len(keys) > 30 {
			keys = keys[len(keys)-30:]
		}
		vals := make([]int, 0, len(keys))
		for _, k := range keys {
			vals = append(vals, source[k])
		}
		return keys, vals
	}

	buildRunningSeries := func() ([]string, []int) {
		keys := make([]string, 0, len(runningSetByMinute))
		for k := range runningSetByMinute {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if len(keys) > 30 {
			keys = keys[len(keys)-30:]
		}
		vals := make([]int, 0, len(keys))
		for _, k := range keys {
			vals = append(vals, len(runningSetByMinute[k]))
		}
		return keys, vals
	}

	buildBatteryRiskSeries := func() ([]string, []float64) {
		keys := make([]string, 0, len(batteryRiskByMinute))
		for k := range batteryRiskByMinute {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if len(keys) > 30 {
			keys = keys[len(keys)-30:]
		}
		vals := make([]float64, 0, len(keys))
		for _, k := range keys {
			rc := batteryRiskByMinute[k]
			if rc.total == 0 {
				vals = append(vals, 0)
			} else {
				vals = append(vals, float64(rc.risk)/float64(rc.total))
			}
		}
		return keys, vals
	}

	runLabels, runValues := buildRunningSeries()
	anomLabels, anomValues := buildSortedSeries(anomalyByMinute)
	batteryLabels, batteryRiskValues := buildBatteryRiskSeries()

	c.JSON(200, gin.H{
		"live": gin.H{
			"total_instances":      totalInstances,
			"running_instances":    runningInstances,
			"anomaly_instances":    anomalyCount,
			"battery_risk_count":   batteryRiskCount,
			"task_completion_rate": taskCompletionRate,
			"avg_route_progress":   avgRouteProgress,
			"completed_tasks":      completedTasks,
			"avg_battery":          avgBattery,
		},
		"distribution": gin.H{
			"mission":     missionDist,
			"task_status": taskStatusDist,
			"alert_type":  alertTypeDist,
		},
		"battery": gin.H{
			"ranking":   batteryRanking,
			"healthy":   batteryHealthy,
			"warning":   batteryWarning,
			"danger":    batteryDanger,
			"avg_level": avgBattery,
		},
		"charts": gin.H{
			"running_trend": gin.H{
				"labels": runLabels,
				"values": runValues,
			},
			"anomaly_trend": gin.H{
				"labels": anomLabels,
				"values": anomValues,
			},
			"battery_risk_trend": gin.H{
				"labels": batteryLabels,
				"values": batteryRiskValues,
			},
			"rl_curve": gin.H{
				"episodes": rlEpisodes,
				"rewards":  rlRewards,
				"epsilons": rlEpsilons,
			},
		},
	})
}
