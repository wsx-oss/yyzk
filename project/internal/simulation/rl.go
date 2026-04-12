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
	"sync"
	"time"
)

// ---------- State / Action / Reward Definitions ----------

// RLState represents the observation space for a single drone or cluster.
type RLState struct {
	// Per-drone state
	Lat         float64 `json:"lat"`
	Lng         float64 `json:"lng"`
	Alt         float64 `json:"alt"`
	Speed       float64 `json:"speed"`
	Heading     float64 `json:"heading"`
	BatteryPct  float64 `json:"battery_pct"`
	BatteryTemp float64 `json:"battery_temp"`
	Phase       int     `json:"phase"`       // encoded flight phase
	TaskProg    float64 `json:"task_prog"`   // task progress 0-1
	HasAnomaly  int     `json:"has_anomaly"` // 0 or 1
	AnomalyType int     `json:"anomaly_type"`

	// Cluster context
	NearbyCount    int     `json:"nearby_count"`   // drones within 200m
	MinSeparation  float64 `json:"min_separation"` // meters to nearest drone
	ClusterCenterX float64 `json:"cluster_center_x"`
	ClusterCenterY float64 `json:"cluster_center_y"`

	// Environment
	WindSpeed   float64 `json:"wind_speed"`
	WindDir     float64 `json:"wind_dir"`
	TimeOfDay   float64 `json:"time_of_day"`    // 0-24
	InNoFlyZone int     `json:"in_no_fly_zone"` // 0 or 1
}

// RLAction represents the action space for drone control.
type RLAction struct {
	Type     int     `json:"type"`      // 0=adjust_heading, 1=adjust_speed, 2=adjust_alt, 3=goto_wp, 4=return, 5=hover, 6=emergency_land
	Value    float64 `json:"value"`     // action-specific parameter
	TargetWP int     `json:"target_wp"` // for goto_wp action
	Priority int     `json:"priority"`  // for cluster scheduling
}

const (
	ActionAdjustHeading = 0
	ActionAdjustSpeed   = 1
	ActionAdjustAlt     = 2
	ActionGotoWP        = 3
	ActionReturn        = 4
	ActionHover         = 5
	ActionEmergencyLand = 6
)

// RLReward computes multi-objective reward.
type RLReward struct {
	RouteEfficiency  float64 `json:"route_efficiency"`  // waypoint progress
	SafetyScore      float64 `json:"safety_score"`      // collision avoidance + geofence
	EnergyEfficiency float64 `json:"energy_efficiency"` // battery optimization
	TaskCompletion   float64 `json:"task_completion"`   // mission progress
	AnomalyHandling  float64 `json:"anomaly_handling"`  // proper response to faults
	ClusterCohesion  float64 `json:"cluster_cohesion"`  // formation/spacing
	Total            float64 `json:"total"`
}

// ---------- Experience Replay Buffer ----------

// Experience stores a single (s, a, r, s') transition.
type Experience struct {
	State     RLState   `json:"state"`
	Action    RLAction  `json:"action"`
	Reward    RLReward  `json:"reward"`
	NextState RLState   `json:"next_state"`
	Done      bool      `json:"done"`
	Timestamp time.Time `json:"timestamp"`
}

// ReplayBuffer stores experiences for training.
type ReplayBuffer struct {
	mu       sync.Mutex
	buffer   []Experience
	maxSize  int
	position int
}

func NewReplayBuffer(maxSize int) *ReplayBuffer {
	return &ReplayBuffer{
		buffer:  make([]Experience, 0, maxSize),
		maxSize: maxSize,
	}
}

func (rb *ReplayBuffer) Add(exp Experience) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	if len(rb.buffer) < rb.maxSize {
		rb.buffer = append(rb.buffer, exp)
	} else {
		rb.buffer[rb.position] = exp
	}
	rb.position = (rb.position + 1) % rb.maxSize
}

func (rb *ReplayBuffer) Sample(batchSize int) []Experience {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	n := len(rb.buffer)
	if n == 0 {
		return nil
	}
	if batchSize > n {
		batchSize = n
	}
	indices := rand.Perm(n)[:batchSize]
	result := make([]Experience, batchSize)
	for i, idx := range indices {
		result[i] = rb.buffer[idx]
	}
	return result
}

func (rb *ReplayBuffer) Len() int {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return len(rb.buffer)
}

// ---------- Q-Table Policy (tabular RL for interpretability) ----------

// QTable implements a simple tabular Q-learning policy.
type QTable struct {
	mu      sync.RWMutex
	table   map[string][]float64 // stateKey -> action values
	nAction int
	lr      float64 // learning rate
	gamma   float64 // discount factor
	epsilon float64 // exploration rate
}

func NewQTable(nActions int, lr, gamma, epsilon float64) *QTable {
	return &QTable{
		table:   make(map[string][]float64),
		nAction: nActions,
		lr:      lr,
		gamma:   gamma,
		epsilon: epsilon,
	}
}

func (q *QTable) stateKey(s RLState) string {
	// Discretize ALL meaningful state features into a string key.
	// Grid: lat/lng to ~100m cells, alt 10m bins, speed 5m/s bins, heading 45° bins,
	// battery 10% bins, nearby count, separation bucket, phase, anomaly.
	latBin := int(math.Round(s.Lat * 1000)) // ~111m per bin
	lngBin := int(math.Round(s.Lng * 1000)) // ~111m per bin
	altBin := int(s.Alt / 10)               // 10m bins
	spdBin := int(s.Speed / 5)              // 5m/s bins: 0,1,2,3,4,5
	hdgBin := int(s.Heading / 45)           // 8 compass sectors
	batBin := int(s.BatteryPct / 10)        // 10% bins: 0-10
	phaseBin := s.Phase                     // 0-6
	anomBin := s.HasAnomaly                 // 0 or 1
	nearBin := s.NearbyCount                // exact count (usually small)
	if nearBin > 5 {
		nearBin = 5 // cap
	}
	sepBin := 3 // far
	if s.MinSeparation < 10 {
		sepBin = 0 // collision danger
	} else if s.MinSeparation < 30 {
		sepBin = 1 // close
	} else if s.MinSeparation < 100 {
		sepBin = 2 // medium
	}
	nfzBin := s.InNoFlyZone // 0 or 1
	return fmt.Sprintf("%d_%d_%d_%d_%d_%d_%d_%d_%d_%d_%d",
		latBin, lngBin, altBin, spdBin, hdgBin, batBin, phaseBin, anomBin, nearBin, sepBin, nfzBin)
}

// getValuesCopy returns a COPY of the Q-values for a state key (thread-safe).
func (q *QTable) getValuesCopy(key string) []float64 {
	q.mu.RLock()
	v, ok := q.table[key]
	if ok {
		cp := make([]float64, len(v))
		copy(cp, v)
		q.mu.RUnlock()
		return cp
	}
	q.mu.RUnlock()
	return make([]float64, q.nAction)
}

// ensureKey creates a zero-value entry if the key doesn't exist (must hold write lock).
func (q *QTable) ensureKey(key string) []float64 {
	v, ok := q.table[key]
	if !ok {
		v = make([]float64, q.nAction)
		q.table[key] = v
	}
	return v
}

// SelectAction uses epsilon-greedy policy. Fully thread-safe (reads only a copy).
func (q *QTable) SelectAction(s RLState) int {
	q.mu.RLock()
	eps := q.epsilon
	q.mu.RUnlock()

	if rand.Float64() < eps {
		return rand.Intn(q.nAction)
	}
	key := q.stateKey(s)
	values := q.getValuesCopy(key)
	best := 0
	for i := 1; i < len(values); i++ {
		if values[i] > values[best] {
			best = i
		}
	}
	return best
}

// Update performs Q-learning update. Fully thread-safe (holds write lock).
func (q *QTable) Update(s RLState, action int, reward float64, nextS RLState, done bool) {
	key := q.stateKey(s)
	nextKey := q.stateKey(nextS)

	q.mu.Lock()
	defer q.mu.Unlock()

	values := q.ensureKey(key)
	nextValues := q.ensureKey(nextKey)

	maxNext := nextValues[0]
	for _, v := range nextValues[1:] {
		if v > maxNext {
			maxNext = v
		}
	}

	target := reward
	if !done {
		target += q.gamma * maxNext
	}
	values[action] += q.lr * (target - values[action])
}

// DecayEpsilon reduces exploration rate.
func (q *QTable) DecayEpsilon(factor float64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.epsilon *= factor
	if q.epsilon < 0.01 {
		q.epsilon = 0.01
	}
}

// ---------- RL Trainer ----------

// droneStep stores the previous-tick state/action for a single drone,
// so the next tick can provide a real (s, a, r, s') transition.
type droneStep struct {
	State  RLState
	Action int
}

// RLTrainer orchestrates reinforcement learning training alongside simulation.
type RLTrainer struct {
	mu           sync.RWMutex
	engine       *Engine
	policy       *QTable
	replayBuffer *ReplayBuffer
	training     bool
	ctx          context.Context
	cancel       context.CancelFunc
	dataDir      string
	cfg          RLTrainerConfig

	// Per-drone previous step tracking for proper (s, a, r, s') transitions
	prevSteps map[string]*droneStep // deviceID -> previous step

	// Training metrics
	episodes      int
	totalReward   float64
	sampleCount   int64   // total samples collected (for accurate avg)
	episodeReward float64 // reward accumulated in current episode
	avgReward     float64
	bestEpReward  float64
	evalResults   []EvalResult

	// Callback for persisting eval results to DB
	OnEval func(result EvalResult, epsilon float64)

	// Optional DB-backed persistence callbacks (replace file I/O when set)
	SavePolicyFunc func(data []byte) error
	LoadPolicyFunc func() ([]byte, error)
}

// EvalResult stores evaluation metrics for a training checkpoint.
type EvalResult struct {
	Episode         int       `json:"episode"`
	AvgReward       float64   `json:"avg_reward"`
	RouteEfficiency float64   `json:"route_efficiency"`
	SafetyScore     float64   `json:"safety_score"`
	EnergyScore     float64   `json:"energy_score"`
	TaskCompletion  float64   `json:"task_completion"`
	AnomalyScore    float64   `json:"anomaly_score"`
	Timestamp       time.Time `json:"timestamp"`
}

// RLTrainerConfig holds training configuration.
type RLTrainerConfig struct {
	LearningRate   float64 `json:"learning_rate"`
	DiscountFactor float64 `json:"discount_factor"`
	Epsilon        float64 `json:"epsilon"`
	BufferSize     int     `json:"buffer_size"`
	BatchSize      int     `json:"batch_size"`
	EvalInterval   int     `json:"eval_interval"` // episodes between evaluations
	DataDir        string  `json:"data_dir"`
}

// NewRLTrainer creates a new trainer bound to a simulation engine.
func NewRLTrainer(engine *Engine, cfg RLTrainerConfig) *RLTrainer {
	if cfg.LearningRate <= 0 {
		cfg.LearningRate = 0.01
	}
	if cfg.DiscountFactor <= 0 {
		cfg.DiscountFactor = 0.99
	}
	if cfg.Epsilon <= 0 {
		cfg.Epsilon = 0.3
	}
	if cfg.BufferSize <= 0 {
		cfg.BufferSize = 100000
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 64
	}
	if cfg.EvalInterval <= 0 {
		cfg.EvalInterval = 100
	}
	if cfg.DataDir == "" {
		cfg.DataDir = "data"
	}

	return &RLTrainer{
		engine:       engine,
		policy:       NewQTable(7, cfg.LearningRate, cfg.DiscountFactor, cfg.Epsilon),
		replayBuffer: NewReplayBuffer(cfg.BufferSize),
		dataDir:      cfg.DataDir,
		cfg:          cfg,
		prevSteps:    make(map[string]*droneStep),
	}
}

// StartTraining begins the training loop that runs alongside 7x24 simulation.
func (t *RLTrainer) StartTraining(evalInterval int) {
	t.mu.Lock()
	if t.training {
		t.mu.Unlock()
		return
	}
	t.training = true
	ctx, cancel := context.WithCancel(context.Background())
	t.ctx = ctx
	t.cancel = cancel
	t.mu.Unlock()

	if evalInterval <= 0 {
		evalInterval = 100
	}

	go t.trainingLoop(ctx, evalInterval)
	log.Println("[RLTrainer] training started")
}

// StopTraining stops the training loop.
func (t *RLTrainer) StopTraining() {
	t.mu.Lock()
	if !t.training {
		t.mu.Unlock()
		return
	}
	t.training = false
	if t.cancel != nil {
		t.cancel()
	}
	t.mu.Unlock()
	t.SavePolicy()
	log.Println("[RLTrainer] training stopped")
}

// IsTraining returns whether the trainer is active.
func (t *RLTrainer) IsTraining() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.training
}

// TrainingMetrics returns current training statistics.
func (t *RLTrainer) TrainingMetrics() map[string]interface{} {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return map[string]interface{}{
		"training":     t.training,
		"episodes":     t.episodes,
		"total_reward": t.totalReward,
		"avg_reward":   t.avgReward,
		"best_reward":  t.bestEpReward,
		"sample_count": t.sampleCount,
		"buffer_size":  t.replayBuffer.Len(),
		"epsilon":      t.policy.epsilon,
		"eval_results": t.evalResults,
	}
}

func (t *RLTrainer) trainingLoop(ctx context.Context, evalInterval int) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			nSamples := t.collectExperience()
			t.trainStep()

			t.mu.Lock()
			t.episodes++
			ep := t.episodes
			// Track per-episode reward: episodeReward was accumulated in collectExperience
			if t.episodeReward > t.bestEpReward {
				t.bestEpReward = t.episodeReward
			}
			// avgReward = total reward / total samples (not episodes)
			if t.sampleCount > 0 {
				t.avgReward = t.totalReward / float64(t.sampleCount)
			}
			_ = nSamples
			t.episodeReward = 0 // reset for next episode
			t.mu.Unlock()

			if ep%evalInterval == 0 {
				t.evaluate()
				t.policy.DecayEpsilon(0.995)
			}
			if ep%1000 == 0 {
				t.SavePolicy()
			}
		}
	}
}

// collectExperience implements a proper RL loop:
//  1. For each running drone, observe current state s'.
//  2. If we have a previous step (s, a) for this drone, compute reward r(s, a, s') and store (s, a, r, s').
//  3. Select a new action from policy based on s', APPLY it to the engine, and record as new prev step.
//
// This closes the RL loop: actions modify the environment, and NextState is real.
func (t *RLTrainer) collectExperience() int {
	telemetry := t.engine.GetAllTelemetry()
	if len(telemetry) == 0 {
		return 0
	}

	samplesAdded := 0

	for _, snap := range telemetry {
		deviceID := snap.DeviceID
		currentState := t.buildState(snap, telemetry)
		done := snap.TaskStatus == TaskCompleted || snap.TaskStatus == TaskFailed

		// ---- Step A: complete the previous transition (s, a) -> r, s' ----
		t.mu.RLock()
		prev, hasPrev := t.prevSteps[deviceID]
		t.mu.RUnlock()

		if hasPrev {
			// Compute reward based on PREVIOUS action + CURRENT outcome
			reward := t.computeReward(snap, prev.Action, prev.State, currentState, telemetry)

			t.replayBuffer.Add(Experience{
				State:     prev.State,
				Action:    RLAction{Type: prev.Action},
				Reward:    reward,
				NextState: currentState, // real next state from environment
				Done:      done,
				Timestamp: time.Now(),
			})

			t.mu.Lock()
			t.totalReward += reward.Total
			t.episodeReward += reward.Total
			t.sampleCount++
			t.mu.Unlock()
			samplesAdded++
		}

		// ---- Step B: select NEW action and APPLY it to the engine ----
		if !done {
			newAction := t.policy.SelectAction(currentState)
			actionValue := t.actionValue(newAction, currentState)

			// EXECUTE the action on the real simulation instance
			t.engine.ApplyRLAction(deviceID, newAction, actionValue)

			t.mu.Lock()
			t.prevSteps[deviceID] = &droneStep{
				State:  currentState,
				Action: newAction,
			}
			t.mu.Unlock()
		} else {
			// Episode ended for this drone — clear prev step
			t.mu.Lock()
			delete(t.prevSteps, deviceID)
			t.mu.Unlock()
		}
	}
	return samplesAdded
}

// actionValue generates a continuous parameter for the discrete action type.
func (t *RLTrainer) actionValue(action int, s RLState) float64 {
	switch action {
	case ActionAdjustHeading:
		// Turn toward cluster center or away from nearest drone
		if s.MinSeparation < 30 {
			return 30 + rand.Float64()*15 // turn away
		}
		return (rand.Float64() - 0.5) * 40 // small random adjustment
	case ActionAdjustSpeed:
		if s.BatteryPct < 30 {
			return 0.7 // slow down to save battery
		}
		return 0.9 + rand.Float64()*0.4 // 0.9-1.3 multiplier
	case ActionAdjustAlt:
		if s.Alt < 20 {
			return 10 // go up
		}
		return (rand.Float64() - 0.5) * 20 // ±10m
	case ActionGotoWP:
		return 0 // next waypoint
	case ActionReturn:
		return 0
	case ActionHover:
		return 0
	case ActionEmergencyLand:
		return 0
	}
	return 0
}

func (t *RLTrainer) trainStep() {
	batch := t.replayBuffer.Sample(t.cfg.BatchSize)
	if len(batch) == 0 {
		return
	}
	for _, exp := range batch {
		t.policy.Update(exp.State, exp.Action.Type, exp.Reward.Total, exp.NextState, exp.Done)
	}
}

func (t *RLTrainer) evaluate() {
	telemetry := t.engine.GetAllTelemetry()
	if len(telemetry) == 0 {
		return
	}

	var totalRoute, totalSafety, totalEnergy, totalTask, totalAnomaly float64
	for _, snap := range telemetry {
		state := t.buildState(snap, telemetry)
		greedyAction := t.policy.SelectAction(state) // evaluation = greedy
		r := t.computeReward(snap, greedyAction, state, state, telemetry)
		totalRoute += r.RouteEfficiency
		totalSafety += r.SafetyScore
		totalEnergy += r.EnergyEfficiency
		totalTask += r.TaskCompletion
		totalAnomaly += r.AnomalyHandling
	}
	n := float64(len(telemetry))

	t.mu.Lock()
	result := EvalResult{
		Episode:         t.episodes,
		AvgReward:       t.avgReward,
		RouteEfficiency: totalRoute / n,
		SafetyScore:     totalSafety / n,
		EnergyScore:     totalEnergy / n,
		TaskCompletion:  totalTask / n,
		AnomalyScore:    totalAnomaly / n,
		Timestamp:       time.Now(),
	}
	t.evalResults = append(t.evalResults, result)
	if len(t.evalResults) > 100 {
		t.evalResults = t.evalResults[len(t.evalResults)-100:]
	}
	t.mu.Unlock()

	log.Printf("[RLTrainer] eval ep=%d avg_reward=%.3f route=%.3f safety=%.3f energy=%.3f task=%.3f anomaly=%.3f",
		result.Episode, result.AvgReward, result.RouteEfficiency, result.SafetyScore,
		result.EnergyScore, result.TaskCompletion, result.AnomalyScore)

	if t.OnEval != nil {
		t.OnEval(result, t.policy.epsilon)
	}
}

func (t *RLTrainer) buildState(snap TelemetrySnapshot, all []TelemetrySnapshot) RLState {
	s := RLState{
		Lat:         snap.GPS.Latitude,
		Lng:         snap.GPS.Longitude,
		Alt:         snap.GPS.Altitude,
		Speed:       snap.GPS.Speed,
		Heading:     snap.GPS.Heading,
		BatteryPct:  float64(snap.Battery.Level),
		BatteryTemp: snap.Battery.Temperature,
		Phase:       encodePhase(snap.FlightPhase),
		TimeOfDay:   float64(time.Now().Hour()) + float64(time.Now().Minute())/60.0,
	}

	// Use real route progress from waypoint tracking (0.0 → 1.0)
	s.TaskProg = snap.RouteProgress
	if snap.TaskStatus == TaskFailed {
		s.TaskProg = -1.0
	}

	if len(snap.Anomalies) > 0 {
		for _, a := range snap.Anomalies {
			if a.Active {
				s.HasAnomaly = 1
				s.AnomalyType = encodeAnomalyType(a.Type)
				break
			}
		}
	}

	minSep := math.MaxFloat64
	nearby := 0
	var sumLat, sumLng float64
	for _, other := range all {
		if other.DeviceID == snap.DeviceID {
			continue
		}
		dist := haversineDist(snap.GPS.Latitude, snap.GPS.Longitude, other.GPS.Latitude, other.GPS.Longitude)
		if dist < 200 {
			nearby++
		}
		if dist < minSep {
			minSep = dist
		}
		sumLat += other.GPS.Latitude
		sumLng += other.GPS.Longitude
	}
	s.NearbyCount = nearby
	if minSep < math.MaxFloat64 {
		s.MinSeparation = minSep
	}
	if len(all) > 1 {
		s.ClusterCenterX = sumLat / float64(len(all)-1)
		s.ClusterCenterY = sumLng / float64(len(all)-1)
	}

	// Simulated wind (slight random, could be replaced with real weather API)
	s.WindSpeed = 2 + rand.Float64()*5
	s.WindDir = rand.Float64() * 360

	// Geofence awareness
	if snap.GeofenceViolation != "" {
		s.InNoFlyZone = 1
	}

	return s
}

// computeReward computes a multi-objective reward that is **action-dependent**.
// Arguments: current snapshot, the action that was taken, the state before, the state after, and all drone snapshots.
func (t *RLTrainer) computeReward(snap TelemetrySnapshot, action int, prevState RLState, curState RLState, all []TelemetrySnapshot) RLReward {
	r := RLReward{}
	batLevel := float64(snap.Battery.Level)
	hasActiveAnomaly := false
	var activeAnomalyType AnomalyType
	for _, a := range snap.Anomalies {
		if a.Active {
			hasActiveAnomaly = true
			activeAnomalyType = a.Type
			break
		}
	}

	// ---- 1. Route efficiency: reward actual waypoint progress advancement ----
	progressDelta := curState.TaskProg - prevState.TaskProg
	if progressDelta > 0 {
		r.RouteEfficiency = progressDelta * 5.0 // strong reward for advancing
	}
	// Bonus for cruise/work phases at reasonable speed
	if snap.FlightPhase == PhaseCruise || snap.FlightPhase == PhaseWork {
		r.RouteEfficiency += math.Min(snap.GPS.Speed/15.0, 1.0) * 0.15
	}
	if snap.TaskStatus == TaskCompleted {
		r.RouteEfficiency += 2.0 // big bonus for mission complete
	}
	// Penalize hover/idle when there's no reason (no anomaly, battery ok)
	if action == ActionHover && !hasActiveAnomaly && batLevel > 30 {
		r.RouteEfficiency -= 0.4
	}
	// Penalize standing still when task is running and should be making progress
	if progressDelta <= 0 && snap.TaskStatus == TaskRunning && !hasActiveAnomaly && batLevel > 30 {
		r.RouteEfficiency -= 0.1
	}

	// ---- 2. Safety: separation + geofence + action appropriateness ----
	minSep := 1000.0
	for _, other := range all {
		if other.DeviceID == snap.DeviceID {
			continue
		}
		dist := haversineDist(snap.GPS.Latitude, snap.GPS.Longitude, other.GPS.Latitude, other.GPS.Longitude)
		if dist < minSep {
			minSep = dist
		}
	}
	if minSep < 10 {
		r.SafetyScore = -2.0
		if action == ActionAdjustHeading || action == ActionAdjustAlt || action == ActionHover {
			r.SafetyScore += 1.0
		}
		if action == ActionAdjustSpeed {
			r.SafetyScore -= 0.5
		}
	} else if minSep < 30 {
		r.SafetyScore = -0.5
		if action == ActionAdjustHeading || action == ActionAdjustAlt {
			r.SafetyScore += 0.5
		}
	} else {
		r.SafetyScore = 0.5
	}
	// Geofence violation: heavy penalty for being inside a no-fly zone
	if snap.GeofenceViolation != "" {
		r.SafetyScore -= 3.0
		if action == ActionReturn || action == ActionAdjustHeading {
			r.SafetyScore += 1.5 // reward corrective action
		}
	}

	// ---- 3. Energy: action-aware battery management ----
	if batLevel > 50 {
		r.EnergyEfficiency = 0.3
	} else if batLevel > 20 {
		r.EnergyEfficiency = 0.1
		// Reward return action when battery is getting low
		if action == ActionReturn {
			r.EnergyEfficiency += 0.5
		}
		// Penalize speed increase on low battery
		if action == ActionAdjustSpeed && curState.Speed > prevState.Speed {
			r.EnergyEfficiency -= 0.3
		}
	} else {
		r.EnergyEfficiency = -0.5
		// Strong reward for return/land when critically low
		if action == ActionReturn || action == ActionEmergencyLand {
			r.EnergyEfficiency += 1.5
		}
		// Strong penalty for continuing cruise on critical battery
		if action == ActionAdjustSpeed || action == ActionGotoWP {
			r.EnergyEfficiency -= 1.0
		}
	}

	// ---- 4. Task completion ----
	switch snap.TaskStatus {
	case TaskCompleted:
		r.TaskCompletion = 2.0
	case TaskRunning:
		r.TaskCompletion = 0.1
		// Reward goto-waypoint when task is running (making progress)
		if action == ActionGotoWP {
			r.TaskCompletion += 0.3
		}
	case TaskFailed:
		r.TaskCompletion = -2.0
	}

	// ---- 5. Anomaly handling: heavily action-dependent ----
	if hasActiveAnomaly {
		switch activeAnomalyType {
		case AnomalyLowBattery:
			if action == ActionReturn || action == ActionEmergencyLand {
				r.AnomalyHandling = 1.5 // correct response
			} else if action == ActionHover {
				r.AnomalyHandling = 0.3 // acceptable
			} else {
				r.AnomalyHandling = -1.0 // ignoring low battery
			}
		case AnomalyDeviation:
			if action == ActionAdjustHeading || action == ActionGotoWP {
				r.AnomalyHandling = 1.5 // correcting course
			} else if action == ActionReturn {
				r.AnomalyHandling = 0.5 // safe fallback
			} else {
				r.AnomalyHandling = -0.5
			}
		case AnomalyCommLost:
			if action == ActionReturn || action == ActionHover {
				r.AnomalyHandling = 1.0 // safe behaviour under comm loss
			} else {
				r.AnomalyHandling = -0.5
			}
		case AnomalyTempHigh, AnomalyTempLow:
			if action == ActionReturn || action == ActionEmergencyLand || action == ActionAdjustAlt {
				r.AnomalyHandling = 1.0
			} else {
				r.AnomalyHandling = -0.3
			}
		default:
			if snap.FlightPhase == PhaseReturn || snap.FlightPhase == PhaseLanding {
				r.AnomalyHandling = 0.5
			} else {
				r.AnomalyHandling = -0.3
			}
		}
	} else {
		// No anomaly: penalize unnecessary emergency actions
		if action == ActionEmergencyLand && batLevel > 30 {
			r.AnomalyHandling = -1.0
		} else if action == ActionReturn && batLevel > 40 && snap.TaskStatus == TaskRunning {
			r.AnomalyHandling = -0.5 // returning too early wastes the mission
		} else {
			r.AnomalyHandling = 0.1 // neutral/slight positive for normal ops
		}
	}

	// ---- 6. Cluster cohesion ----
	if len(all) > 1 {
		var sumDist float64
		for _, other := range all {
			if other.DeviceID != snap.DeviceID {
				sumDist += haversineDist(snap.GPS.Latitude, snap.GPS.Longitude, other.GPS.Latitude, other.GPS.Longitude)
			}
		}
		avgDist := sumDist / float64(len(all)-1)
		if avgDist > 50 && avgDist < 500 {
			r.ClusterCohesion = 0.3
		} else if avgDist <= 50 {
			r.ClusterCohesion = -0.3 // too close
		} else {
			r.ClusterCohesion = -0.2 // too far
		}
	}

	// ---- Total weighted reward ----
	r.Total = r.RouteEfficiency*0.20 + r.SafetyScore*0.20 + r.EnergyEfficiency*0.15 +
		r.TaskCompletion*0.15 + r.AnomalyHandling*0.20 + r.ClusterCohesion*0.10

	return r
}

// SavePolicy persists the Q-table to the database (or disk as fallback).
func (t *RLTrainer) SavePolicy() {
	t.policy.mu.RLock()
	defer t.policy.mu.RUnlock()

	data := map[string]interface{}{
		"table":   t.policy.table,
		"epsilon": t.policy.epsilon,
		"lr":      t.policy.lr,
		"gamma":   t.policy.gamma,
	}

	raw, err := json.Marshal(data)
	if err != nil {
		log.Printf("[RLTrainer] save policy error: %v", err)
		return
	}

	// Prefer DB-backed persistence when callback is set
	if t.SavePolicyFunc != nil {
		if err := t.SavePolicyFunc(raw); err != nil {
			log.Printf("[RLTrainer] DB policy save error: %v", err)
		} else {
			log.Printf("[RLTrainer] policy saved to DB (%d states)", len(t.policy.table))
		}
		return
	}

	// Fallback: file-based persistence
	path := filepath.Join(t.dataDir, "rl_policy.json")
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		log.Printf("[RLTrainer] write policy error: %v", err)
	} else {
		log.Printf("[RLTrainer] policy saved (%d states)", len(t.policy.table))
	}
}

// LoadPolicy loads a saved Q-table from the database (or disk as fallback).
func (t *RLTrainer) LoadPolicy() error {
	var raw []byte

	// Prefer DB-backed restore when callback is set
	if t.LoadPolicyFunc != nil {
		var err error
		raw, err = t.LoadPolicyFunc()
		if err != nil || len(raw) == 0 {
			return nil
		}
	} else {
		// Fallback: file-based restore
		path := filepath.Join(t.dataDir, "rl_policy.json")
		var err error
		raw, err = os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
	}

	var data struct {
		Table   map[string][]float64 `json:"table"`
		Epsilon float64              `json:"epsilon"`
		LR      float64              `json:"lr"`
		Gamma   float64              `json:"gamma"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return err
	}

	t.policy.mu.Lock()
	t.policy.table = data.Table
	t.policy.epsilon = data.Epsilon
	if data.LR > 0 {
		t.policy.lr = data.LR
	}
	if data.Gamma > 0 {
		t.policy.gamma = data.Gamma
	}
	t.policy.mu.Unlock()

	log.Printf("[RLTrainer] policy loaded (%d states)", len(data.Table))
	return nil
}

// GetEvalResults returns the evaluation history.
func (t *RLTrainer) GetEvalResults() []EvalResult {
	t.mu.RLock()
	defer t.mu.RUnlock()
	result := make([]EvalResult, len(t.evalResults))
	copy(result, t.evalResults)
	return result
}

// ---------- Helpers ----------

func encodePhase(p FlightPhase) int {
	switch p {
	case PhaseIdle:
		return 0
	case PhaseTakeoff:
		return 1
	case PhaseCruise:
		return 2
	case PhaseWork:
		return 3
	case PhaseReturn:
		return 4
	case PhaseLanding:
		return 5
	case PhaseHover:
		return 6
	default:
		return 0
	}
}

func encodeAnomalyType(a AnomalyType) int {
	switch a {
	case AnomalyLowBattery:
		return 1
	case AnomalyDeviation:
		return 2
	case AnomalyCommLost:
		return 3
	case AnomalyTempHigh:
		return 4
	case AnomalyTempLow:
		return 5
	default:
		return 0
	}
}

func haversineDist(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371000
	dLat := (lat2 - lat1) * math.Pi / 180
	dLon := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return R * c
}
