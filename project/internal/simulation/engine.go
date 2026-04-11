package simulation

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

const (
	defaultMaxInstances  = 200
	defaultMaxGoroutines = 500
	snapshotFile         = "data/sim_snapshots.json"
	snapshotInterval     = 30 * time.Second
)

// Engine is the high-concurrency multi-instance parallel simulation engine.
// Each simulated drone runs as an isolated instance in a managed goroutine.
type Engine struct {
	mu        sync.RWMutex
	instances map[string]*Instance // deviceID -> instance
	batches   map[string]*BatchConfig
	limiter   *ResourceLimiter
	ctx       context.Context
	cancel    context.CancelFunc
	startTime time.Time
	dataDir   string

	// No-fly zone geofence data
	nfzMu      sync.RWMutex
	noFlyZones []NoFlyZone

	// Callbacks for integrating with the main application
	onTelemetry   func(TelemetrySnapshot) // push telemetry to DB/WS
	onStateChange func(string, InstanceState)
	onAnomaly     func(string, AnomalyEvent) // push anomaly events

	snapshotMu sync.Mutex
}

// EngineConfig holds engine initialization parameters.
type EngineConfig struct {
	MaxInstances  int
	MaxGoroutines int
	DataDir       string
	OnTelemetry   func(TelemetrySnapshot)
	OnStateChange func(string, InstanceState)
	OnAnomaly     func(string, AnomalyEvent)
}

// NewEngine creates and starts the simulation engine.
func NewEngine(cfg EngineConfig) *Engine {
	if cfg.MaxInstances <= 0 {
		cfg.MaxInstances = defaultMaxInstances
	}
	if cfg.MaxGoroutines <= 0 {
		cfg.MaxGoroutines = defaultMaxGoroutines
	}
	if cfg.DataDir == "" {
		cfg.DataDir = "data"
	}
	ctx, cancel := context.WithCancel(context.Background())
	e := &Engine{
		instances:     make(map[string]*Instance),
		batches:       make(map[string]*BatchConfig),
		limiter:       NewResourceLimiter(cfg.MaxInstances, cfg.MaxGoroutines),
		ctx:           ctx,
		cancel:        cancel,
		startTime:     time.Now(),
		dataDir:       cfg.DataDir,
		onTelemetry:   cfg.OnTelemetry,
		onStateChange: cfg.OnStateChange,
		onAnomaly:     cfg.OnAnomaly,
	}

	// Start periodic snapshot persistence
	go e.snapshotLoop()
	// Start health monitor for auto-restart of failed instances
	go e.healthMonitorLoop()
	// Start collision avoidance for safe multi-drone operation
	go e.collisionAvoidanceLoop()

	log.Printf("[SimEngine] started: maxInstances=%d, maxGoroutines=%d", cfg.MaxInstances, cfg.MaxGoroutines)
	return e
}

// SetNoFlyZones updates the engine's geofence data.
func (e *Engine) SetNoFlyZones(zones []NoFlyZone) {
	e.nfzMu.Lock()
	defer e.nfzMu.Unlock()
	e.noFlyZones = zones
	log.Printf("[SimEngine] loaded %d no-fly zones for geofence", len(zones))
}

// GetNoFlyZones returns a copy of current no-fly zones.
func (e *Engine) GetNoFlyZones() []NoFlyZone {
	e.nfzMu.RLock()
	defer e.nfzMu.RUnlock()
	out := make([]NoFlyZone, len(e.noFlyZones))
	copy(out, e.noFlyZones)
	return out
}

// CheckGeofence returns the name of the violated no-fly zone, or empty string if safe.
func (e *Engine) CheckGeofence(lat, lng float64) string {
	e.nfzMu.RLock()
	defer e.nfzMu.RUnlock()
	for _, z := range e.noFlyZones {
		if pointInPolygon(lat, lng, z.Vertices) {
			return z.Name
		}
	}
	return ""
}

// pointInPolygon uses the ray-casting algorithm to test if (lat,lng) is inside a polygon.
func pointInPolygon(lat, lng float64, verts [][2]float64) bool {
	n := len(verts)
	if n < 3 {
		return false
	}
	inside := false
	j := n - 1
	for i := 0; i < n; i++ {
		yi, xi := verts[i][0], verts[i][1]
		yj, xj := verts[j][0], verts[j][1]
		if ((yi > lat) != (yj > lat)) && (lng < (xj-xi)*(lat-yi)/(yj-yi)+xi) {
			inside = !inside
		}
		j = i
	}
	return inside
}

// Shutdown gracefully stops all instances and persists state.
func (e *Engine) Shutdown() {
	log.Println("[SimEngine] shutting down...")
	e.cancel()

	e.mu.RLock()
	instances := make([]*Instance, 0, len(e.instances))
	for _, inst := range e.instances {
		instances = append(instances, inst)
	}
	e.mu.RUnlock()

	// Stop all running instances
	var wg sync.WaitGroup
	for _, inst := range instances {
		wg.Add(1)
		go func(i *Instance) {
			defer wg.Done()
			i.Stop()
		}(inst)
	}
	wg.Wait()

	// Final snapshot save
	e.PersistSnapshots()
	log.Println("[SimEngine] shutdown complete")
}

// ---------- Batch Operations ----------

// CreateBatch creates a batch of simulated drones.
func (e *Engine) CreateBatch(cfg BatchConfig) ([]string, error) {
	if cfg.Count <= 0 || cfg.Count > 500 {
		return nil, fmt.Errorf("batch count must be 1-500, got %d", cfg.Count)
	}
	if cfg.ID == "" {
		cfg.ID = fmt.Sprintf("batch_%d_%d", time.Now().UnixMilli(), rand.Intn(1000))
	}
	if cfg.Name == "" {
		cfg.Name = "批次-" + cfg.ID
	}
	if cfg.CruiseSpeed <= 0 {
		cfg.CruiseSpeed = 15.0
	}
	if cfg.MaxAlt <= 0 {
		cfg.MaxAlt = 120
	}
	cfg.CreatedAt = time.Now()

	e.mu.Lock()
	e.batches[cfg.ID] = &cfg
	e.mu.Unlock()

	ids := make([]string, 0, cfg.Count)
	var errs []error
	var mu sync.Mutex

	// Default mission type
	if cfg.Mission == "" {
		cfg.Mission = MissionPatrol
	}

	// Auto-generate waypoints if none provided, using mission-specific templates
	if len(cfg.Waypoints) == 0 {
		wps, desc := GenerateMissionWaypoints(cfg.Mission, cfg.CenterLat, cfg.CenterLng, cfg.SpreadM, cfg.MaxAlt)
		cfg.Waypoints = wps
		if cfg.MissionDesc == "" {
			cfg.MissionDesc = desc
		}
	}

	// Create instances in parallel using worker pool pattern
	sem := make(chan struct{}, 32) // limit concurrent creation
	var wg sync.WaitGroup

	for i := 0; i < cfg.Count; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			// Spread drones around center point
			angle := 2 * math.Pi * float64(idx) / float64(cfg.Count)
			spreadDeg := cfg.SpreadM / 111000.0 // meters to degrees
			if spreadDeg < 0.0001 {
				spreadDeg = 0.001
			}
			lat := cfg.CenterLat + spreadDeg*math.Cos(angle)*(0.5+rand.Float64()*0.5)
			lng := cfg.CenterLng + spreadDeg*math.Sin(angle)*(0.5+rand.Float64()*0.5)

			deviceID := fmt.Sprintf("SIM-%s-%03d", cfg.ID, idx+1)
			name := fmt.Sprintf("%s-无人机%03d", cfg.Name, idx+1)

			// Build unique waypoints per drone:
			// 1) Shift to drone's start position
			// 2) Rotate pattern by a per-drone angle for route diversity
			// 3) Add per-waypoint random jitter
			rotAngle := 2 * math.Pi * float64(idx) / float64(cfg.Count) // unique rotation per drone
			cosR := math.Cos(rotAngle)
			sinR := math.Sin(rotAngle)
			cosLat := math.Cos(cfg.CenterLat * math.Pi / 180)
			wps := make([]Waypoint, len(cfg.Waypoints))
			for j, wp := range cfg.Waypoints {
				// Vector from batch center to waypoint (in degrees)
				dLat := wp.Lat - cfg.CenterLat
				dLng := wp.Lng - cfg.CenterLng
				// Rotate around center (latitude-corrected)
				rLat := dLat*cosR + dLng*cosLat*sinR
				rLng := -dLat*sinR/cosLat + dLng*cosR
				// Translate to drone's start position + add jitter (±30m)
				jitter := 30.0 / 111000.0 // 30 meters in degrees
				wps[j] = Waypoint{
					Lat:     lat + rLat + (rand.Float64()-0.5)*jitter,
					Lng:     lng + rLng + (rand.Float64()-0.5)*jitter/cosLat,
					Alt:     wp.Alt + (rand.Float64()-0.5)*6,
					Speed:   wp.Speed,
					Action:  wp.Action,
					HoldSec: wp.HoldSec,
					Name:    wp.Name,
				}
			}
			// Stagger start: each drone begins at a different waypoint index
			startWpIdx := 0
			if len(wps) > 1 {
				startWpIdx = idx % len(wps)
				if startWpIdx > 0 {
					rotated := make([]Waypoint, len(wps))
					for j := range wps {
						rotated[j] = wps[(j+startWpIdx)%len(wps)]
					}
					wps = rotated
				}
			}

			instCfg := InstanceConfig{
				ID:          deviceID,
				Name:        name,
				Model:       cfg.Model,
				BatchID:     cfg.ID,
				Mission:     cfg.Mission,
				MissionDesc: cfg.MissionDesc,
				InitialLat:  lat,
				InitialLng:  lng,
				InitialAlt:  0,
				Waypoints:   wps,
				CruiseSpeed: cfg.CruiseSpeed + (rand.Float64()-0.5)*2,
				MaxAlt:      cfg.MaxAlt,
				BatteryFull: 95 + rand.Intn(6),
				LoopRoute:   cfg.LoopRoute,
			}

			err := e.CreateInstance(instCfg)
			mu.Lock()
			if err != nil {
				errs = append(errs, fmt.Errorf("instance %s: %v", deviceID, err))
			} else {
				ids = append(ids, deviceID)
			}
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	if len(errs) > 0 {
		log.Printf("[SimEngine] batch %s: created %d/%d instances, %d errors", cfg.ID, len(ids), cfg.Count, len(errs))
	} else {
		log.Printf("[SimEngine] batch %s: created %d instances", cfg.ID, len(ids))
	}

	return ids, nil
}

// StartBatch starts all instances in a batch.
func (e *Engine) StartBatch(batchID string) (int, error) {
	e.mu.RLock()
	instances := e.getInstancesByBatch(batchID)
	e.mu.RUnlock()

	if len(instances) == 0 {
		return 0, fmt.Errorf("no instances found for batch %s", batchID)
	}

	count := 0
	for _, inst := range instances {
		if inst.State() != StateRunning {
			inst.Start(e.ctx)
			count++
		}
	}
	return count, nil
}

// StopBatch stops all instances in a batch.
func (e *Engine) StopBatch(batchID string) (int, error) {
	e.mu.RLock()
	instances := e.getInstancesByBatch(batchID)
	e.mu.RUnlock()

	if len(instances) == 0 {
		return 0, fmt.Errorf("no instances found for batch %s", batchID)
	}

	count := 0
	var wg sync.WaitGroup
	for _, inst := range instances {
		if inst.State() == StateRunning {
			wg.Add(1)
			go func(i *Instance) {
				defer wg.Done()
				i.Stop()
			}(inst)
			count++
		}
	}
	wg.Wait()
	return count, nil
}

// DeleteBatch removes all instances in a batch.
func (e *Engine) DeleteBatch(batchID string) (int, error) {
	e.mu.Lock()
	toStop := make([]*Instance, 0)
	ids := make([]string, 0)
	for id, inst := range e.instances {
		if inst.config.BatchID == batchID {
			ids = append(ids, id)
			if inst.State() == StateRunning {
				toStop = append(toStop, inst)
			}
		}
	}
	e.mu.Unlock()

	// Stop running instances with proper goroutine cleanup (outside write lock)
	var wg sync.WaitGroup
	for _, inst := range toStop {
		wg.Add(1)
		go func(i *Instance) {
			defer wg.Done()
			i.Stop()
		}(inst)
	}
	wg.Wait()

	// Now remove from maps under write lock
	e.mu.Lock()
	for _, id := range ids {
		delete(e.instances, id)
		e.limiter.Release()
	}
	delete(e.batches, batchID)
	e.mu.Unlock()

	return len(ids), nil
}

// ---------- Single Instance Operations ----------

// CreateInstance creates and registers a single simulation instance.
func (e *Engine) CreateInstance(cfg InstanceConfig) error {
	if !e.limiter.Acquire() {
		return fmt.Errorf("resource limit reached: max %d instances", e.limiter.Max())
	}

	inst := NewInstance(cfg, e.onTelemetry, e.onStateChange)
	inst.geofenceCheck = e.CheckGeofence

	e.mu.Lock()
	if _, exists := e.instances[cfg.ID]; exists {
		e.mu.Unlock()
		e.limiter.Release()
		return fmt.Errorf("instance %s already exists", cfg.ID)
	}
	e.instances[cfg.ID] = inst
	e.mu.Unlock()

	return nil
}

// StartInstance starts a single instance by ID.
func (e *Engine) StartInstance(id string) error {
	e.mu.RLock()
	inst, ok := e.instances[id]
	e.mu.RUnlock()
	if !ok {
		return fmt.Errorf("instance %s not found", id)
	}
	inst.Start(e.ctx)
	return nil
}

// StopInstance stops a single instance by ID.
func (e *Engine) StopInstance(id string) error {
	e.mu.RLock()
	inst, ok := e.instances[id]
	e.mu.RUnlock()
	if !ok {
		return fmt.Errorf("instance %s not found", id)
	}
	inst.Stop()
	return nil
}

// DeleteInstance removes a single instance.
func (e *Engine) DeleteInstance(id string) error {
	e.mu.Lock()
	inst, ok := e.instances[id]
	if !ok {
		e.mu.Unlock()
		return fmt.Errorf("instance %s not found", id)
	}
	delete(e.instances, id)
	e.mu.Unlock()

	if inst.State() == StateRunning {
		inst.Stop()
	}
	e.limiter.Release()
	return nil
}

// GetInstance returns an instance by ID.
func (e *Engine) GetInstance(id string) (*Instance, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	inst, ok := e.instances[id]
	return inst, ok
}

// UpdateInstanceConfig updates a single instance's mutable config.
func (e *Engine) UpdateInstanceConfig(id string, speed, maxAlt float64, loopRoute bool) error {
	e.mu.RLock()
	inst, ok := e.instances[id]
	e.mu.RUnlock()
	if !ok {
		return fmt.Errorf("instance %s not found", id)
	}
	inst.UpdateConfig(speed, maxAlt, loopRoute)
	return nil
}

// ---------- Anomaly Injection ----------

// InjectAnomaly injects an anomaly into a single instance.
func (e *Engine) InjectAnomaly(id string, cfg AnomalyConfig) error {
	e.mu.RLock()
	inst, ok := e.instances[id]
	e.mu.RUnlock()
	if !ok {
		return fmt.Errorf("instance %s not found", id)
	}
	inst.InjectAnomaly(cfg)
	if e.onAnomaly != nil {
		e.onAnomaly(id, AnomalyEvent{
			Type:      cfg.Type,
			Level:     cfg.Level,
			Message:   anomalyMessage(cfg.Type, cfg.Level, inst.Config().Name),
			StartTime: time.Now(),
			Duration:  cfg.Duration,
			Active:    true,
		})
	}
	return nil
}

// InjectBatchAnomaly injects anomalies into a percentage of instances in a batch.
func (e *Engine) InjectBatchAnomaly(batchID string, cfg AnomalyConfig, percent float64) (int, error) {
	e.mu.RLock()
	instances := e.getInstancesByBatch(batchID)
	e.mu.RUnlock()

	if len(instances) == 0 {
		return 0, fmt.Errorf("no instances in batch %s", batchID)
	}

	if percent <= 0 || percent > 100 {
		percent = 100
	}

	count := int(math.Ceil(float64(len(instances)) * percent / 100.0))
	if count > len(instances) {
		count = len(instances)
	}

	// Shuffle and pick
	perm := rand.Perm(len(instances))
	injected := 0
	for i := 0; i < count; i++ {
		inst := instances[perm[i]]
		inst.InjectAnomaly(cfg)
		injected++
		if e.onAnomaly != nil {
			e.onAnomaly(inst.ID(), AnomalyEvent{
				Type:      cfg.Type,
				Level:     cfg.Level,
				Message:   anomalyMessage(cfg.Type, cfg.Level, inst.Config().Name),
				StartTime: time.Now(),
				Duration:  cfg.Duration,
				Active:    true,
			})
		}
	}
	return injected, nil
}

// ClearInstanceAnomalies clears anomalies from a single instance.
func (e *Engine) ClearInstanceAnomalies(id string) error {
	e.mu.RLock()
	inst, ok := e.instances[id]
	e.mu.RUnlock()
	if !ok {
		return fmt.Errorf("instance %s not found", id)
	}
	inst.ClearAnomalies()
	return nil
}

// ---------- Query ----------

// ListInstances returns all instance snapshots, optionally filtered by batch.
func (e *Engine) ListInstances(batchID string) []InstanceSnapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var result []InstanceSnapshot
	for _, inst := range e.instances {
		if batchID != "" && inst.config.BatchID != batchID {
			continue
		}
		result = append(result, inst.Snapshot())
	}
	return result
}

// ListBatches returns all batch configurations.
func (e *Engine) ListBatches() []*BatchConfig {
	e.mu.RLock()
	defer e.mu.RUnlock()

	result := make([]*BatchConfig, 0, len(e.batches))
	for _, b := range e.batches {
		result = append(result, b)
	}
	return result
}

// GetBatch returns a batch configuration by ID.
func (e *Engine) GetBatch(id string) (*BatchConfig, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	b, ok := e.batches[id]
	return b, ok
}

// Metrics returns engine-level metrics.
func (e *Engine) Metrics() EngineMetrics {
	e.mu.RLock()
	defer e.mu.RUnlock()

	m := EngineMetrics{
		TotalBatches: len(e.batches),
		UptimeSec:    time.Since(e.startTime).Seconds(),
	}

	for _, inst := range e.instances {
		m.TotalInstances++
		switch inst.State() {
		case StateRunning:
			m.RunningInstances++
		case StateStopped:
			m.StoppedInstances++
		case StateFailed:
			m.FailedInstances++
		}
	}

	m.GoroutineCount = runtime.NumGoroutine()
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	m.MemoryUsageMB = float64(memStats.Alloc) / 1024 / 1024

	return m
}

// ---------- Persistence for 7x24 Recovery ----------

// PersistSnapshots saves all instance states to disk.
func (e *Engine) PersistSnapshots() {
	e.snapshotMu.Lock()
	defer e.snapshotMu.Unlock()

	e.mu.RLock()
	type PersistData struct {
		Batches   map[string]*BatchConfig `json:"batches"`
		Instances []InstanceSnapshot      `json:"instances"`
		SavedAt   time.Time               `json:"saved_at"`
	}
	data := PersistData{
		Batches:   e.batches,
		Instances: make([]InstanceSnapshot, 0, len(e.instances)),
		SavedAt:   time.Now(),
	}
	for _, inst := range e.instances {
		data.Instances = append(data.Instances, inst.Snapshot())
	}
	e.mu.RUnlock()

	path := filepath.Join(e.dataDir, "sim_snapshots.json")
	os.MkdirAll(filepath.Dir(path), 0o755)
	raw, err := json.Marshal(data)
	if err != nil {
		log.Printf("[SimEngine] snapshot marshal error: %v", err)
		return
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		log.Printf("[SimEngine] snapshot write error: %v", err)
	}
}

// RestoreFromSnapshots loads persisted state and recreates instances.
func (e *Engine) RestoreFromSnapshots() (int, error) {
	path := filepath.Join(e.dataDir, "sim_snapshots.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	type PersistData struct {
		Batches   map[string]*BatchConfig `json:"batches"`
		Instances []InstanceSnapshot      `json:"instances"`
		SavedAt   time.Time               `json:"saved_at"`
	}
	var data PersistData
	if err := json.Unmarshal(raw, &data); err != nil {
		return 0, fmt.Errorf("unmarshal snapshots: %w", err)
	}

	e.mu.Lock()
	// Restore batches
	for id, b := range data.Batches {
		e.batches[id] = b
	}

	// Restore instances
	restored := 0
	for _, snap := range data.Instances {
		if snap.State == StateDeleted {
			continue
		}
		if !e.limiter.Acquire() {
			log.Printf("[SimEngine] restore: resource limit reached, skipping remaining")
			break
		}
		inst := RestoreInstance(snap, e.onTelemetry, e.onStateChange)
		e.instances[snap.Config.ID] = inst
		restored++

		// Auto-restart instances that were running.
		// Reset state to Stopped first — Start() short-circuits if state is already Running.
		if snap.State == StateRunning {
			inst.mu.Lock()
			inst.state = StateStopped
			inst.task = TaskPaused
			inst.mu.Unlock()
			inst.Start(e.ctx)
		}
	}
	e.mu.Unlock()

	log.Printf("[SimEngine] restored %d instances from snapshot (saved at %v)", restored, data.SavedAt)
	return restored, nil
}

func (e *Engine) snapshotLoop() {
	ticker := time.NewTicker(snapshotInterval)
	defer ticker.Stop()
	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
			e.PersistSnapshots()
		}
	}
}

// ---------- Helpers ----------

func (e *Engine) getInstancesByBatch(batchID string) []*Instance {
	result := make([]*Instance, 0)
	for _, inst := range e.instances {
		if inst.config.BatchID == batchID {
			result = append(result, inst)
		}
	}
	return result
}

// ApplyRLAction dispatches an RL action to a specific running instance.
func (e *Engine) ApplyRLAction(instanceID string, action int, value float64) bool {
	e.mu.RLock()
	inst, ok := e.instances[instanceID]
	e.mu.RUnlock()
	if !ok {
		return false
	}
	return inst.ApplyRLAction(action, value)
}

// GetAllTelemetry returns current telemetry for all running instances.
func (e *Engine) GetAllTelemetry() []TelemetrySnapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()

	result := make([]TelemetrySnapshot, 0, len(e.instances))
	for _, inst := range e.instances {
		if inst.State() == StateRunning {
			result = append(result, inst.Telemetry())
		}
	}
	return result
}

// collisionAvoidanceLoop runs a high-frequency check to prevent drone-drone collisions.
// If two drones are within safeMinDist, it adjusts their headings and altitudes to diverge.
func (e *Engine) collisionAvoidanceLoop() {
	const safeMinDist = 15.0 // meters — trigger avoidance
	const warnDist = 30.0    // meters — start gentle adjustment
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
			e.mu.RLock()
			running := make([]*Instance, 0)
			for _, inst := range e.instances {
				if inst.State() == StateRunning {
					running = append(running, inst)
				}
			}
			e.mu.RUnlock()

			n := len(running)
			if n < 2 {
				continue
			}

			// Gather positions snapshot
			type pos struct {
				lat, lng, alt, hdg float64
			}
			positions := make([]pos, n)
			for i, inst := range running {
				inst.mu.RLock()
				positions[i] = pos{inst.gps.Latitude, inst.gps.Longitude, inst.gps.Altitude, inst.gps.Heading}
				inst.mu.RUnlock()
			}

			// Pairwise distance check
			for i := 0; i < n; i++ {
				for j := i + 1; j < n; j++ {
					dLat := (positions[j].lat - positions[i].lat) * 111000
					dLng := (positions[j].lng - positions[i].lng) * 111000 * math.Cos(positions[i].lat*math.Pi/180)
					dAlt := positions[j].alt - positions[i].alt
					horizDist := math.Sqrt(dLat*dLat + dLng*dLng)
					dist3d := math.Sqrt(horizDist*horizDist + dAlt*dAlt)

					if dist3d < warnDist {
						// Calculate avoidance: push them apart
						strength := 1.0
						if dist3d < safeMinDist {
							strength = 2.0 // urgent
						}

						// Adjust altitude: one goes up, one goes down
						altDelta := 1.5 * strength
						running[i].mu.Lock()
						running[i].gps.Altitude -= altDelta
						if running[i].gps.Altitude < 10 {
							running[i].gps.Altitude = 10
						}
						// Adjust heading: turn away from the other drone
						running[i].gps.Heading = math.Mod(running[i].gps.Heading+15*strength+360, 360)
						running[i].mu.Unlock()

						running[j].mu.Lock()
						running[j].gps.Altitude += altDelta
						if running[j].gps.Altitude > running[j].config.MaxAlt {
							running[j].gps.Altitude = running[j].config.MaxAlt
						}
						running[j].gps.Heading = math.Mod(running[j].gps.Heading-15*strength+360, 360)
						running[j].mu.Unlock()
					}
				}
			}
		}
	}
}

// healthMonitorLoop periodically checks for failed instances and auto-restarts them.
func (e *Engine) healthMonitorLoop() {
	const maxAutoRestarts = 5
	restartCount := make(map[string]int) // track restart attempts per instance

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
			e.mu.RLock()
			var failedIDs []string
			for id, inst := range e.instances {
				if inst.State() == StateFailed {
					failedIDs = append(failedIDs, id)
				}
			}
			e.mu.RUnlock()

			for _, id := range failedIDs {
				cnt := restartCount[id]
				if cnt >= maxAutoRestarts {
					continue // stop retrying after too many restarts
				}
				e.mu.RLock()
				inst, ok := e.instances[id]
				e.mu.RUnlock()
				if !ok {
					continue
				}
				// Reset state and restart
				inst.mu.Lock()
				inst.state = StateCreated
				inst.errorMsg = ""
				inst.mu.Unlock()
				inst.Start(e.ctx)
				restartCount[id] = cnt + 1
				log.Printf("[SimEngine] auto-restarted failed instance %s (attempt %d/%d)", id, cnt+1, maxAutoRestarts)
			}
		}
	}
}

// GenerateCircularWaypoints creates a set of waypoints in a circle around a center point.
func GenerateCircularWaypoints(centerLat, centerLng, radiusM, alt float64, numPoints int) []Waypoint {
	if numPoints <= 0 {
		numPoints = 8
	}
	if radiusM <= 0 {
		radiusM = 500
	}
	if alt <= 0 {
		alt = 50
	}
	radiusDeg := radiusM / 111000.0
	wps := make([]Waypoint, numPoints)
	for i := 0; i < numPoints; i++ {
		angle := 2 * math.Pi * float64(i) / float64(numPoints)
		wps[i] = Waypoint{
			Lat:   centerLat + radiusDeg*math.Cos(angle),
			Lng:   centerLng + radiusDeg*math.Sin(angle)/math.Cos(centerLat*math.Pi/180),
			Alt:   alt,
			Speed: 12 + rand.Float64()*6,
		}
	}
	return wps
}

// GenerateMissionWaypoints creates realistic waypoints based on mission type.
// Each mission type has a distinct route pattern, waypoint actions, and task descriptions.
func GenerateMissionWaypoints(mission MissionType, centerLat, centerLng, radiusM, maxAlt float64) ([]Waypoint, string) {
	if radiusM <= 0 {
		radiusM = 500
	}
	if maxAlt <= 0 {
		maxAlt = 80
	}
	rDeg := radiusM / 111000.0
	cosLat := math.Cos(centerLat * math.Pi / 180)

	switch mission {
	case MissionPatrol:
		// 巡逻: 矩形巡逻区域, 4个角+中间点, 低速持续飞行
		alt := maxAlt * 0.5
		wps := []Waypoint{
			{Lat: centerLat + rDeg, Lng: centerLng - rDeg/cosLat, Alt: alt, Speed: 10, Action: "fly", Name: "巡逻起点-西北角"},
			{Lat: centerLat + rDeg, Lng: centerLng + rDeg/cosLat, Alt: alt, Speed: 10, Action: "hover", HoldSec: 10, Name: "东北角-悬停观察"},
			{Lat: centerLat, Lng: centerLng + rDeg*1.2/cosLat, Alt: alt + 10, Speed: 8, Action: "photo", HoldSec: 5, Name: "东侧中点-拍照"},
			{Lat: centerLat - rDeg, Lng: centerLng + rDeg/cosLat, Alt: alt, Speed: 10, Action: "fly", Name: "东南角"},
			{Lat: centerLat - rDeg, Lng: centerLng - rDeg/cosLat, Alt: alt, Speed: 10, Action: "hover", HoldSec: 10, Name: "西南角-悬停观察"},
			{Lat: centerLat, Lng: centerLng - rDeg*1.2/cosLat, Alt: alt + 10, Speed: 8, Action: "photo", HoldSec: 5, Name: "西侧中点-拍照"},
			{Lat: centerLat, Lng: centerLng, Alt: alt + 15, Speed: 6, Action: "hover", HoldSec: 15, Name: "中心点-全景扫描"},
		}
		return wps, "区域安全巡逻：矩形覆盖区域，4角+中心共7个航点，含悬停观察和拍照"

	case MissionInspection:
		// 电力/管道巡检: 沿直线排列多个检查点, 每个点悬停拍照
		alt := maxAlt * 0.4
		n := 8
		wps := make([]Waypoint, n)
		for i := 0; i < n; i++ {
			frac := float64(i) / float64(n-1)
			lat := centerLat - rDeg + 2*rDeg*frac
			lng := centerLng - rDeg*0.3/cosLat + rDeg*0.6/cosLat*frac*(0.8+rand.Float64()*0.4)
			action := "inspect"
			hold := 8
			name := fmt.Sprintf("检查点%d-设备巡检", i+1)
			if i == 0 {
				name = "起点-变电站A出发"
			} else if i == n-1 {
				name = "终点-变电站B到达"
				action = "photo"
				hold = 12
			}
			wps[i] = Waypoint{Lat: lat, Lng: lng, Alt: alt + float64(i)*2, Speed: 6, Action: action, HoldSec: hold, Name: name}
		}
		return wps, "电力线路巡检：从变电站A到变电站B，沿线路8个检查点逐一悬停拍照检测"

	case MissionDelivery:
		// 物流配送: 起点->配送点->返回, 直线往返, 中途有投递点
		alt := maxAlt * 0.6
		destLat := centerLat + rDeg*1.5
		destLng := centerLng + rDeg*1.0/cosLat
		midLat := (centerLat + destLat) / 2
		midLng := (centerLng + destLng) / 2
		wps := []Waypoint{
			{Lat: centerLat + rDeg*0.1, Lng: centerLng + rDeg*0.1/cosLat, Alt: alt, Speed: 15, Action: "fly", Name: "仓库出发-爬升"},
			{Lat: midLat, Lng: midLng, Alt: alt + 10, Speed: 18, Action: "fly", Name: "航路中间点-巡航"},
			{Lat: destLat - rDeg*0.1, Lng: destLng - rDeg*0.1/cosLat, Alt: alt, Speed: 12, Action: "fly", Name: "目的地上空-准备投递"},
			{Lat: destLat, Lng: destLng, Alt: 15, Speed: 3, Action: "drop", HoldSec: 20, Name: "投递点-下降投放货物"},
			{Lat: destLat, Lng: destLng, Alt: alt, Speed: 10, Action: "fly", Name: "投递完成-爬升返航"},
			{Lat: midLat, Lng: midLng, Alt: alt + 10, Speed: 18, Action: "fly", Name: "返航中间点"},
		}
		return wps, "物流配送任务：从仓库出发至目标配送点，完成货物投放后返航"

	case MissionSurvey:
		// 区域测绘: 之字形(弓字形)扫描覆盖, 模拟正射影像采集
		alt := maxAlt * 0.7
		rows := 5
		cols := 3
		wps := make([]Waypoint, 0, rows*cols)
		for r := 0; r < rows; r++ {
			for c := 0; c < cols; c++ {
				cc := c
				if r%2 == 1 {
					cc = cols - 1 - c // 反向
				}
				lat := centerLat - rDeg + 2*rDeg*float64(r)/float64(rows-1)
				lng := centerLng - rDeg/cosLat + 2*rDeg/cosLat*float64(cc)/float64(cols-1)
				wps = append(wps, Waypoint{
					Lat:     lat,
					Lng:     lng,
					Alt:     alt,
					Speed:   8,
					Action:  "photo",
					HoldSec: 3,
					Name:    fmt.Sprintf("测绘网格[%d,%d]-正射拍摄", r+1, cc+1),
				})
			}
		}
		return wps, fmt.Sprintf("区域测绘任务：%dx%d弓字形航线覆盖扫描，每点正射影像采集", rows, cols)

	case MissionSAR:
		// 搜救: 螺旋扩展搜索, 从中心逐渐扩大
		alt := maxAlt * 0.45
		n := 12
		wps := make([]Waypoint, n)
		for i := 0; i < n; i++ {
			angle := 2 * math.Pi * float64(i) / 4 // ~3圈
			r := rDeg * 0.2 * float64(i+1) / float64(n)
			wps[i] = Waypoint{
				Lat:     centerLat + r*math.Cos(angle),
				Lng:     centerLng + r*math.Sin(angle)/cosLat,
				Alt:     alt + float64(i)*1.5,
				Speed:   7,
				Action:  "photo",
				HoldSec: 5,
				Name:    fmt.Sprintf("搜索点%d-螺旋扫描", i+1),
			}
		}
		return wps, "搜索救援任务：从失联点为中心向外螺旋搜索，每点悬停拍照扫描"

	default:
		// Fallback to circular patrol
		wps := GenerateCircularWaypoints(centerLat, centerLng, radiusM, maxAlt*0.5, 8)
		for i := range wps {
			wps[i].Action = "fly"
			wps[i].Name = fmt.Sprintf("航点%d", i+1)
		}
		return wps, "通用巡航任务：环形航线"
	}
}
