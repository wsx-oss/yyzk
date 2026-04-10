package simulation

import (
	"context"
	"math"
	"math/rand"
	"sync"
	"time"
)

// Instance represents a single simulated drone running in its own goroutine.
type Instance struct {
	mu             sync.RWMutex
	config         InstanceConfig
	state          InstanceState
	phase          FlightPhase
	task           TaskStatus
	gps            GPSData
	battery        BatteryData
	batteryLevelF  float64 // precise battery level (avoids int truncation)
	wpIdx          int     // current waypoint index
	wpProg         float64 // progress within current segment [0,1]
	loopCnt        int
	anomalies      []AnomalyEvent
	totalFlightSec float64
	createdAt      time.Time
	lastTick       time.Time
	errorMsg       string

	cancel context.CancelFunc
	doneCh chan struct{}

	// callback to push telemetry to the DB/WebSocket layer
	onTelemetry func(TelemetrySnapshot)
	// callback when instance state changes
	onStateChange func(id string, state InstanceState)
}

// NewInstance creates a new simulation instance from config.
func NewInstance(cfg InstanceConfig, onTelemetry func(TelemetrySnapshot), onStateChange func(string, InstanceState)) *Instance {
	now := time.Now()
	batteryLevel := cfg.BatteryFull
	if batteryLevel <= 0 {
		batteryLevel = 100
	}
	cruiseSpeed := cfg.CruiseSpeed
	if cruiseSpeed <= 0 {
		cruiseSpeed = 15.0
	}
	cfg.CruiseSpeed = cruiseSpeed

	inst := &Instance{
		config: cfg,
		state:  StateCreated,
		phase:  PhaseIdle,
		task:   TaskNotStarted,
		gps: GPSData{
			Latitude:  cfg.InitialLat,
			Longitude: cfg.InitialLng,
			Altitude:  cfg.InitialAlt,
			Speed:     0,
			Heading:   0,
			Accuracy:  1.5,
		},
		batteryLevelF: float64(batteryLevel),
		battery: BatteryData{
			Level:        batteryLevel,
			Voltage:      4.2 * 6, // 6S LiPo
			Current:      0,
			Temperature:  25.0 + rand.Float64()*3,
			Health:       95 + rand.Intn(6),
			ChargeCycles: rand.Intn(50),
			Status:       "正常",
		},
		createdAt:     now,
		lastTick:      now,
		doneCh:        make(chan struct{}),
		onTelemetry:   onTelemetry,
		onStateChange: onStateChange,
	}
	return inst
}

// RestoreInstance recreates an instance from a persisted snapshot.
func RestoreInstance(snap InstanceSnapshot, onTelemetry func(TelemetrySnapshot), onStateChange func(string, InstanceState)) *Instance {
	inst := &Instance{
		config:         snap.Config,
		state:          snap.State,
		phase:          snap.Phase,
		task:           snap.TaskStatus,
		gps:            snap.GPS,
		batteryLevelF:  float64(snap.Battery.Level),
		battery:        snap.Battery,
		wpIdx:          snap.WaypointIdx,
		wpProg:         snap.WaypointProg,
		loopCnt:        snap.LoopCount,
		anomalies:      snap.Anomalies,
		totalFlightSec: snap.TotalFlightSec,
		createdAt:      snap.CreatedAt,
		lastTick:       snap.LastUpdate,
		errorMsg:       snap.ErrorMsg,
		doneCh:         make(chan struct{}),
		onTelemetry:    onTelemetry,
		onStateChange:  onStateChange,
	}
	return inst
}

// Snapshot returns the current state for persistence.
func (inst *Instance) Snapshot() InstanceSnapshot {
	inst.mu.RLock()
	defer inst.mu.RUnlock()
	return InstanceSnapshot{
		Config:         inst.config,
		State:          inst.state,
		Phase:          inst.phase,
		TaskStatus:     inst.task,
		GPS:            inst.gps,
		Battery:        inst.battery,
		WaypointIdx:    inst.wpIdx,
		WaypointProg:   inst.wpProg,
		LoopCount:      inst.loopCnt,
		Anomalies:      inst.anomalies,
		TotalFlightSec: inst.totalFlightSec,
		LastUpdate:     inst.lastTick,
		CreatedAt:      inst.createdAt,
		ErrorMsg:       inst.errorMsg,
	}
}

// ID returns the instance's device ID.
func (inst *Instance) ID() string {
	return inst.config.ID
}

// Config returns the instance configuration.
func (inst *Instance) Config() InstanceConfig {
	return inst.config
}

// State returns the current lifecycle state.
func (inst *Instance) State() InstanceState {
	inst.mu.RLock()
	defer inst.mu.RUnlock()
	return inst.state
}

// Telemetry returns the latest telemetry snapshot.
func (inst *Instance) Telemetry() TelemetrySnapshot {
	inst.mu.RLock()
	defer inst.mu.RUnlock()
	wpTotal := len(inst.config.Waypoints)
	routeProg := 0.0
	if wpTotal > 0 {
		routeProg = (float64(inst.wpIdx) + inst.wpProg) / float64(wpTotal)
		if routeProg > 1.0 {
			routeProg = 1.0
		}
	}
	if inst.task == TaskCompleted {
		routeProg = 1.0
	}
	return TelemetrySnapshot{
		DeviceID:      inst.config.ID,
		Timestamp:     inst.lastTick,
		GPS:           inst.gps,
		Battery:       inst.battery,
		FlightPhase:   inst.phase,
		TaskStatus:    inst.task,
		Anomalies:     append([]AnomalyEvent{}, inst.anomalies...),
		WaypointIdx:   inst.wpIdx,
		WaypointTotal: wpTotal,
		RouteProgress: routeProg,
	}
}

// Start begins the simulation loop in a new goroutine.
func (inst *Instance) Start(ctx context.Context) {
	inst.mu.Lock()
	if inst.state == StateRunning {
		inst.mu.Unlock()
		return
	}
	childCtx, cancel := context.WithCancel(ctx)
	inst.cancel = cancel
	inst.state = StateRunning
	inst.doneCh = make(chan struct{})
	if inst.task == TaskNotStarted || inst.task == TaskPaused {
		inst.task = TaskRunning
	}
	inst.mu.Unlock()

	if inst.onStateChange != nil {
		inst.onStateChange(inst.config.ID, StateRunning)
	}

	go inst.runLoop(childCtx)
}

// Stop gracefully stops the simulation instance.
func (inst *Instance) Stop() {
	inst.mu.Lock()
	if inst.state != StateRunning {
		inst.mu.Unlock()
		return
	}
	inst.state = StateStopped
	inst.task = TaskPaused
	if inst.cancel != nil {
		inst.cancel()
	}
	inst.mu.Unlock()

	// Wait for goroutine to exit (with timeout)
	select {
	case <-inst.doneCh:
	case <-time.After(3 * time.Second):
	}

	if inst.onStateChange != nil {
		inst.onStateChange(inst.config.ID, StateStopped)
	}
}

// InjectAnomaly adds an anomaly to the instance.
func (inst *Instance) InjectAnomaly(cfg AnomalyConfig) {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	event := AnomalyEvent{
		Type:      cfg.Type,
		Level:     cfg.Level,
		Message:   anomalyMessage(cfg.Type, cfg.Level, inst.config.Name),
		StartTime: time.Now(),
		Duration:  cfg.Duration,
		Active:    true,
	}
	inst.anomalies = append(inst.anomalies, event)
}

// ClearAnomalies removes all anomalies from the instance.
func (inst *Instance) ClearAnomalies() {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	inst.anomalies = nil
}

// UpdateConfig updates mutable configuration fields.
func (inst *Instance) UpdateConfig(speed float64, maxAlt float64, loopRoute bool) {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	if speed > 0 {
		inst.config.CruiseSpeed = speed
	}
	if maxAlt > 0 {
		inst.config.MaxAlt = maxAlt
	}
	inst.config.LoopRoute = loopRoute
}

// ApplyRLAction executes an RL-selected action on this instance, modifying its physical state.
// Returns true if the action was successfully applied.
func (inst *Instance) ApplyRLAction(action int, value float64) bool {
	inst.mu.Lock()
	defer inst.mu.Unlock()

	if inst.state != StateRunning {
		return false
	}

	switch action {
	case 0: // ActionAdjustHeading
		// Rotate heading by value degrees (clamped ±45)
		delta := math.Max(-45, math.Min(45, value))
		inst.gps.Heading = math.Mod(inst.gps.Heading+delta+360, 360)
		return true

	case 1: // ActionAdjustSpeed
		// Additive speed adjustment: value maps to delta m/s (clamped ±5)
		delta := (value - 1.0) * 10.0 // value=1.0 → no change, 0.7 → -3, 1.3 → +3
		delta = math.Max(-5, math.Min(5, delta))
		inst.config.CruiseSpeed += delta
		if inst.config.CruiseSpeed < 3 {
			inst.config.CruiseSpeed = 3
		}
		if inst.config.CruiseSpeed > 22 {
			inst.config.CruiseSpeed = 22
		}
		return true

	case 2: // ActionAdjustAlt
		// Adjust target altitude by value meters (clamped ±30)
		delta := math.Max(-30, math.Min(30, value))
		inst.gps.Altitude += delta
		if inst.gps.Altitude < 5 {
			inst.gps.Altitude = 5
		}
		if inst.gps.Altitude > inst.config.MaxAlt {
			inst.gps.Altitude = inst.config.MaxAlt
		}
		return true

	case 3: // ActionGotoWP — skip to next waypoint
		if len(inst.config.Waypoints) > 0 && inst.wpIdx < len(inst.config.Waypoints)-1 {
			inst.wpIdx++
			inst.wpProg = 0
			inst.phase = PhaseCruise
		}
		return true

	case 4: // ActionReturn — trigger return-to-home
		if inst.phase != PhaseReturn && inst.phase != PhaseLanding && inst.phase != PhaseIdle {
			inst.phase = PhaseReturn
			return true
		}
		return false

	case 5: // ActionHover — hold position
		if inst.phase == PhaseCruise || inst.phase == PhaseWork {
			inst.phase = PhaseHover
			inst.gps.Speed = 0
			return true
		}
		return false

	case 6: // ActionEmergencyLand — immediate descent
		inst.phase = PhaseLanding
		inst.gps.Speed = 1.0
		return true
	}
	return false
}

// ---------- Simulation Loop ----------

func (inst *Instance) runLoop(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			inst.mu.Lock()
			inst.state = StateFailed
			inst.errorMsg = "panic recovered in simulation loop"
			inst.mu.Unlock()
			if inst.onStateChange != nil {
				inst.onStateChange(inst.config.ID, StateFailed)
			}
		}
		close(inst.doneCh)
	}()

	ticker := time.NewTicker(1 * time.Second) // 1Hz simulation tick
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			inst.tick(now)
		}
	}
}

func (inst *Instance) tick(now time.Time) {
	inst.mu.Lock()
	dt := now.Sub(inst.lastTick).Seconds()
	if dt <= 0 {
		dt = 1.0
	}
	if dt > 5.0 {
		dt = 5.0 // cap dt to handle pauses
	}
	inst.lastTick = now

	// Check for comm_lost anomaly - if active, skip telemetry push
	commLost := false
	for i := range inst.anomalies {
		if inst.anomalies[i].Type == AnomalyCommLost && inst.anomalies[i].Active {
			commLost = true
		}
	}

	// Update anomaly timers
	inst.updateAnomalies(now)

	// Update battery
	inst.updateBattery(dt)

	// Check low battery forced return
	if inst.battery.Level <= 15 && inst.phase != PhaseReturn && inst.phase != PhaseLanding && inst.phase != PhaseIdle {
		inst.phase = PhaseReturn
		// Add low battery anomaly if not already present
		hasLowBat := false
		for _, a := range inst.anomalies {
			if a.Type == AnomalyLowBattery && a.Active {
				hasLowBat = true
			}
		}
		if !hasLowBat {
			inst.anomalies = append(inst.anomalies, AnomalyEvent{
				Type:      AnomalyLowBattery,
				Level:     AlertCritical,
				Message:   inst.config.Name + " 电量过低，强制返航",
				StartTime: now,
				Duration:  0, // until landing
				Active:    true,
			})
		}
	}

	// Update flight simulation
	inst.updateFlight(dt)

	// Apply deviation anomaly
	inst.applyDeviation()

	// Apply temperature anomaly
	inst.applyTempAnomaly()

	// Build telemetry snapshot with real route progress
	wpTotal := len(inst.config.Waypoints)
	routeProg := 0.0
	if wpTotal > 0 {
		routeProg = (float64(inst.wpIdx) + inst.wpProg) / float64(wpTotal)
		if routeProg > 1.0 {
			routeProg = 1.0
		}
	}
	if inst.task == TaskCompleted {
		routeProg = 1.0
	}
	snap := TelemetrySnapshot{
		DeviceID:      inst.config.ID,
		Timestamp:     now,
		GPS:           inst.gps,
		Battery:       inst.battery,
		FlightPhase:   inst.phase,
		TaskStatus:    inst.task,
		Anomalies:     append([]AnomalyEvent{}, inst.anomalies...),
		WaypointIdx:   inst.wpIdx,
		WaypointTotal: wpTotal,
		RouteProgress: routeProg,
	}
	inst.totalFlightSec += dt
	inst.mu.Unlock()

	// Push telemetry (outside lock)
	if !commLost && inst.onTelemetry != nil {
		inst.onTelemetry(snap)
	}
}

func (inst *Instance) updateAnomalies(now time.Time) {
	active := inst.anomalies[:0]
	for i := range inst.anomalies {
		a := &inst.anomalies[i]
		if a.Active && a.Duration > 0 && now.Sub(a.StartTime) > a.Duration {
			a.Active = false
		}
		// Keep all anomalies (active or historical)
		active = append(active, *a)
	}
	inst.anomalies = active
}

func (inst *Instance) updateBattery(dt float64) {
	// ---- Recharge when idle (simulate ground charging / battery swap for 7x24) ----
	if inst.phase == PhaseIdle && inst.batteryLevelF < 100 {
		// Fast recharge: ~120 seconds from 0→100 (simulate battery swap / fast-charge)
		chargeRate := 100.0 / 120.0 // %/s
		inst.batteryLevelF += chargeRate * dt
		if inst.batteryLevelF > 100 {
			inst.batteryLevelF = 100
		}
		inst.battery.Level = int(math.Round(inst.batteryLevelF))
		inst.battery.Current = 5.0 // charging
		inst.battery.Voltage = 21.0 + inst.batteryLevelF/100.0*4.2
		inst.battery.Status = "充电中"
		if inst.battery.Level >= 95 {
			inst.battery.Status = "正常"
		}
		// Auto-restart flight when battery is full enough
		if inst.batteryLevelF >= 95 && (inst.task == TaskCompleted || inst.task == TaskRunning || inst.task == TaskPaused) {
			inst.phase = PhaseTakeoff
			inst.task = TaskRunning
			inst.wpIdx = 0
			inst.wpProg = 0
			inst.loopCnt++
			inst.battery.ChargeCycles++
		}
		return
	}

	if inst.phase == PhaseIdle || inst.phase == PhaseLanding {
		inst.battery.Current = -0.5 // idle discharge
	} else if inst.phase == PhaseTakeoff {
		inst.battery.Current = -25.0 // high power takeoff
	} else if inst.phase == PhaseHover {
		inst.battery.Current = -15.0
	} else {
		inst.battery.Current = -18.0 // cruise/work
	}

	// Discharge: level drops proportional to current * dt
	// Assume 5000mAh battery, 6S = 22.2V nominal
	// Full discharge at 18A takes ~16.7 minutes
	discharge := (-inst.battery.Current * dt) / (5.0 * 3600) * 100
	inst.batteryLevelF -= discharge
	if inst.batteryLevelF < 0 {
		inst.batteryLevelF = 0
	}
	inst.battery.Level = int(math.Round(inst.batteryLevelF))

	// Voltage varies with level
	inst.battery.Voltage = 21.0 + inst.batteryLevelF/100.0*4.2

	// Temperature: slight fluctuation
	baseTempDelta := 0.01 * (rand.Float64() - 0.5)
	if inst.phase != PhaseIdle {
		baseTempDelta += 0.02 // slight heating during flight
	}
	inst.battery.Temperature += baseTempDelta
	if inst.battery.Temperature < 15 {
		inst.battery.Temperature = 15
	}
	if inst.battery.Temperature > 60 {
		inst.battery.Temperature = 60
	}

	// Status
	inst.battery.Status = "正常"
	if inst.battery.Level <= 10 {
		inst.battery.Status = "严重不足"
	} else if inst.battery.Level <= 20 {
		inst.battery.Status = "电量低"
	} else if inst.battery.Temperature >= 50 {
		inst.battery.Status = "温度过高"
	}
}

func (inst *Instance) updateFlight(dt float64) {
	wps := inst.config.Waypoints
	if len(wps) == 0 {
		// No waypoints: idle hover simulation at initial position
		if inst.phase == PhaseIdle {
			inst.phase = PhaseTakeoff
		}
		if inst.phase == PhaseTakeoff {
			inst.gps.Altitude += 2.0 * dt
			inst.gps.Speed = 2.0
			if inst.gps.Altitude >= 30 {
				inst.gps.Altitude = 30
				inst.phase = PhaseHover
				inst.gps.Speed = 0
			}
		}
		// Hover: slight drift
		if inst.phase == PhaseHover {
			inst.gps.Latitude += (rand.Float64() - 0.5) * 0.000001
			inst.gps.Longitude += (rand.Float64() - 0.5) * 0.000001
			inst.gps.Speed = rand.Float64() * 0.5
		}
		if inst.phase == PhaseReturn {
			inst.flyToward(inst.config.InitialLat, inst.config.InitialLng, inst.config.InitialAlt, dt)
			if inst.distTo(inst.config.InitialLat, inst.config.InitialLng) < 5 {
				inst.phase = PhaseLanding
			}
		}
		if inst.phase == PhaseLanding {
			inst.gps.Altitude -= 2.0 * dt
			inst.gps.Speed = 1.0
			if inst.gps.Altitude <= inst.config.InitialAlt {
				inst.gps.Altitude = inst.config.InitialAlt
				inst.gps.Speed = 0
				inst.phase = PhaseIdle
				inst.task = TaskCompleted
			}
		}
		return
	}

	switch inst.phase {
	case PhaseIdle:
		inst.phase = PhaseTakeoff
		inst.gps.Speed = 0

	case PhaseTakeoff:
		targetAlt := 30.0
		if len(wps) > 0 {
			targetAlt = wps[0].Alt
		}
		if targetAlt < 10 {
			targetAlt = 30
		}
		inst.gps.Altitude += 3.0 * dt
		inst.gps.Speed = 3.0
		if inst.gps.Altitude >= targetAlt {
			inst.gps.Altitude = targetAlt
			inst.phase = PhaseCruise
			inst.wpIdx = 0
			inst.wpProg = 0
		}

	case PhaseCruise, PhaseWork:
		if inst.wpIdx >= len(wps) {
			// Reached end of waypoints
			if inst.config.LoopRoute {
				inst.wpIdx = 0
				inst.wpProg = 0
				inst.loopCnt++
				inst.phase = PhaseCruise
			} else {
				inst.phase = PhaseReturn
			}
			return
		}

		wp := wps[inst.wpIdx]
		targetLat := wp.Lat
		targetLng := wp.Lng
		targetAlt := wp.Alt
		if targetAlt < 5 {
			targetAlt = 30
		}
		speed := inst.config.CruiseSpeed
		if wp.Speed > 0 {
			speed = wp.Speed
		}

		dist := inst.distTo(targetLat, targetLng)
		if dist < 3 { // reached waypoint
			// Handle waypoint action (hover/work)
			if wp.Action != "" && wp.Action != "fly" {
				inst.phase = PhaseWork
				if wp.HoldSec > 0 {
					// Simulate hold time via progress
					holdProg := inst.wpProg + dt/float64(wp.HoldSec)
					if holdProg >= 1.0 {
						inst.wpIdx++
						inst.wpProg = 0
						inst.phase = PhaseCruise
					} else {
						inst.wpProg = holdProg
						inst.gps.Speed = 0
					}
				} else {
					inst.wpIdx++
					inst.wpProg = 0
					inst.phase = PhaseCruise
				}
			} else {
				inst.wpIdx++
				inst.wpProg = 0
			}
			return
		}

		// Fly toward waypoint
		inst.flyToward(targetLat, targetLng, targetAlt, dt)
		inst.gps.Speed = speed
		segLen := inst.distTo(targetLat, targetLng)
		if segLen+dist > 0 {
			inst.wpProg = 1.0 - segLen/(segLen+1)
		}

	case PhaseReturn:
		inst.flyToward(inst.config.InitialLat, inst.config.InitialLng, inst.config.InitialAlt+30, dt)
		inst.gps.Speed = inst.config.CruiseSpeed
		if inst.distTo(inst.config.InitialLat, inst.config.InitialLng) < 5 {
			inst.phase = PhaseLanding
		}

	case PhaseLanding:
		inst.gps.Altitude -= 2.0 * dt
		inst.gps.Speed = 1.5
		if inst.gps.Altitude <= inst.config.InitialAlt+0.5 {
			inst.gps.Altitude = inst.config.InitialAlt
			inst.gps.Speed = 0
			inst.phase = PhaseIdle
			inst.task = TaskCompleted
			// If looping, restart
			if inst.config.LoopRoute {
				inst.phase = PhaseTakeoff
				inst.task = TaskRunning
				inst.wpIdx = 0
				inst.wpProg = 0
				// Simulate battery swap
				inst.batteryLevelF = float64(95 + rand.Intn(6))
				inst.battery.Level = int(inst.batteryLevelF)
				inst.battery.ChargeCycles++
			}
		}

	case PhaseHover:
		inst.gps.Latitude += (rand.Float64() - 0.5) * 0.000001
		inst.gps.Longitude += (rand.Float64() - 0.5) * 0.000001
		inst.gps.Speed = rand.Float64() * 0.3
		// Auto-resume from hover after ~10 seconds to prevent RL-triggered hover deadlock
		if inst.task == TaskRunning && inst.wpIdx < len(wps) {
			inst.totalFlightSec += 0 // no-op, just for readability
			// Use a simple probabilistic resume: ~10% chance per tick ≈ 10s avg hold
			if rand.Float64() < 0.1 {
				inst.phase = PhaseCruise
			}
		}
	}
}

func (inst *Instance) applyDeviation() {
	for _, a := range inst.anomalies {
		if a.Type == AnomalyDeviation && a.Active {
			// Add random offset to GPS position
			inst.gps.Latitude += (rand.Float64() - 0.5) * 0.0005
			inst.gps.Longitude += (rand.Float64() - 0.5) * 0.0005
			inst.gps.Accuracy = 10 + rand.Float64()*20 // degraded accuracy
		}
	}
}

func (inst *Instance) applyTempAnomaly() {
	for _, a := range inst.anomalies {
		if a.Active {
			switch a.Type {
			case AnomalyTempHigh:
				inst.battery.Temperature = 55 + rand.Float64()*10
				inst.battery.Status = "温度过高"
			case AnomalyTempLow:
				inst.battery.Temperature = -5 + rand.Float64()*5
			}
		}
	}
}

// flyToward moves the drone toward a target lat/lng/alt.
func (inst *Instance) flyToward(lat, lng, alt, dt float64) {
	speed := inst.config.CruiseSpeed
	// Convert speed (m/s) to degree delta (rough approximation)
	// 1 degree lat ≈ 111,000 meters
	dLat := lat - inst.gps.Latitude
	dLng := lng - inst.gps.Longitude
	dist := math.Sqrt(dLat*dLat+dLng*dLng) * 111000 // approximate meters
	if dist < 0.5 {
		inst.gps.Latitude = lat
		inst.gps.Longitude = lng
		return
	}

	// Normalize direction
	moveDist := speed * dt // meters to move this tick
	if moveDist > dist {
		moveDist = dist
	}
	ratio := moveDist / dist
	inst.gps.Latitude += dLat * ratio
	inst.gps.Longitude += dLng * ratio

	// Altitude
	dAlt := alt - inst.gps.Altitude
	if math.Abs(dAlt) > 0.5 {
		altSpeed := 2.0 * dt
		if dAlt > 0 {
			inst.gps.Altitude += altSpeed
		} else {
			inst.gps.Altitude -= altSpeed
		}
	}

	// Heading (degrees from north)
	inst.gps.Heading = math.Mod(math.Atan2(dLng, dLat)*180/math.Pi+360, 360)

	// GPS noise
	inst.gps.Latitude += (rand.Float64() - 0.5) * 0.0000005
	inst.gps.Longitude += (rand.Float64() - 0.5) * 0.0000005
	inst.gps.Accuracy = 1.0 + rand.Float64()*2.0
}

// distTo returns approximate distance in meters to a lat/lng.
func (inst *Instance) distTo(lat, lng float64) float64 {
	dLat := (lat - inst.gps.Latitude) * 111000
	dLng := (lng - inst.gps.Longitude) * 111000 * math.Cos(inst.gps.Latitude*math.Pi/180)
	return math.Sqrt(dLat*dLat + dLng*dLng)
}

// anomalyMessage generates a human-readable anomaly message.
func anomalyMessage(aType AnomalyType, level AlertLevel, droneName string) string {
	switch aType {
	case AnomalyLowBattery:
		return droneName + " 低电量告警，触发强制返航"
	case AnomalyDeviation:
		return droneName + " 偏离预设航线"
	case AnomalyCommLost:
		return droneName + " 通信失联，数据上报中断"
	case AnomalyTempHigh:
		return droneName + " 设备温度异常（超温告警）"
	case AnomalyTempLow:
		return droneName + " 设备温度异常（低温告警）"
	default:
		return droneName + " 未知异常"
	}
}
