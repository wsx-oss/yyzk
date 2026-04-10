package simulation

import (
	"sync"
	"time"
)

// ---------- Enums / Constants ----------

// FlightPhase represents the current flight phase of a simulated drone.
type FlightPhase string

const (
	PhaseIdle    FlightPhase = "待飞"
	PhaseTakeoff FlightPhase = "起飞"
	PhaseCruise  FlightPhase = "巡航"
	PhaseWork    FlightPhase = "作业"
	PhaseReturn  FlightPhase = "返航"
	PhaseLanding FlightPhase = "降落"
	PhaseHover   FlightPhase = "悬停"
)

// TaskStatus represents the mission/task status.
type TaskStatus string

const (
	TaskNotStarted TaskStatus = "未开始"
	TaskRunning    TaskStatus = "执行中"
	TaskCompleted  TaskStatus = "已完成"
	TaskPaused     TaskStatus = "已暂停"
	TaskFailed     TaskStatus = "任务失败"
)

// InstanceState represents the simulation instance lifecycle state.
type InstanceState string

const (
	StateCreated InstanceState = "created"
	StateRunning InstanceState = "running"
	StateStopped InstanceState = "stopped"
	StateFailed  InstanceState = "failed"
	StateDeleted InstanceState = "deleted"
)

// AnomalyType represents types of injectable anomalies.
type AnomalyType string

const (
	AnomalyLowBattery AnomalyType = "low_battery"
	AnomalyDeviation  AnomalyType = "flight_deviation"
	AnomalyCommLost   AnomalyType = "comm_lost"
	AnomalyTempHigh   AnomalyType = "temp_high"
	AnomalyTempLow    AnomalyType = "temp_low"
)

// AlertLevel represents severity levels.
type AlertLevel string

const (
	AlertInfo     AlertLevel = "提示"
	AlertWarning  AlertLevel = "警告"
	AlertCritical AlertLevel = "紧急故障"
)

// ---------- Data Structures ----------

// MissionType represents the type of drone mission.
type MissionType string

const (
	MissionPatrol     MissionType = "patrol"     // 巡逻任务
	MissionInspection MissionType = "inspection" // 电力/管道巡检
	MissionDelivery   MissionType = "delivery"   // 物流配送
	MissionSurvey     MissionType = "survey"     // 区域测绘
	MissionSAR        MissionType = "sar"        // 搜救任务
)

// Waypoint represents a single point in a flight route.
type Waypoint struct {
	Lat     float64 `json:"lat"`
	Lng     float64 `json:"lng"`
	Alt     float64 `json:"alt"`
	Speed   float64 `json:"speed"`    // target speed m/s
	Action  string  `json:"action"`   // fly, hover, photo, spray, inspect, drop
	HoldSec int     `json:"hold_sec"` // seconds to hold at waypoint
	Name    string  `json:"name"`     // waypoint description
}

// GPSData represents GPS telemetry data aligned with real drone format.
type GPSData struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Altitude  float64 `json:"altitude"`
	Speed     float64 `json:"speed"`
	Heading   float64 `json:"heading"`
	Accuracy  float64 `json:"accuracy"`
}

// BatteryData represents battery telemetry data.
type BatteryData struct {
	Level        int     `json:"level"`       // 0-100
	Voltage      float64 `json:"voltage"`     // V
	Current      float64 `json:"current"`     // A (negative = discharging)
	Temperature  float64 `json:"temperature"` // °C
	Health       int     `json:"health"`      // 0-100
	ChargeCycles int     `json:"charge_cycles"`
	Status       string  `json:"status"` // 正常/电量低/严重不足/温度过高
}

// TelemetrySnapshot is a complete telemetry frame for one instant.
type TelemetrySnapshot struct {
	DeviceID      string         `json:"device_id"`
	Timestamp     time.Time      `json:"timestamp"`
	GPS           GPSData        `json:"gps"`
	Battery       BatteryData    `json:"battery"`
	FlightPhase   FlightPhase    `json:"flight_phase"`
	TaskStatus    TaskStatus     `json:"task_status"`
	Anomalies     []AnomalyEvent `json:"anomalies,omitempty"`
	WaypointIdx   int            `json:"waypoint_idx"`
	WaypointTotal int            `json:"waypoint_total"`
	RouteProgress float64        `json:"route_progress"` // 0.0-1.0 overall route completion
}

// AnomalyEvent represents an active anomaly on an instance.
type AnomalyEvent struct {
	Type      AnomalyType   `json:"type"`
	Level     AlertLevel    `json:"level"`
	Message   string        `json:"message"`
	StartTime time.Time     `json:"start_time"`
	Duration  time.Duration `json:"duration"`
	Active    bool          `json:"active"`
}

// AnomalyConfig describes how to inject an anomaly.
type AnomalyConfig struct {
	Type      AnomalyType   `json:"type"`
	Level     AlertLevel    `json:"level"`
	Duration  time.Duration `json:"duration"`
	Trigger   string        `json:"trigger"`    // immediate, time_based, condition
	TriggerAt time.Time     `json:"trigger_at"` // for time_based
	Condition string        `json:"condition"`  // e.g. "battery<30"
}

// ---------- Instance Config ----------

// InstanceConfig holds the configuration for a single simulated drone.
type InstanceConfig struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Model       string      `json:"model"`
	BatchID     string      `json:"batch_id"`
	Mission     MissionType `json:"mission"`      // mission type
	MissionDesc string      `json:"mission_desc"` // human readable mission description
	InitialLat  float64     `json:"initial_lat"`
	InitialLng  float64     `json:"initial_lng"`
	InitialAlt  float64     `json:"initial_alt"`
	Waypoints   []Waypoint  `json:"waypoints"`
	CruiseSpeed float64     `json:"cruise_speed"` // m/s, default 15
	MaxAlt      float64     `json:"max_alt"`      // meters, default 120
	BatteryFull int         `json:"battery_full"` // initial level, default 100
	LoopRoute   bool        `json:"loop_route"`   // loop waypoints continuously
}

// ---------- Instance Runtime State (persisted for recovery) ----------

// InstanceSnapshot captures the full state of an instance for persistence/recovery.
type InstanceSnapshot struct {
	Config         InstanceConfig `json:"config"`
	State          InstanceState  `json:"state"`
	Phase          FlightPhase    `json:"flight_phase"`
	TaskStatus     TaskStatus     `json:"task_status"`
	GPS            GPSData        `json:"gps"`
	Battery        BatteryData    `json:"battery"`
	WaypointIdx    int            `json:"waypoint_idx"`
	WaypointProg   float64        `json:"waypoint_progress"` // 0.0 - 1.0 within segment
	LoopCount      int            `json:"loop_count"`
	Anomalies      []AnomalyEvent `json:"anomalies"`
	TotalFlightSec float64        `json:"total_flight_sec"`
	LastUpdate     time.Time      `json:"last_update"`
	CreatedAt      time.Time      `json:"created_at"`
	ErrorMsg       string         `json:"error_msg,omitempty"`
}

// ---------- Batch ----------

// BatchConfig holds the configuration for a batch of simulated drones.
type BatchConfig struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Count       int         `json:"count"`
	Model       string      `json:"model"`
	Mission     MissionType `json:"mission"`      // mission type for the batch
	MissionDesc string      `json:"mission_desc"` // human readable description
	CenterLat   float64     `json:"center_lat"`
	CenterLng   float64     `json:"center_lng"`
	SpreadM     float64     `json:"spread_m"` // spread radius in meters
	CruiseSpeed float64     `json:"cruise_speed"`
	MaxAlt      float64     `json:"max_alt"`
	LoopRoute   bool        `json:"loop_route"`
	Waypoints   []Waypoint  `json:"waypoints"` // shared waypoints (offset per drone)
	CreatedAt   time.Time   `json:"created_at"`
}

// ---------- Engine Metrics ----------

// EngineMetrics holds runtime statistics for the simulation engine.
type EngineMetrics struct {
	TotalInstances   int     `json:"total_instances"`
	RunningInstances int     `json:"running_instances"`
	StoppedInstances int     `json:"stopped_instances"`
	FailedInstances  int     `json:"failed_instances"`
	TotalBatches     int     `json:"total_batches"`
	GoroutineCount   int     `json:"goroutine_count"`
	MemoryUsageMB    float64 `json:"memory_usage_mb"`
	UptimeSec        float64 `json:"uptime_sec"`
}

// ---------- Resource Limiter ----------

// ResourceLimiter controls concurrent simulation resources.
type ResourceLimiter struct {
	mu            sync.Mutex
	maxInstances  int
	maxGoroutines int
	current       int
}

func NewResourceLimiter(maxInstances, maxGoroutines int) *ResourceLimiter {
	return &ResourceLimiter{
		maxInstances:  maxInstances,
		maxGoroutines: maxGoroutines,
	}
}

func (rl *ResourceLimiter) Acquire() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if rl.current >= rl.maxInstances {
		return false
	}
	rl.current++
	return true
}

func (rl *ResourceLimiter) Release() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if rl.current > 0 {
		rl.current--
	}
}

func (rl *ResourceLimiter) Current() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return rl.current
}

func (rl *ResourceLimiter) Max() int {
	return rl.maxInstances
}
