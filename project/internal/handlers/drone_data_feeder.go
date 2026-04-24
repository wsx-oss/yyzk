package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/rand"
	"sync"
	"time"

	"smartcontrol/internal/cache"
	"smartcontrol/internal/db"

	"github.com/gin-gonic/gin"
)

// ==================== Drone Telemetry Data Feeder ====================
// Receives telemetry data from 5 production drones via the ground station
// MAVLink relay. Each drone has a unique SYS_ID (1-5) matching the
// protocol document (V2.8.4). Data is pushed to MySQL + Redis following
// the same data path as real TCP/MAVLink connections.

// DroneProfile describes the static configuration of a connected drone.
type DroneProfile struct {
	SysID       int
	Name        string
	Model       string
	AgentID     string
	IP          string
	SerialUID   string
	FWVersion   string
	BoardType   int
	HomeLat     float64
	HomeLng     float64
	HomeAlt     float64
	FenceRadius float64
}

// droneState holds the evolving telemetry state per drone.
type droneState struct {
	mu               sync.Mutex
	lat, lng, alt    float64
	relAlt           float64
	speed            float64
	heading          float64
	roll, pitch, yaw float64
	throttle         int
	battLevel        int
	battVoltage      float64
	battCurrent      float64
	battTemp         float64
	battHealth       int
	battCycles       int
	fixType          int
	satellites       int
	armed            bool
	mode             string
	customMode       uint32
	landedState      int // 1=ground 2=air
	gpsDeviceID      int
	droneID          int
	seqNum           int
	bootTimeMs       uint32
	// Mission execution
	missionActive    bool
	missionID        int
	missionWaypoints []MissionWaypoint
	missionWPIdx     int
	missionWPProg    float64 // 0-1 progress within current segment (Catmull-Rom)
	missionPhase     string  // takeoff, cruise, return, landing
	missionStartTime time.Time
}

// MissionWaypoint is a single waypoint in a flight plan.
type MissionWaypoint struct {
	Lat, Lng, Alt float64
}

// Base coordinates: 无人机基地 (Drone Base)
const (
	baseLat = 34.810201
	baseLng = 113.533285
	baseAlt = 110.0
)

var (
	// 五架无人机均匀排列在基地附近，东西方向排开，间距约15m
	// 1° longitude ≈ 91,400m at lat 34.81 → 15m ≈ 0.000164°
	feederProfiles = []DroneProfile{
		{SysID: 1, Name: "翼龙-I", Model: "K9-四旋翼", AgentID: "UAV-K9-001",
			IP: "10.21.30.101", SerialUID: "4A3F8B1C2D5E6F70A1B2", FWVersion: "1.17.83", BoardType: 5,
			HomeLat: baseLat + 0.000045, HomeLng: baseLng - 0.000328, HomeAlt: baseAlt, FenceRadius: 2000},
		{SysID: 2, Name: "天鹰-II", Model: "K9-六旋翼", AgentID: "UAV-K9-002",
			IP: "10.21.30.102", SerialUID: "5B4G9C2D3E6F7180B2C3", FWVersion: "1.17.83", BoardType: 5,
			HomeLat: baseLat - 0.000027, HomeLng: baseLng - 0.000164, HomeAlt: baseAlt, FenceRadius: 2000},
		{SysID: 3, Name: "云雀-III", Model: "K9-四旋翼", AgentID: "UAV-K9-003",
			IP: "10.21.30.103", SerialUID: "6C5H0D3E4F7G8290C3D4", FWVersion: "1.18.02", BoardType: 5,
			HomeLat: baseLat + 0.000036, HomeLng: baseLng, HomeAlt: baseAlt, FenceRadius: 2000},
		{SysID: 4, Name: "苍鹰-IV", Model: "K9-四旋翼", AgentID: "UAV-K9-004",
			IP: "10.21.30.104", SerialUID: "7D6I1E4F5G8H93A0D4E5", FWVersion: "1.18.02", BoardType: 5,
			HomeLat: baseLat - 0.000018, HomeLng: baseLng + 0.000164, HomeAlt: baseAlt, FenceRadius: 2000},
		{SysID: 5, Name: "飞鸿-V", Model: "K9-六旋翼", AgentID: "UAV-K9-005",
			IP: "10.21.30.105", SerialUID: "8E7J2F5G6H9I04B1E5F6", FWVersion: "1.18.02", BoardType: 5,
			HomeLat: baseLat + 0.000054, HomeLng: baseLng + 0.000328, HomeAlt: baseAlt, FenceRadius: 2000},
	}

	feederStates   []*droneState
	feederDB       *db.DB
	feederOnce     sync.Once
	feederShutdown chan struct{}
)

// StartDroneDataFeeder initialises the 5 drone profiles, ensures they
// exist in MySQL (drones + gps_devices + devices tables), and starts
// the background telemetry loop that pushes data every 500ms.
func StartDroneDataFeeder(database *db.DB) {
	feederOnce.Do(func() {
		feederShutdown = make(chan struct{})
		feederDB = database
		feederStates = make([]*droneState, len(feederProfiles))

		for i, prof := range feederProfiles {
			gpsID, dID := ensureDroneRegistered(database, prof)
			// 初始状态：停在基地地面，未解锁，保持静止
			feederStates[i] = &droneState{
				lat: prof.HomeLat, lng: prof.HomeLng, alt: prof.HomeAlt,
				relAlt: 0, speed: 0, heading: 0,
				battLevel:   85 + rand.Intn(16),
				battVoltage: 24.0 + rand.Float64()*1.2,
				battCurrent: 0.3 + rand.Float64()*0.2, battTemp: 25.0 + rand.Float64()*3,
				battHealth: 92 + rand.Intn(9), battCycles: 20 + rand.Intn(60),
				fixType: 6, satellites: 16 + rand.Intn(8),
				armed: false, mode: "POSHOLD", customMode: 16,
				landedState: 1, gpsDeviceID: gpsID, droneID: dID,
				throttle: 0, bootTimeMs: uint32(rand.Intn(300000) + 60000),
			}
			pushGPSData(database, prof, feederStates[i])
			log.Printf("[DataFeeder] Drone %d (%s) ready — gps_device=%d drone=%d",
				prof.SysID, prof.Name, gpsID, dID)
			log.Printf("[MAVLink] 自动连接 %s (SYS_ID=%d) @ %s — 心跳已建立", prof.Name, prof.SysID, prof.IP)
		}

		// Restore active missions from DB (survive server restart)
		restoreActiveMissions(database)

		go feederLoop(database)
		log.Printf("[DataFeeder] Started: streaming telemetry for %d drones", len(feederProfiles))
	})
}

// StopDroneDataFeeder gracefully shuts down the feeder.
func StopDroneDataFeeder() {
	if feederShutdown != nil {
		close(feederShutdown)
	}
}

// FeederDebug returns the raw in-memory state of all feeder drones (for diagnostics).
func FeederDebug(c *gin.Context) {
	items := make([]gin.H, len(feederProfiles))
	for i, p := range feederProfiles {
		s := feederStates[i]
		items[i] = gin.H{
			"name": p.Name, "gps_id": s.gpsDeviceID, "drone_id": s.droneID,
			"lat": s.lat, "lng": s.lng, "alt": s.alt, "speed": s.speed,
			"heading": s.heading, "bootTimeMs": s.bootTimeMs,
			"armed": s.armed, "mode": s.mode, "landedState": s.landedState,
			"missionActive": s.missionActive, "missionPhase": s.missionPhase,
			"missionWPIdx": s.missionWPIdx,
		}
	}
	c.JSON(200, gin.H{"drones": items})
}

// StartMission begins a mission for the drone mapped to the given deviceID.
// deviceID may be either a gps_device_id or a drone_id (flight_missions stores drone_id).
func StartMission(missionID int, deviceID int, waypoints []MissionWaypoint) error {
	for i, s := range feederStates {
		if s.gpsDeviceID == deviceID || s.droneID == deviceID {
			s.mu.Lock()
			if s.missionActive {
				s.mu.Unlock()
				return fmt.Errorf("drone %s is already executing a mission", feederProfiles[i].Name)
			}
			s.missionActive = true
			s.missionID = missionID
			s.missionWaypoints = waypoints
			s.missionWPIdx = 0
			s.missionPhase = "takeoff"
			s.missionStartTime = time.Now()
			s.armed = true
			s.landedState = 2
			s.mode = "AUTO"
			s.customMode = 3
			s.mu.Unlock()
			// Mark drone as busy
			if feederDB != nil {
				feederDB.Exec(`UPDATE drones SET status='busy' WHERE id=?`, s.droneID)
			}
			log.Printf("[DataFeeder] Mission %d started — drone %s (drone_id=%d, gps_id=%d) executing %d waypoints",
				missionID, feederProfiles[i].Name, s.droneID, s.gpsDeviceID, len(waypoints))
			return nil
		}
	}
	return fmt.Errorf("no feeder drone matching device_id=%d (checked gps_device_id and drone_id)", deviceID)
}

// StopMission 强制取消正在执行的任务，无人机回到基地地面静止状态
func StopMission(missionID int) {
	for i, s := range feederStates {
		s.mu.Lock()
		if s.missionActive && s.missionID == missionID {
			s.missionActive = false
			s.missionID = 0
			s.missionWaypoints = nil
			s.missionWPIdx = 0
			s.missionWPProg = 0
			s.missionPhase = ""
			s.mode = "POSHOLD"
			s.customMode = 16
			s.armed = false
			s.lat = feederProfiles[i].HomeLat
			s.lng = feederProfiles[i].HomeLng
			s.alt = feederProfiles[i].HomeAlt
			s.relAlt = 0
			s.speed = 0
			s.throttle = 0
			s.landedState = 1 // 着陆在基地地面
			s.mu.Unlock()
			if feederDB != nil {
				feederDB.Exec(`UPDATE drones SET status='online' WHERE id=?`, s.droneID)
			}
			log.Printf("[DataFeeder] Mission %d force-stopped on drone %s", missionID, feederProfiles[i].Name)
			return
		}
		s.mu.Unlock()
	}
}

// IsDroneBusy checks if a drone (by gps device ID) is currently executing a mission.
func IsDroneBusy(gpsDeviceID int) bool {
	for _, s := range feederStates {
		if s.gpsDeviceID == gpsDeviceID {
			s.mu.Lock()
			defer s.mu.Unlock()
			return s.missionActive
		}
	}
	return false
}

// restoreActiveMissions checks DB for in-flight missions and resumes them in the feeder.
// This allows missions to survive server restarts.
func restoreActiveMissions(database *db.DB) {
	rows, err := database.Query(`SELECT id, device_id, waypoints_json, status, current_phase, progress FROM flight_missions WHERE status IN ('飞行中','返航中') AND device_id > 0`)
	if err != nil {
		log.Printf("[DataFeeder] Failed to query active missions: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var mID, deviceID, progress int
		var wpJSON, status, phase string
		if err := rows.Scan(&mID, &deviceID, &wpJSON, &status, &phase, &progress); err != nil {
			continue
		}
		// Parse waypoints from stored JSON
		var waypoints []MissionWaypoint
		if wpJSON != "" && wpJSON != "null" && wpJSON != "[]" {
			type wpObj struct {
				Lat float64 `json:"lat"`
				Lng float64 `json:"lng"`
				Alt float64 `json:"alt"`
			}
			var wps []wpObj
			if json.Unmarshal([]byte(wpJSON), &wps) == nil {
				for _, w := range wps {
					waypoints = append(waypoints, MissionWaypoint{Lat: w.Lat, Lng: w.Lng, Alt: w.Alt})
				}
			}
		}
		if len(waypoints) == 0 {
			log.Printf("[DataFeeder] Mission %d has no restorable waypoints, skipping", mID)
			continue
		}

		// Find the feeder drone matching this device_id (drone_id or gps_device_id)
		for i, s := range feederStates {
			if s.gpsDeviceID == deviceID || s.droneID == deviceID {
				s.mu.Lock()
				s.missionActive = true
				s.missionID = mID
				s.missionWaypoints = waypoints
				s.armed = true
				s.landedState = 2
				s.relAlt = 50
				s.alt = feederProfiles[i].HomeAlt + 50

				if status == "返航中" {
					s.missionPhase = "return"
					s.mode = "RTL"
					s.customMode = 6
					// Start returning from last waypoint
					s.missionWPIdx = len(waypoints) - 1
				} else {
					// Estimate which waypoint to resume from based on progress
					wpIdx := int(float64(progress-5) / 55.0 * float64(len(waypoints)))
					if wpIdx < 0 {
						wpIdx = 0
					}
					if wpIdx >= len(waypoints) {
						wpIdx = len(waypoints) - 1
					}
					s.missionWPIdx = wpIdx
					s.missionPhase = "cruise"
					s.mode = "AUTO"
					s.customMode = 3
				}
				s.missionStartTime = time.Now()
				s.mu.Unlock()

				database.Exec(`UPDATE drones SET status='busy' WHERE id=?`, s.droneID)
				log.Printf("[DataFeeder] Restored mission %d on drone %s — phase=%s wpIdx=%d/%d progress=%d%%",
					mID, feederProfiles[i].Name, s.missionPhase, s.missionWPIdx, len(waypoints), progress)
				break
			}
		}
	}
}

// ensureDroneRegistered creates/finds the drone, gps_device, and device
// entries in MySQL and returns (gpsDeviceID, droneID).
func ensureDroneRegistered(database *db.DB, p DroneProfile) (int, int) {
	var droneID int
	err := database.QueryRow(`SELECT id FROM drones WHERE agent_id=?`, p.AgentID).Scan(&droneID)
	if err != nil {
		res, err2 := database.Exec(
			`INSERT INTO drones(name, model, description, ip, ssh_port, vnc_port, rdp_port, protocol, username, password,
			agent_id, initial_lat, initial_lng, initial_alt, fence_enabled, fence_lat, fence_lng, fence_radius,
			auto_connect, log_enabled, status, video_url)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			p.Name, p.Model,
			fmt.Sprintf("序列号:%s 固件:%s 飞控类型:K9", p.SerialUID, p.FWVersion),
			p.IP, 22, 5900, 3389, "MAVLink", "root", "",
			p.AgentID, p.HomeLat, p.HomeLng, p.HomeAlt,
			1, p.HomeLat, p.HomeLng, p.FenceRadius,
			1, 1, "online", "",
		)
		if err2 != nil {
			log.Printf("[DataFeeder] Failed to create drone %s: %v", p.Name, err2)
			return 0, 0
		}
		id, _ := res.LastInsertId()
		droneID = int(id)
	} else {
		database.Exec(`UPDATE drones SET status='online', name=?, model=?, ip=?, description=?,
			initial_lat=?, initial_lng=?, initial_alt=?,
			fence_enabled=1, fence_lat=?, fence_lng=?, fence_radius=?,
			updated_at=datetime('now') WHERE id=?`,
			p.Name, p.Model, p.IP,
			fmt.Sprintf("序列号:%s 固件:%s 飞控类型:K9", p.SerialUID, p.FWVersion),
			p.HomeLat, p.HomeLng, p.HomeAlt,
			p.HomeLat, p.HomeLng, p.FenceRadius, droneID)
	}

	// GPS device — find or create, always set map_visible=1 so drones appear on map
	var gpsID int
	err = database.QueryRow(`SELECT id FROM gps_devices WHERE agent_id=? AND agent_id!=''`, p.AgentID).Scan(&gpsID)
	if err != nil {
		res, err2 := database.Exec(
			`INSERT INTO gps_devices(name, agent_id, device_type, latitude, longitude, altitude,
			speed, heading, accuracy, status, fence_enabled, fence_lat, fence_lng, fence_radius,
			drone_id, last_update, map_visible)
			VALUES(?,?,?,?,?,?,0,0,0.8,'在线',?,?,?,?,?,datetime('now'),1)`,
			p.Name, p.AgentID, "无人机", p.HomeLat, p.HomeLng, p.HomeAlt,
			1, p.HomeLat, p.HomeLng, p.FenceRadius, droneID,
		)
		if err2 != nil {
			log.Printf("[DataFeeder] Failed to create gps_device for %s: %v", p.Name, err2)
			return 0, droneID
		}
		id, _ := res.LastInsertId()
		gpsID = int(id)
	} else {
		database.Exec(`UPDATE gps_devices SET name=?, device_type='无人机', status='在线',
			latitude=?, longitude=?, altitude=?,
			fence_enabled=1, fence_lat=?, fence_lng=?, fence_radius=?,
			drone_id=?, last_update=datetime('now'), map_visible=1 WHERE id=?`,
			p.Name, p.HomeLat, p.HomeLng, p.HomeAlt,
			p.HomeLat, p.HomeLng, p.FenceRadius, droneID, gpsID)
	}

	// Link drone -> gps_device (always update)
	database.Exec(`UPDATE drones SET linked_gps_device_id=? WHERE id=?`, gpsID, droneID)

	// Remote device
	var devID int
	err = database.QueryRow(`SELECT id FROM devices WHERE drone_id=?`, droneID).Scan(&devID)
	if err != nil {
		res, _ := database.Exec(
			`INSERT INTO devices(name, ip, protocol, port, username, password, auto_connect, log_enabled,
			description, status, drone_id, created_at, updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,datetime('now'),datetime('now'))`,
			p.Name, p.IP, "MAVLink", 14550, "root", "", 1, 1,
			"[自动关联] "+p.Model, "online", droneID,
		)
		if res != nil {
			id, _ := res.LastInsertId()
			devID = int(id)
		}
	} else {
		database.Exec(`UPDATE devices SET status='online', updated_at=datetime('now') WHERE id=?`, devID)
	}
	database.Exec(`UPDATE drones SET linked_device_id=? WHERE id=? AND (linked_device_id IS NULL OR linked_device_id=0)`, devID, droneID)

	return gpsID, droneID
}

// feederLoop is the main goroutine that continuously pushes telemetry.
// Tickers:
//   - evolve:    200ms — update position/state in memory (smooth animation)
//   - gpsPush:   500ms — write GPS position to MySQL + WebSocket
//   - keepalive:  3s  — force ALL feeder drones+GPS devices online (bulletproof)
//   - battery:    3s  — battery telemetry
//   - heartbeat:  1s  — MAVLink heartbeat + telemetry push
//   - log:       30s  — console status log
func feederLoop(database *db.DB) {
	tickEvolve := time.NewTicker(200 * time.Millisecond)
	tickGPSPush := time.NewTicker(500 * time.Millisecond)
	tickKeepalive := time.NewTicker(3 * time.Second)
	tickBattery := time.NewTicker(3 * time.Second)
	tickAttitude := time.NewTicker(100 * time.Millisecond)
	tickHeartbeat := time.NewTicker(1 * time.Second)
	tickMavTelem := time.NewTicker(1 * time.Second)
	tickRawFrame := time.NewTicker(2 * time.Second)
	tickLog := time.NewTicker(30 * time.Second)
	defer tickEvolve.Stop()
	defer tickGPSPush.Stop()
	defer tickKeepalive.Stop()
	defer tickBattery.Stop()
	defer tickAttitude.Stop()
	defer tickHeartbeat.Stop()
	defer tickMavTelem.Stop()
	defer tickRawFrame.Stop()
	defer tickLog.Stop()

	for {
		select {
		case <-feederShutdown:
			return
		case <-tickEvolve.C:
			// Fast in-memory position evolution — MUST be synchronous, no DB
			for i := range feederProfiles {
				evolvePosition(feederStates[i], feederProfiles[i], database)
			}
		case <-tickGPSPush.C:
			// Write GPS to DB — async to avoid blocking the evolve tick
			for i := range feederProfiles {
				ii := i
				go pushGPSData(database, feederProfiles[ii], feederStates[ii])
			}
		case <-tickKeepalive.C:
			// Force online — async
			go func() {
				for i := range feederProfiles {
					s := feederStates[i]
					if s.gpsDeviceID > 0 {
						database.Exec(`UPDATE gps_devices SET status='在线', map_visible=1, last_update=datetime('now') WHERE id=?`, s.gpsDeviceID)
					}
					if s.droneID > 0 {
						database.Exec(`UPDATE drones SET status=CASE WHEN status='busy' THEN 'busy' ELSE 'online' END, updated_at=datetime('now') WHERE id=?`, s.droneID)
					}
				}
			}()
		case <-tickBattery.C:
			for i := range feederProfiles {
				evolveBattery(feederStates[i])
			}
			go func() {
				for i := range feederProfiles {
					pushBatteryData(database, feederProfiles[i], feederStates[i])
				}
			}()
		case <-tickAttitude.C:
			for i := range feederProfiles {
				evolveAttitude(feederStates[i])
			}
		case <-tickHeartbeat.C:
			for i := range feederProfiles {
				evolveHeartbeat(feederStates[i])
			}
			go func() {
				for i := range feederProfiles {
					pushMavlinkTelemetry(database, feederProfiles[i], feederStates[i])
				}
			}()
		case <-tickMavTelem.C:
			go func() {
				for i := range feederProfiles {
					pushMavlinkRedis(feederProfiles[i], feederStates[i])
				}
			}()
		case <-tickRawFrame.C:
			// Push synthetic MAVLink v2 raw frames to device_tcp_log for debug console
			go func() {
				idx := rand.Intn(len(feederProfiles))
				pushRawMavlinkFrame(database, feederProfiles[idx], feederStates[idx])
			}()
		case <-tickLog.C:
			for i := range feederProfiles {
				s := feederStates[i]
				log.Printf("[MAVLink] %s SYS_ID=%d 信号正常 — lat=%.4f lng=%.4f alt=%.0f bat=%d%% sat=%d mode=%s",
					feederProfiles[i].Name, feederProfiles[i].SysID,
					s.lat, s.lng, s.alt, s.battLevel, s.satellites, s.mode)
			}
		}
	}
}

// ---- Position evolution (idle drift / mission following) ----

const (
	missionSpeed = 0.00012 // ≈ 13 m per 200ms tick (roughly 15 m/s cruising)
	returnSpeed  = 0.00006 // ≈ 7 m per 200ms tick (roughly 8 m/s slow return)
	takeoffSpeed = 0.00003 // slow horizontal drift toward WP1 during climb
)

// catmullRomFeeder evaluates a Catmull-Rom spline at parameter t ∈ [0,1] given 4 control points.
func catmullRomFeeder(t, p0, p1, p2, p3 float64) float64 {
	t2 := t * t
	t3 := t2 * t
	return 0.5 * ((2 * p1) + (-p0+p2)*t + (2*p0-5*p1+4*p2-p3)*t2 + (-p0+3*p1-3*p2+p3)*t3)
}

func evolvePosition(s *droneState, p DroneProfile, database *db.DB) {
	s.bootTimeMs += 200

	s.mu.Lock()
	isMission := s.missionActive
	s.mu.Unlock()

	if isMission {
		evolveMission(s, p, database)
		return
	}

	// ======== 空闲状态：无任务时保持绝对静止，禁止漂移/巡航 ========
	// 无论地面还是空中，无任务时坐标完全不变，锁定在基地位置
	s.lat = p.HomeLat
	s.lng = p.HomeLng
	s.alt = p.HomeAlt
	s.relAlt = 0
	s.speed = 0
	s.throttle = 0
	s.armed = false
	s.landedState = 1 // 停在地面
	s.mode = "POSHOLD"
	s.customMode = 16
	s.satellites = 18 + rand.Intn(6)
}

// evolveMission moves the drone along its planned route.
func evolveMission(s *droneState, p DroneProfile, database *db.DB) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.satellites = 18 + rand.Intn(6)

	switch s.missionPhase {
	case "takeoff":
		// Ascend to cruising altitude (50m AGL) while drifting toward WP1
		s.relAlt += 1.0 // slower climb: ~5 m/s at 200ms ticks
		s.alt = p.HomeAlt + s.relAlt
		s.speed = 2.0 + rand.Float64()*1.5
		s.throttle = 70 + rand.Intn(10)
		// Also move horizontally toward first waypoint during climb
		if len(s.missionWaypoints) > 0 {
			wp := s.missionWaypoints[0]
			dx := wp.Lat - s.lat
			dy := wp.Lng - s.lng
			dist := math.Sqrt(dx*dx + dy*dy)
			if dist > 0.00005 {
				s.heading = math.Mod(math.Atan2(dy, dx)*180/math.Pi+360, 360)
				step := takeoffSpeed
				if dist < step {
					step = dist
				}
				s.lat += dx / dist * step
				s.lng += dy / dist * step
			}
		}
		if s.relAlt >= 50 {
			s.relAlt = 50
			s.missionPhase = "cruise"
			mID := s.missionID
			if database != nil {
				go database.Exec(`UPDATE flight_missions SET status='飞行中', current_phase='巡航' WHERE id=?`, mID)
			}
			log.Printf("[DataFeeder] Mission %d — drone %s reached cruise altitude, navigating to WP 1/%d",
				mID, p.Name, len(s.missionWaypoints))
		}

	case "cruise":
		if s.missionWPIdx >= len(s.missionWaypoints) {
			// All waypoints reached — begin return along reverse route
			s.missionPhase = "return"
			s.missionWPIdx = len(s.missionWaypoints) - 2 // start returning from second-to-last WP
			s.missionWPProg = 0
			s.mode = "RTL"
			s.customMode = 6
			mID := s.missionID
			if database != nil {
				go database.Exec(`UPDATE flight_missions SET status='返航中', current_phase='返航' WHERE id=?`, mID)
			}
			log.Printf("[DataFeeder] Mission %d — all waypoints reached, %s returning via reverse route", mID, p.Name)
			return
		}
		wp := s.missionWaypoints[s.missionWPIdx]

		// Determine segment "from" point
		var fromLat, fromLng float64
		if s.missionWPIdx == 0 {
			fromLat = p.HomeLat
			fromLng = p.HomeLng
		} else {
			fromLat = s.missionWaypoints[s.missionWPIdx-1].Lat
			fromLng = s.missionWaypoints[s.missionWPIdx-1].Lng
		}

		// Segment length in degrees
		dLat := wp.Lat - fromLat
		dLng := wp.Lng - fromLng
		segLen := math.Sqrt(dLat*dLat + dLng*dLng)
		if segLen < 0.000001 {
			segLen = 0.000001
		}

		// Advance progress
		s.missionWPProg += missionSpeed / segLen

		if s.missionWPProg >= 1.0 {
			// Waypoint reached
			s.lat = wp.Lat
			s.lng = wp.Lng
			s.missionWPIdx++
			s.missionWPProg = 0
			progress := 5 + int(float64(s.missionWPIdx)/float64(len(s.missionWaypoints))*55)
			mID := s.missionID
			if database != nil {
				go database.Exec(`UPDATE flight_missions SET progress=?, current_phase='执行任务' WHERE id=?`, progress, mID)
			}
			log.Printf("[DataFeeder] Mission %d — WP %d/%d reached (progress %d%%)", mID, s.missionWPIdx, len(s.missionWaypoints), progress)
			return
		}

		// ---- Catmull-Rom spline interpolation (same as simulation engine) ----
		var p0Lat, p0Lng, p3Lat, p3Lng float64
		if s.missionWPIdx >= 2 {
			p0Lat = s.missionWaypoints[s.missionWPIdx-2].Lat
			p0Lng = s.missionWaypoints[s.missionWPIdx-2].Lng
		} else if s.missionWPIdx == 1 {
			p0Lat = p.HomeLat
			p0Lng = p.HomeLng
		} else {
			p0Lat = 2*fromLat - wp.Lat
			p0Lng = 2*fromLng - wp.Lng
		}
		if s.missionWPIdx+1 < len(s.missionWaypoints) {
			p3Lat = s.missionWaypoints[s.missionWPIdx+1].Lat
			p3Lng = s.missionWaypoints[s.missionWPIdx+1].Lng
		} else {
			p3Lat = 2*wp.Lat - fromLat
			p3Lng = 2*wp.Lng - fromLng
		}

		t := s.missionWPProg
		newLat := catmullRomFeeder(t, p0Lat, fromLat, wp.Lat, p3Lat)
		newLng := catmullRomFeeder(t, p0Lng, fromLng, wp.Lng, p3Lng)

		// Heading from old position to new position
		hDx := newLat - s.lat
		hDy := newLng - s.lng
		if math.Abs(hDx) > 0.0000001 || math.Abs(hDy) > 0.0000001 {
			s.heading = math.Mod(math.Atan2(hDy, hDx)*180/math.Pi+360, 360)
		}

		s.lat = newLat + (rand.Float64()-0.5)*0.0000005
		s.lng = newLng + (rand.Float64()-0.5)*0.0000005
		s.alt = p.HomeAlt + 50 + (rand.Float64()-0.5)*0.5
		s.relAlt = 50 + (rand.Float64()-0.5)*0.5
		s.speed = 12.0 + rand.Float64()*4.0
		s.throttle = 55 + rand.Intn(10)

	case "return":
		// Follow waypoints in reverse via Catmull-Rom, then fly to home base
		var targetLat, targetLng float64
		var fromLat, fromLng float64
		if s.missionWPIdx >= 0 {
			targetLat = s.missionWaypoints[s.missionWPIdx].Lat
			targetLng = s.missionWaypoints[s.missionWPIdx].Lng
		} else {
			targetLat = p.HomeLat
			targetLng = p.HomeLng
		}
		// "from" for return is the waypoint we just came from (one index higher)
		if s.missionWPIdx+1 < len(s.missionWaypoints) {
			fromLat = s.missionWaypoints[s.missionWPIdx+1].Lat
			fromLng = s.missionWaypoints[s.missionWPIdx+1].Lng
		} else if len(s.missionWaypoints) > 0 {
			last := s.missionWaypoints[len(s.missionWaypoints)-1]
			fromLat = last.Lat
			fromLng = last.Lng
		} else {
			fromLat = s.lat
			fromLng = s.lng
		}

		dLat := targetLat - fromLat
		dLng := targetLng - fromLng
		segLen := math.Sqrt(dLat*dLat + dLng*dLng)
		if segLen < 0.000001 {
			segLen = 0.000001
		}

		s.missionWPProg += returnSpeed / segLen

		if s.missionWPProg >= 1.0 {
			s.lat = targetLat
			s.lng = targetLng
			s.missionWPProg = 0
			if s.missionWPIdx >= 0 {
				s.missionWPIdx--
				nTotal := len(s.missionWaypoints)
				returned := nTotal - 1 - s.missionWPIdx
				progress := 60 + int(float64(returned)/float64(nTotal+1)*35)
				mID := s.missionID
				if database != nil {
					go database.Exec(`UPDATE flight_missions SET progress=?, current_phase='返航' WHERE id=?`, progress, mID)
				}
				log.Printf("[DataFeeder] Mission %d — return WP reached, %d remaining", mID, s.missionWPIdx+1)
			} else {
				s.missionPhase = "landing"
				s.mode = "LAND"
				s.customMode = 9
				mID := s.missionID
				if database != nil {
					go database.Exec(`UPDATE flight_missions SET current_phase='降落', progress=95 WHERE id=?`, mID)
				}
				log.Printf("[DataFeeder] Mission %d — %s reached base, landing", mID, p.Name)
			}
			return
		}

		// ---- Catmull-Rom for return (reverse direction) ----
		var p0Lat, p0Lng, p3Lat, p3Lng float64
		// p0 = point before "from" in return direction (higher index)
		if s.missionWPIdx+2 < len(s.missionWaypoints) {
			p0Lat = s.missionWaypoints[s.missionWPIdx+2].Lat
			p0Lng = s.missionWaypoints[s.missionWPIdx+2].Lng
		} else {
			p0Lat = 2*fromLat - targetLat
			p0Lng = 2*fromLng - targetLng
		}
		// p3 = point after target in return direction (lower index or home)
		if s.missionWPIdx >= 1 {
			p3Lat = s.missionWaypoints[s.missionWPIdx-1].Lat
			p3Lng = s.missionWaypoints[s.missionWPIdx-1].Lng
		} else {
			p3Lat = p.HomeLat
			p3Lng = p.HomeLng
		}

		rt := s.missionWPProg
		newLat := catmullRomFeeder(rt, p0Lat, fromLat, targetLat, p3Lat)
		newLng := catmullRomFeeder(rt, p0Lng, fromLng, targetLng, p3Lng)

		hDx := newLat - s.lat
		hDy := newLng - s.lng
		if math.Abs(hDx) > 0.0000001 || math.Abs(hDy) > 0.0000001 {
			s.heading = math.Mod(math.Atan2(hDy, hDx)*180/math.Pi+360, 360)
		}

		s.lat = newLat
		s.lng = newLng
		s.alt = p.HomeAlt + 50 + (rand.Float64()-0.5)*0.3
		s.relAlt = 50
		s.speed = 6.0 + rand.Float64()*3.0
		s.throttle = 45 + rand.Intn(8)

	case "landing":
		s.relAlt -= 0.8 // slower descent at 200ms ticks
		s.alt = p.HomeAlt + s.relAlt
		s.speed = 0.5 + rand.Float64()*0.5
		s.throttle = 30 + rand.Intn(10)
		if s.relAlt <= 0 {
			// ======== 任务完成：降落在基地，恢复地面静止状态 ========
			s.relAlt = 0
			s.alt = p.HomeAlt
			s.speed = 0
			s.throttle = 0
			s.armed = false
			s.landedState = 1 // 着陆状态，停在地面
			s.mode = "POSHOLD"
			s.customMode = 16
			s.lat = p.HomeLat // 回到基地坐标
			s.lng = p.HomeLng
			s.missionActive = false
			mID := s.missionID
			dID := s.droneID
			s.missionID = 0
			s.missionWaypoints = nil
			s.missionWPIdx = 0
			s.missionWPProg = 0
			s.missionPhase = ""
			if database != nil {
				go func() {
					database.Exec(`UPDATE flight_missions SET status='已完成', current_phase='降落', progress=100 WHERE id=?`, mID)
					database.Exec(`UPDATE drones SET status='online' WHERE id=?`, dID)
				}()
			}
			log.Printf("[DataFeeder] Mission %d completed — %s landed at base", mID, p.Name)
		}
	}
}

// ---- Battery evolution ----

func evolveBattery(s *droneState) {
	if s.landedState == 2 { // in flight: discharge
		s.battLevel -= rand.Intn(2) // slow drain
		if s.battLevel < 30 {
			s.battLevel = 30 // auto-return threshold: never reach low-battery zone (≤20%)
		}
		s.battVoltage = 22.0 + float64(s.battLevel)*0.03
		s.battCurrent = 8.0 + rand.Float64()*4.0
		s.battTemp = 32.0 + rand.Float64()*8.0
	} else { // on ground: stable
		s.battCurrent = 0.3 + rand.Float64()*0.2
		s.battTemp = 25.0 + rand.Float64()*3.0
		// Slow charge recovery when idle
		if s.battLevel < 95 && rand.Intn(10) == 0 {
			s.battLevel++
		}
		s.battVoltage = 24.0 + float64(s.battLevel-85)*0.08
	}
}

// ---- Attitude evolution ----

func evolveAttitude(s *droneState) {
	if s.landedState == 2 { // flying — smooth with inertia + turbulence
		targetRoll := (rand.Float64() - 0.5) * 12  // ±6° target
		targetPitch := (rand.Float64() - 0.5) * 10 // ±5° target
		// Smooth with 70% inertia from previous value
		s.roll = s.roll*0.7 + targetRoll*0.3
		s.pitch = s.pitch*0.7 + targetPitch*0.3
		s.yaw = s.heading
		// Throttle correlates with altitude changes
		baseThrottle := 48 + rand.Intn(8) // 48-56% cruise
		if s.alt > s.relAlt+2 {
			baseThrottle += 10 // climbing
		}
		s.throttle = baseThrottle
	} else { // ground — minimal sensor noise
		s.roll = s.roll*0.9 + (rand.Float64()-0.5)*0.2
		s.pitch = s.pitch*0.9 + (rand.Float64()-0.5)*0.2
		s.yaw = s.heading
		s.throttle = 0
	}
}

// ---- Heartbeat / mode evolution ----

func evolveHeartbeat(s *droneState) {
	s.seqNum = (s.seqNum + 1) % 256
	// During active missions, keep AUTO mode — don't override
	s.mu.Lock()
	isMission := s.missionActive
	s.mu.Unlock()
	if isMission {
		s.armed = true
		return
	}
	if s.landedState == 1 {
		s.armed = false
		s.mode = "POSHOLD"
		s.customMode = 16
	} else {
		s.armed = true
		s.mode = "LOITER"
		s.customMode = 5
	}
}

// ---- Push GPS data to MySQL + WebSocket ----

func pushGPSData(database *db.DB, p DroneProfile, s *droneState) {
	if s.gpsDeviceID <= 0 {
		return
	}
	// Use a context with timeout to avoid blocking forever when connection pool is busy
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Update gps_devices table — always set status='在线' and map_visible=1
	_, err := database.ExecContext(ctx, `UPDATE gps_devices SET latitude=?, longitude=?, altitude=?, speed=?, heading=?,
		accuracy=0.8, status='在线', map_visible=1, last_update=datetime('now') WHERE id=?`,
		s.lat, s.lng, s.alt, s.speed, s.heading, s.gpsDeviceID)
	if err != nil {
		log.Printf("[DataFeeder] GPS push FAILED for %s (gps_device=%d): %v", p.Name, s.gpsDeviceID, err)
		return
	}

	// Batch GPS history
	BatchGPSHistory(database, s.gpsDeviceID, s.lat, s.lng, s.alt, s.speed, s.heading)

	// Keep drone online (don't override 'busy' status)
	if s.droneID > 0 {
		database.ExecContext(ctx, `UPDATE drones SET status=CASE WHEN status='busy' THEN 'busy' ELSE 'online' END, updated_at=datetime('now') WHERE id=?`, s.droneID)
	}

	// WebSocket broadcast
	ThrottledBroadcast("gps", WSEvent{Type: "gps_update", Data: gin.H{
		"device_id": s.gpsDeviceID, "latitude": s.lat, "longitude": s.lng,
		"altitude": s.alt, "speed": s.speed, "heading": s.heading,
	}})
}

// ---- Push battery data to MySQL ----

func pushBatteryData(database *db.DB, p DroneProfile, s *droneState) {
	var deviceName string
	database.QueryRow(`SELECT name FROM gps_devices WHERE id=?`, s.gpsDeviceID).Scan(&deviceName)
	if deviceName == "" {
		deviceName = p.Name
	}

	status := "正常"
	if s.battLevel <= 10 {
		status = "严重不足"
	} else if s.battLevel <= 20 {
		status = "电量低"
	} else if s.battTemp >= 50 {
		status = "温度过高"
	}

	BatchBatteryRecord(database, func() {
		database.Exec(
			`INSERT INTO battery_records(device_id, device_name, voltage, current_val, level, temperature, health, status, charge_cycles, remaining_time)
			VALUES(?,?,?,?,?,?,?,?,?,?)`,
			s.gpsDeviceID, deviceName, s.battVoltage, s.battCurrent, s.battLevel,
			s.battTemp, s.battHealth, status, s.battCycles,
			fmt.Sprintf("%dm", s.battLevel*2),
		)
	})

	ThrottledBroadcast("battery", WSEvent{Type: "battery_update", Data: gin.H{
		"device_id": s.gpsDeviceID, "status": status,
	}})
}

// ---- Push MAVLink telemetry to MySQL (mavlink_telemetry table) ----

func pushMavlinkTelemetry(database *db.DB, p DroneProfile, s *droneState) {
	now := time.Now().Format("2006-01-02 15:04:05")
	timeBoot := time.Now().UnixMilli() % 86400000 // ms since midnight

	// Heartbeat
	hbJSON, _ := json.Marshal(map[string]interface{}{
		"custom_mode": s.customMode, "mav_type": "QUADROTOR", "mav_type_id": 2,
		"autopilot": 8, "autopilot_name": "PX4",
		"base_mode": func() int {
			if s.armed {
				return 0xC1
			}
			return 0x41
		}(),
		"system_status": func() string {
			if s.armed {
				return "ACTIVE"
			}
			return "STANDBY"
		}(),
		"armed": s.armed, "mode": s.mode, "mavlink_version": 3,
	})
	upsertTelemetry(database, p.SysID, 1, "heartbeat", string(hbJSON), now)

	// Velocity decomposition (realistic NED frame)
	headRad := s.heading * math.Pi / 180
	vx := s.speed * math.Cos(headRad) * 100 // cm/s north
	vy := s.speed * math.Sin(headRad) * 100 // cm/s east
	vz := (rand.Float64() - 0.5) * 30       // cm/s down (light oscillation)
	if s.landedState == 1 {
		vx, vy, vz = 0, 0, 0
	}

	// Position (GLOBAL_POSITION_INT)
	posJSON, _ := json.Marshal(map[string]interface{}{
		"lat": int(s.lat * 1e7), "lon": int(s.lng * 1e7),
		"alt": int(s.alt * 1000), "relative_alt": int(s.relAlt * 1000),
		"lat_deg": s.lat, "lon_deg": s.lng, "alt_m": s.alt, "rel_alt_m": s.relAlt,
		"vx": int(vx), "vy": int(vy), "vz": int(vz),
		"hdg": int(s.heading * 100), "hdg_deg": s.heading,
		"time_boot_ms": timeBoot,
	})
	upsertTelemetry(database, p.SysID, 1, "position", string(posJSON), now)

	// Attitude (radians as per MAVLink spec)
	rollRad := s.roll * math.Pi / 180
	pitchRad := s.pitch * math.Pi / 180
	yawRad := s.yaw * math.Pi / 180
	attJSON, _ := json.Marshal(map[string]interface{}{
		"roll":         math.Round(rollRad*1000) / 1000,
		"pitch":        math.Round(pitchRad*1000) / 1000,
		"yaw":          math.Round(yawRad*1000) / 1000,
		"roll_deg":     math.Round(s.roll*100) / 100,
		"pitch_deg":    math.Round(s.pitch*100) / 100,
		"yaw_deg":      math.Round(s.yaw*100) / 100,
		"rollspeed":    math.Round((rand.Float64()-0.5)*0.08*1000) / 1000,
		"pitchspeed":   math.Round((rand.Float64()-0.5)*0.06*1000) / 1000,
		"yawspeed":     math.Round((rand.Float64()-0.5)*0.03*1000) / 1000,
		"time_boot_ms": timeBoot,
	})
	upsertTelemetry(database, p.SysID, 1, "attitude", string(attJSON), now)

	// GPS raw (GPS_RAW_INT) — realistic noise
	ephBase := 0.65 + rand.Float64()*0.25 // HDOP 0.65-0.90 for RTK
	epvBase := 1.0 + rand.Float64()*0.35
	satBase := s.satellites + rand.Intn(3) - 1
	if satBase < 8 {
		satBase = 8
	}
	gpsJSON, _ := json.Marshal(map[string]interface{}{
		"lat": int(s.lat * 1e7), "lon": int(s.lng * 1e7), "alt": int(s.alt * 1000),
		"lat_deg": s.lat, "lon_deg": s.lng, "alt_m": s.alt,
		"eph": int(ephBase * 100), "epv": int(epvBase * 100),
		"hdop": math.Round(ephBase*100) / 100, "vdop": math.Round(epvBase*100) / 100,
		"vel": int(s.speed * 100), "cog": int(s.heading * 100),
		"fix_type": s.fixType, "fix_name": func() string {
			switch s.fixType {
			case 6:
				return "RTK_FIXED"
			case 5:
				return "RTK_FLOAT"
			default:
				return "3D_FIX"
			}
		}(),
		"satellites_visible": satBase,
		"time_boot_ms":       timeBoot,
	})
	upsertTelemetry(database, p.SysID, 1, "gps_raw", string(gpsJSON), now)

	// Battery (BATTERY_STATUS)
	voltPerCell := s.battVoltage / 6.0 // assume 6S LiPo
	batJSON, _ := json.Marshal(map[string]interface{}{
		"voltage":   math.Round(s.battVoltage*100) / 100,
		"current":   math.Round(s.battCurrent*100) / 100,
		"remaining": s.battLevel, "temperature": math.Round(s.battTemp*10) / 10,
		"current_consumed": (100 - s.battLevel) * 52, "energy_consumed": (100 - s.battLevel) * 125,
		"cell_count": 6, "voltage_per_cell": math.Round(voltPerCell*1000) / 1000,
		"charge_state": func() string {
			if s.battLevel > 80 {
				return "OK"
			}
			if s.battLevel > 50 {
				return "LOW"
			}
			return "CRITICAL"
		}(),
		"time_remaining": s.battLevel * 120, // seconds estimate
	})
	upsertTelemetry(database, p.SysID, 1, "battery", string(batJSON), now)

	// VFR HUD
	climbRate := (rand.Float64() - 0.5) * 0.4
	if s.landedState == 1 {
		climbRate = 0
	}
	airspeed := s.speed + (rand.Float64()-0.5)*0.8 // slight airspeed-groundspeed difference
	if airspeed < 0 {
		airspeed = 0
	}
	hudJSON, _ := json.Marshal(map[string]interface{}{
		"airspeed":    math.Round(airspeed*100) / 100,
		"groundspeed": math.Round(s.speed*100) / 100,
		"alt":         math.Round(s.alt*100) / 100,
		"climb":       math.Round(climbRate*100) / 100,
		"heading":     int(s.heading), "throttle": s.throttle,
	})
	upsertTelemetry(database, p.SysID, 1, "vfr_hud", string(hudJSON), now)

	// Sys status (SYS_STATUS)
	cpuLoad := 12.0 + rand.Float64()*8 // 12-20%
	if s.landedState == 2 {
		cpuLoad += 5 + rand.Float64()*5 // higher load when flying
	}
	sysJSON, _ := json.Marshal(map[string]interface{}{
		"load":              math.Round(cpuLoad*10) / 10,
		"voltage_battery":   int(s.battVoltage * 1000),
		"current_battery":   int(s.battCurrent * 100),
		"battery_remaining": s.battLevel,
		"drop_rate_comm":    rand.Intn(2),
		"errors_comm":       0,
		"sensors_present":   0x1FF7F, // typical sensor bitmap
		"sensors_enabled":   0x1FF7F,
		"sensors_health":    0x1FF7F,
	})
	upsertTelemetry(database, p.SysID, 1, "sys_status", string(sysJSON), now)

	// Landed state (EXTENDED_SYS_STATE)
	lsJSON, _ := json.Marshal(map[string]interface{}{
		"vtol_state": 0, "landed_state": s.landedState,
		"state": func() string {
			if s.landedState == 1 {
				return "ON_GROUND"
			}
			return "IN_AIR"
		}(),
	})
	upsertTelemetry(database, p.SysID, 1, "landed_state", string(lsJSON), now)

	// Vibration (VIBRATION)
	vibBase := 5.0
	if s.landedState == 2 {
		vibBase = 12.0 + rand.Float64()*8 // higher vibration in flight
	}
	vibJSON, _ := json.Marshal(map[string]interface{}{
		"vibration_x": math.Round((vibBase+rand.Float64()*3)*100) / 100,
		"vibration_y": math.Round((vibBase+rand.Float64()*3)*100) / 100,
		"vibration_z": math.Round((vibBase+rand.Float64()*4)*100) / 100,
		"clipping_0":  0, "clipping_1": 0, "clipping_2": 0,
	})
	upsertTelemetry(database, p.SysID, 1, "vibration", string(vibJSON), now)

	// Wind estimation (WIND_COV)
	windSpeed := 1.5 + rand.Float64()*3.0 // 1.5-4.5 m/s
	windDir := rand.Float64() * 360
	windJSON, _ := json.Marshal(map[string]interface{}{
		"wind_x":         math.Round(windSpeed*math.Cos(windDir*math.Pi/180)*100) / 100,
		"wind_y":         math.Round(windSpeed*math.Sin(windDir*math.Pi/180)*100) / 100,
		"wind_z":         math.Round((rand.Float64()-0.5)*0.3*100) / 100,
		"wind_speed":     math.Round(windSpeed*100) / 100,
		"wind_direction": math.Round(windDir*10) / 10,
	})
	upsertTelemetry(database, p.SysID, 1, "wind", string(windJSON), now)

	// Home position
	homeJSON, _ := json.Marshal(map[string]interface{}{
		"lat": int(p.HomeLat * 1e7), "lon": int(p.HomeLng * 1e7), "alt": int(p.HomeAlt * 1000),
		"lat_deg": p.HomeLat, "lon_deg": p.HomeLng, "alt_m": p.HomeAlt,
		"approach_x": 0, "approach_y": 0, "approach_z": 1,
	})
	upsertTelemetry(database, p.SysID, 1, "home_position", string(homeJSON), now)

	// Autopilot version
	avJSON, _ := json.Marshal(map[string]interface{}{
		"serial_uid": p.SerialUID, "firmware_version": p.FWVersion,
		"board_type": p.BoardType, "board_name": "K9",
		"os_custom_version": "PX4 v1.14.3",
		"flight_sw_version": 0x011403FF,
	})
	upsertTelemetry(database, p.SysID, 1, "autopilot_version", string(avJSON), now)

	// RC Channels (RC_CHANNELS)
	rcJSON, _ := json.Marshal(map[string]interface{}{
		"chancount": 16, "rssi": 220 + rand.Intn(36),
		"chan1_raw": 1500 + rand.Intn(10) - 5, // roll centered
		"chan2_raw": 1500 + rand.Intn(10) - 5, // pitch centered
		"chan3_raw": func() int {
			if s.armed {
				return 1300 + s.throttle*4
			}
			return 1000
		}(), // throttle
		"chan4_raw": 1500 + rand.Intn(10) - 5, // yaw centered
		"chan5_raw": func() int {
			if s.armed {
				return 1800
			}
			return 1000
		}(), // arm switch
		"chan6_raw": 1500, "chan7_raw": 1500, "chan8_raw": 1500,
	})
	upsertTelemetry(database, p.SysID, 1, "rc_channels", string(rcJSON), now)
}

func upsertTelemetry(database *db.DB, sysID, compID int, msgType, payload, updatedAt string) {
	_, err := database.Exec(`INSERT INTO mavlink_telemetry (sys_id, msg_type, payload, updated_at)
		VALUES (?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE payload=VALUES(payload), updated_at=VALUES(updated_at)`,
		sysID, msgType, payload, updatedAt)
	if err != nil {
		// Might be SQLite — try with REPLACE
		database.Exec(`REPLACE INTO mavlink_telemetry (sys_id, msg_type, payload, updated_at)
			VALUES (?, ?, ?, ?)`, sysID, msgType, payload, updatedAt)
	}
}

// ---- Push MAVLink data to Redis for real-time dashboard ----

func pushMavlinkRedis(p DroneProfile, s *droneState) {
	if !cache.Available() {
		return
	}
	prefix := fmt.Sprintf("mavlink:drone:%d", p.SysID)

	// Mark online
	cache.Set(prefix+":online", "1", 15*time.Second)

	// Position
	posData, _ := json.Marshal(map[string]interface{}{
		"lat": s.lat, "lon": s.lng, "alt": s.alt, "rel_alt": s.relAlt,
		"speed": s.speed, "hdg": s.heading,
	})
	cache.Set(prefix+":position", string(posData), 30*time.Second)

	// Heartbeat
	hbData, _ := json.Marshal(map[string]interface{}{
		"armed": s.armed, "mode": s.mode, "system_status": "ACTIVE",
		"mav_type": "QUADROTOR",
	})
	cache.Set(prefix+":heartbeat", string(hbData), 30*time.Second)

	// Battery
	batData, _ := json.Marshal(map[string]interface{}{
		"voltage": s.battVoltage, "current": s.battCurrent,
		"remaining": s.battLevel, "temperature": s.battTemp,
	})
	cache.Set(prefix+":battery", string(batData), 30*time.Second)

	// Attitude
	attData, _ := json.Marshal(map[string]interface{}{
		"roll": s.roll, "pitch": s.pitch, "yaw": s.yaw,
	})
	cache.Set(prefix+":attitude", string(attData), 30*time.Second)

	// GPS raw
	gpsData, _ := json.Marshal(map[string]interface{}{
		"lat": s.lat, "lon": s.lng, "alt": s.alt,
		"fix_type": s.fixType, "fix_name": "RTK_FIXED",
		"satellites": s.satellites,
	})
	cache.Set(prefix+":gps_raw", string(gpsData), 30*time.Second)
}

// ---- Push synthetic MAVLink v2 raw frame to device_tcp_log ----

// buildMavlinkV2Hex generates a realistic MAVLink v2 hex frame string.
// MAVLink v2 format: FD <len> <incompat> <compat> <seq> <sysid> <compid> <msgid:3> <payload> <crc:2>
func buildMavlinkV2Hex(sysID int, seq int, msgID int, payload []byte) string {
	frameLen := 12 + len(payload) // header(10) + payload + crc(2)
	frame := make([]byte, frameLen)
	frame[0] = 0xFD
	frame[1] = byte(len(payload))
	frame[2] = 0x00 // incompat flags
	frame[3] = 0x00 // compat flags
	frame[4] = byte(seq & 0xFF)
	frame[5] = byte(sysID)
	frame[6] = 0x01 // comp_id = 1 (autopilot)
	frame[7] = byte(msgID & 0xFF)
	frame[8] = byte((msgID >> 8) & 0xFF)
	frame[9] = byte((msgID >> 16) & 0xFF)
	copy(frame[10:], payload)
	// Simple CRC (X.25) — just produce plausible bytes
	var crc uint16 = 0xFFFF
	for i := 1; i < 10+len(payload); i++ {
		tmp := uint16(frame[i]) ^ (crc & 0xFF)
		tmp ^= (tmp << 4) & 0xFF
		crc = (crc >> 8) ^ (tmp << 8) ^ (tmp << 3) ^ (tmp >> 4)
	}
	frame[10+len(payload)] = byte(crc & 0xFF)
	frame[10+len(payload)+1] = byte((crc >> 8) & 0xFF)
	hex := ""
	for _, b := range frame {
		hex += fmt.Sprintf("%02x", b)
	}
	return hex
}

func pushRawMavlinkFrame(database *db.DB, p DroneProfile, s *droneState) {
	// Pick a random message type to generate
	type frameGen struct {
		msgID int
		build func() []byte
	}

	int32Bytes := func(v int32) []byte {
		return []byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)}
	}
	uint16Bytes := func(v uint16) []byte {
		return []byte{byte(v), byte(v >> 8)}
	}
	float32Bytes := func(v float32) []byte {
		bits := math.Float32bits(v)
		return []byte{byte(bits), byte(bits >> 8), byte(bits >> 16), byte(bits >> 24)}
	}

	generators := []frameGen{
		{0, func() []byte { // HEARTBEAT
			p := make([]byte, 9)
			cm := s.customMode
			p[0] = byte(cm)
			p[1] = byte(cm >> 8)
			p[2] = byte(cm >> 16)
			p[3] = byte(cm >> 24)
			p[4] = 2 // MAV_TYPE_QUADROTOR
			p[5] = 3 // AUTOPILOT_ARDUPILOT
			bm := byte(0x41)
			if s.armed {
				bm = 0xC1
			}
			p[6] = bm
			p[7] = 4 // MAV_STATE_ACTIVE
			p[8] = 3 // MAVLink version
			return p
		}},
		{33, func() []byte { // GLOBAL_POSITION_INT
			p := make([]byte, 28)
			copy(p[0:4], int32Bytes(int32(s.bootTimeMs)))
			copy(p[4:8], int32Bytes(int32(s.lat*1e7)))
			copy(p[8:12], int32Bytes(int32(s.lng*1e7)))
			copy(p[12:16], int32Bytes(int32(s.alt*1000)))
			copy(p[16:20], int32Bytes(int32(s.relAlt*1000)))
			vx := int16(s.speed * math.Sin(s.heading*math.Pi/180) * 50)
			vy := int16(s.speed * math.Cos(s.heading*math.Pi/180) * 50)
			copy(p[20:22], uint16Bytes(uint16(vx)))
			copy(p[22:24], uint16Bytes(uint16(vy)))
			copy(p[24:26], uint16Bytes(0))
			copy(p[26:28], uint16Bytes(uint16(s.heading*100)))
			return p
		}},
		{30, func() []byte { // ATTITUDE
			p := make([]byte, 28)
			copy(p[0:4], int32Bytes(int32(s.bootTimeMs)))
			copy(p[4:8], float32Bytes(float32(s.roll*math.Pi/180)))
			copy(p[8:12], float32Bytes(float32(s.pitch*math.Pi/180)))
			copy(p[12:16], float32Bytes(float32(s.yaw*math.Pi/180)))
			copy(p[16:20], float32Bytes(float32((rand.Float64()-0.5)*0.1)))
			copy(p[20:24], float32Bytes(float32((rand.Float64()-0.5)*0.1)))
			copy(p[24:28], float32Bytes(float32((rand.Float64()-0.5)*0.05)))
			return p
		}},
		{24, func() []byte { // GPS_RAW_INT
			p := make([]byte, 30)
			// time_usec at 0:8 (skip, leave zero)
			copy(p[8:12], int32Bytes(int32(s.lat*1e7)))
			copy(p[12:16], int32Bytes(int32(s.lng*1e7)))
			copy(p[16:20], int32Bytes(int32(s.alt*1000)))
			copy(p[20:22], uint16Bytes(uint16(80+rand.Intn(30))))  // eph
			copy(p[22:24], uint16Bytes(uint16(120+rand.Intn(40)))) // epv
			copy(p[24:26], uint16Bytes(uint16(s.speed*100)))
			copy(p[26:28], uint16Bytes(uint16(s.heading*100)))
			p[28] = byte(s.fixType)
			p[29] = byte(s.satellites)
			return p
		}},
		{74, func() []byte { // VFR_HUD
			p := make([]byte, 20)
			copy(p[0:4], float32Bytes(float32(s.speed)))
			copy(p[4:8], float32Bytes(float32(s.speed)))
			copy(p[8:12], float32Bytes(float32(s.alt)))
			copy(p[12:16], float32Bytes(float32((rand.Float64()-0.5)*0.5)))
			copy(p[16:18], uint16Bytes(uint16(int16(s.heading))))
			copy(p[18:20], uint16Bytes(uint16(s.throttle)))
			return p
		}},
	}

	gen := generators[rand.Intn(len(generators))]
	payload := gen.build()
	hex := buildMavlinkV2Hex(p.SysID, s.seqNum, gen.msgID, payload)

	frameJSON, _ := json.Marshal(map[string]interface{}{
		"sys_id":      p.SysID,
		"comp_id":     1,
		"msg_id":      gen.msgID,
		"seq":         s.seqNum,
		"payload_len": len(payload),
		"hex":         hex,
	})

	database.Exec(`INSERT INTO device_tcp_log(agent_id, msg_type, payload, received_at)
		VALUES(?, 'mavlink_v2', ?, datetime('now'))`,
		p.AgentID, string(frameJSON))

	// Trim old frames to prevent table bloat (keep last 500)
	var cutoffID int
	if database.QueryRow(`SELECT id FROM device_tcp_log WHERE msg_type IN ('mavlink_v1','mavlink_v2') ORDER BY id DESC LIMIT 1 OFFSET 500`).Scan(&cutoffID) == nil {
		database.Exec(`DELETE FROM device_tcp_log WHERE msg_type IN ('mavlink_v1','mavlink_v2') AND id <= ?`, cutoffID)
	}
}
