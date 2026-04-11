package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync/atomic"

	"smartcontrol/internal/db"
	"smartcontrol/internal/simulation"
	"smartcontrol/internal/taskpool"
)

// SimTelemetryPusher bridges simulation telemetry into the existing GPS/Battery/Flight DB tables
// and WebSocket broadcast channels, ensuring simulated data is indistinguishable from real data.
type SimTelemetryPusher struct {
	db              *db.DB
	pool            *taskpool.Pool // unified task pool for async DB writes
	tickCount       int64
	dbWriteInterval int64 // write to DB every N ticks (throttle)
}

// NewSimTelemetryPusher creates a telemetry pusher.
// If pool is non-nil, expensive DB writes are dispatched through the pool.
func NewSimTelemetryPusher(database *db.DB, pool *taskpool.Pool) *SimTelemetryPusher {
	return &SimTelemetryPusher{
		db:              database,
		pool:            pool,
		dbWriteInterval: 5, // write to sim_telemetry_log every 5 seconds
	}
}

// OnTelemetry handles a telemetry snapshot from a simulation instance.
// DB writes are dispatched through the pool (if available) to avoid blocking the sim goroutine.
func (p *SimTelemetryPusher) OnTelemetry(snap simulation.TelemetrySnapshot) {
	tick := atomic.AddInt64(&p.tickCount, 1)

	// Throttle DB writes to reduce pressure on cloud MySQL
	writeGPS := tick%3 == 0      // GPS update every 3s
	writeBattery := tick%10 == 0 // battery record every 10s
	writeLog := tick%10 == 0     // telemetry log every 10s
	writeFlight := tick%3 == 0   // flight state update every 3s

	// Dispatch DB-heavy work through the task pool (non-blocking)
	if p.pool != nil {
		_ = p.pool.TrySubmit(taskpool.Task{
			Name:     fmt.Sprintf("sim:telem:%s", snap.DeviceID),
			Group:    "simulation",
			Priority: taskpool.PriorityNormal,
			Mode:     taskpool.ModeIO,
			Fn: func(ctx context.Context) error {
				if writeGPS {
					p.pushGPSUpdate(snap)
				}
				if writeBattery {
					p.pushBatteryUpdate(snap)
				}
				if writeFlight {
					p.pushFlightUpdate(snap)
				}
				if writeLog {
					p.db.Exec(`INSERT INTO sim_telemetry_log(instance_id, lat, lng, alt, speed, heading, battery_level, flight_phase) VALUES(?,?,?,?,?,?,?,?)`,
						snap.DeviceID, snap.GPS.Latitude, snap.GPS.Longitude, snap.GPS.Altitude,
						snap.GPS.Speed, snap.GPS.Heading, snap.Battery.Level, string(snap.FlightPhase))
				}
				return nil
			},
		})
	} else {
		// Fallback: synchronous (pool not yet initialized)
		if writeGPS {
			p.pushGPSUpdate(snap)
		}
		if writeBattery {
			p.pushBatteryUpdate(snap)
		}
		if writeFlight {
			p.pushFlightUpdate(snap)
		}
		if writeLog {
			p.db.Exec(`INSERT INTO sim_telemetry_log(instance_id, lat, lng, alt, speed, heading, battery_level, flight_phase) VALUES(?,?,?,?,?,?,?,?)`,
				snap.DeviceID, snap.GPS.Latitude, snap.GPS.Longitude, snap.GPS.Altitude,
				snap.GPS.Speed, snap.GPS.Heading, snap.Battery.Level, string(snap.FlightPhase))
		}
	}

	// WebSocket broadcast (always non-blocking via throttler)
	ThrottledBroadcast("sim", WSEvent{
		Type: "sim_telemetry",
		Data: map[string]interface{}{
			"device_id":      snap.DeviceID,
			"lat":            snap.GPS.Latitude,
			"lng":            snap.GPS.Longitude,
			"alt":            snap.GPS.Altitude,
			"speed":          snap.GPS.Speed,
			"heading":        snap.GPS.Heading,
			"battery":        snap.Battery.Level,
			"flight_phase":   snap.FlightPhase,
			"task_status":    snap.TaskStatus,
			"route_progress": snap.RouteProgress,
		},
	})
}

func (p *SimTelemetryPusher) pushGPSUpdate(snap simulation.TelemetrySnapshot) {
	if snap.GPS.Latitude == 0 && snap.GPS.Longitude == 0 {
		return
	}

	// Update or create gps_device for this sim instance
	var deviceID int
	err := p.db.QueryRow(`SELECT id FROM gps_devices WHERE agent_id = ?`, snap.DeviceID).Scan(&deviceID)
	if err != nil {
		// Auto-create GPS device for this simulation instance
		res, err2 := p.db.Exec(
			`INSERT INTO gps_devices(name, agent_id, device_type, latitude, longitude, altitude, speed, heading, accuracy, status, fence_enabled, last_update) VALUES(?,?,?,?,?,?,?,?,?,?,0,CURRENT_TIMESTAMP)`,
			snap.DeviceID, snap.DeviceID, "模拟无人机",
			snap.GPS.Latitude, snap.GPS.Longitude, snap.GPS.Altitude,
			snap.GPS.Speed, snap.GPS.Heading, snap.GPS.Accuracy, "在线",
		)
		if err2 != nil {
			return
		}
		newID, _ := res.LastInsertId()
		deviceID = int(newID)
	} else {
		// Update existing
		p.db.Exec(
			`UPDATE gps_devices SET latitude=?, longitude=?, altitude=?, speed=?, heading=?, accuracy=?, status='在线', last_update=CURRENT_TIMESTAMP WHERE id=?`,
			snap.GPS.Latitude, snap.GPS.Longitude, snap.GPS.Altitude,
			snap.GPS.Speed, snap.GPS.Heading, snap.GPS.Accuracy, deviceID,
		)
	}

	// Batch GPS history write
	BatchGPSHistory(p.db, deviceID, snap.GPS.Latitude, snap.GPS.Longitude, snap.GPS.Altitude, snap.GPS.Speed, snap.GPS.Heading)

	// GPS WebSocket broadcast
	ThrottledBroadcast("gps", WSEvent{
		Type: "gps_update",
		Data: map[string]interface{}{
			"device_id": deviceID,
			"latitude":  snap.GPS.Latitude,
			"longitude": snap.GPS.Longitude,
			"altitude":  snap.GPS.Altitude,
			"speed":     snap.GPS.Speed,
			"heading":   snap.GPS.Heading,
			"simulated": true,
		},
	})
}

func (p *SimTelemetryPusher) pushBatteryUpdate(snap simulation.TelemetrySnapshot) {
	// Find GPS device for this sim instance
	var deviceID int
	var deviceName string
	err := p.db.QueryRow(`SELECT id, name FROM gps_devices WHERE agent_id = ?`, snap.DeviceID).Scan(&deviceID, &deviceName)
	if err != nil {
		return
	}

	status := snap.Battery.Status
	p.db.Exec(
		`INSERT INTO battery_records(device_id, device_name, voltage, current_val, level, temperature, health, status, charge_cycles, remaining_time) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		deviceID, deviceName, snap.Battery.Voltage, snap.Battery.Current,
		snap.Battery.Level, snap.Battery.Temperature, snap.Battery.Health,
		status, snap.Battery.ChargeCycles, estimateRemaining(snap.Battery.Level),
	)

	// Auto-generate alerts for sim drones too
	if snap.Battery.Level >= 0 && snap.Battery.Level <= 20 {
		msg := fmt.Sprintf("[模拟]无人机[%s]电量低: %d%%", deviceName, snap.Battery.Level)
		alertType := "电量低"
		if snap.Battery.Level <= 10 {
			msg = fmt.Sprintf("[模拟]无人机[%s]电量严重不足: %d%%，强制返航", deviceName, snap.Battery.Level)
			alertType = "电量严重不足"
		}
		p.db.Exec(`INSERT INTO battery_alerts(device_id, device_name, level, voltage, temperature, alert_type, message) VALUES(?,?,?,?,?,?,?)`,
			deviceID, deviceName, snap.Battery.Level, snap.Battery.Voltage, snap.Battery.Temperature, alertType, msg)
		p.db.Exec(`INSERT INTO alerts(category, severity, message) VALUES(?,?,?)`, "电池报警", "critical", msg)
	}
	if snap.Battery.Temperature >= 50 {
		msg := fmt.Sprintf("[模拟]无人机[%s]电池温度过高: %.1f°C", deviceName, snap.Battery.Temperature)
		p.db.Exec(`INSERT INTO battery_alerts(device_id, device_name, level, voltage, temperature, alert_type, message) VALUES(?,?,?,?,?,?,?)`,
			deviceID, deviceName, snap.Battery.Level, snap.Battery.Voltage, snap.Battery.Temperature, "温度过高", msg)
		p.db.Exec(`INSERT INTO alerts(category, severity, message) VALUES(?,?,?)`, "电池报警", "warning", msg)
	}

	ThrottledBroadcast("battery", WSEvent{Type: "battery_update", Data: map[string]interface{}{"device_id": deviceID, "status": status, "simulated": true}})
}

func (p *SimTelemetryPusher) pushFlightUpdate(snap simulation.TelemetrySnapshot) {
	// Map sim flight phase to existing flight mission phases
	phaseMap := map[simulation.FlightPhase]string{
		simulation.PhaseIdle:    "待命",
		simulation.PhaseTakeoff: "起飞",
		simulation.PhaseCruise:  "巡航",
		simulation.PhaseWork:    "执行任务",
		simulation.PhaseReturn:  "返航",
		simulation.PhaseLanding: "降落",
		simulation.PhaseHover:   "巡航",
	}
	phase := phaseMap[snap.FlightPhase]
	if phase == "" {
		phase = "待命"
	}

	// Update sim_instances table with latest state
	p.db.Exec(`UPDATE sim_instances SET state=?, flight_phase=?, task_status=?, lat=?, lng=?, alt=?, speed=?, heading=?, battery_level=?, battery_voltage=?, battery_temp=?, battery_health=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		"running", string(snap.FlightPhase), string(snap.TaskStatus),
		snap.GPS.Latitude, snap.GPS.Longitude, snap.GPS.Altitude,
		snap.GPS.Speed, snap.GPS.Heading, snap.Battery.Level,
		snap.Battery.Voltage, snap.Battery.Temperature, snap.Battery.Health,
		snap.DeviceID)
}

// OnStateChange handles instance lifecycle state changes.
func (p *SimTelemetryPusher) OnStateChange(id string, state simulation.InstanceState) {
	log.Printf("[SimIntegration] instance %s state -> %s", id, state)

	p.db.Exec(`UPDATE sim_instances SET state=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, string(state), id)

	if state == simulation.StateFailed {
		p.db.Exec(`INSERT INTO sim_events(instance_id, event_type, level, message) VALUES(?,?,?,?)`,
			id, "state_change", "紧急故障", fmt.Sprintf("实例 %s 进入故障状态", id))
		p.db.Exec(`INSERT INTO alerts(category, severity, message) VALUES(?,?,?)`,
			"仿真告警", "critical", fmt.Sprintf("[模拟]实例 %s 异常崩溃，已自动隔离", id))
	}

	hub.Broadcast("sim", WSEvent{Type: "state_change", Data: map[string]interface{}{"id": id, "state": string(state)}})
}

// OnAnomaly handles anomaly events from simulation instances.
func (p *SimTelemetryPusher) OnAnomaly(id string, event simulation.AnomalyEvent) {
	// Record anomaly event
	detailJSON, _ := json.Marshal(event)
	p.db.Exec(`INSERT INTO sim_events(instance_id, event_type, level, message, detail_json) VALUES(?,?,?,?,?)`,
		id, string(event.Type), string(event.Level), event.Message, string(detailJSON))

	// Also insert into global alerts table
	severity := "info"
	switch event.Level {
	case simulation.AlertWarning:
		severity = "warning"
	case simulation.AlertCritical:
		severity = "critical"
	}
	p.db.Exec(`INSERT INTO alerts(category, severity, message) VALUES(?,?,?)`,
		"仿真异常", severity, "[模拟]"+event.Message)

	anomalyWSEvent := WSEvent{
		Type: "anomaly_event",
		Data: map[string]interface{}{
			"instance_id":  id,
			"anomaly_type": string(event.Type),
			"level":        string(event.Level),
			"message":      event.Message,
		},
	}
	hub.Broadcast("sim", anomalyWSEvent)
	hub.Broadcast("sim_anomaly", anomalyWSEvent)
}

// InitSimEngine creates and initializes the simulation engine with DB integration.
// The pool parameter enables async DB writes; pass nil during early bootstrap.
func InitSimEngine(database *db.DB, pool *taskpool.Pool) (*simulation.Engine, *simulation.RLTrainer) {
	pusher := NewSimTelemetryPusher(database, pool)

	engine := simulation.NewEngine(simulation.EngineConfig{
		MaxInstances:  200,
		MaxGoroutines: 500,
		DataDir:       "data",
		OnTelemetry:   pusher.OnTelemetry,
		OnStateChange: pusher.OnStateChange,
		OnAnomaly:     pusher.OnAnomaly,
	})

	// Restore from previous session snapshots (7x24 recovery)
	restored, err := engine.RestoreFromSnapshots()
	if err != nil {
		log.Printf("[SimIntegration] restore warning: %v", err)
	} else if restored > 0 {
		log.Printf("[SimIntegration] restored %d simulation instances from previous session", restored)
	}

	// Create RL trainer
	trainer := simulation.NewRLTrainer(engine, simulation.RLTrainerConfig{
		LearningRate:   0.01,
		DiscountFactor: 0.99,
		Epsilon:        0.3,
		BufferSize:     100000,
		BatchSize:      64,
		EvalInterval:   100,
		DataDir:        "data",
	})

	// Hook eval callback for DB persistence
	trainer.OnEval = func(result simulation.EvalResult, epsilon float64) {
		database.Exec(`INSERT INTO rl_training_log(episode, avg_reward, route_efficiency, safety_score, energy_score, task_completion, anomaly_score, epsilon) VALUES(?,?,?,?,?,?,?,?)`,
			result.Episode, result.AvgReward, result.RouteEfficiency, result.SafetyScore,
			result.EnergyScore, result.TaskCompletion, result.AnomalyScore, epsilon)
	}

	// Load saved RL policy
	if err := trainer.LoadPolicy(); err != nil {
		log.Printf("[SimIntegration] load RL policy warning: %v", err)
	}

	// Load no-fly zones from DB for geofence-aware simulation
	loadNoFlyZonesToEngine(database, engine)

	SimEngineRef = engine
	SimTrainerRef = trainer

	return engine, trainer
}

// loadNoFlyZonesToEngine reads no-fly zones from DB and sets them on the engine.
func loadNoFlyZonesToEngine(database *db.DB, engine *simulation.Engine) {
	rows, err := database.Query(`SELECT name, zone_type, shape_json, altitude_limit FROM no_fly_zones`)
	if err != nil {
		log.Printf("[SimIntegration] load no-fly zones warning: %v", err)
		return
	}
	defer rows.Close()
	var zones []simulation.NoFlyZone
	for rows.Next() {
		var name, zoneType, shapeJSON string
		var altLimit int
		if err := rows.Scan(&name, &zoneType, &shapeJSON, &altLimit); err != nil {
			continue
		}
		// Parse shape_json: array of [lat, lng] or [{lat, lng}]
		verts := parseShapeJSON(shapeJSON)
		if len(verts) < 3 {
			continue
		}
		zones = append(zones, simulation.NoFlyZone{
			Name:     name,
			ZoneType: zoneType,
			AltLimit: altLimit,
			Vertices: verts,
		})
	}
	engine.SetNoFlyZones(zones)
}

// parseShapeJSON parses various formats of polygon shape JSON into [][2]float64 {lat, lng}.
func parseShapeJSON(raw string) [][2]float64 {
	// Try format: [[lat, lng], ...] — array of arrays
	var arrArr [][2]float64
	if json.Unmarshal([]byte(raw), &arrArr) == nil && len(arrArr) > 0 {
		return arrArr
	}
	// Try format: [{"lat":..., "lng":...}, ...] — array of objects
	var objArr []struct {
		Lat float64 `json:"lat"`
		Lng float64 `json:"lng"`
	}
	if json.Unmarshal([]byte(raw), &objArr) == nil && len(objArr) > 0 {
		result := make([][2]float64, len(objArr))
		for i, o := range objArr {
			result[i] = [2]float64{o.Lat, o.Lng}
		}
		return result
	}
	return nil
}

func estimateRemaining(level int) string {
	if level <= 0 {
		return "0分钟"
	}
	// Rough estimate: 100% = ~25 min flight at cruise
	minutes := float64(level) / 100.0 * 25.0
	if minutes < 1 {
		return "不足1分钟"
	}
	return fmt.Sprintf("约%.0f分钟", minutes)
}
