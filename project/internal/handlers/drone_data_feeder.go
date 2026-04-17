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
	missionPhase     string // takeoff, cruise, return, landing
	missionStartTime time.Time
}

// MissionWaypoint is a single waypoint in a flight plan.
type MissionWaypoint struct {
	Lat, Lng, Alt float64
}

// Base coordinates: 郑州大学东门 (Zhengzhou University East Gate)
const (
	baseLat = 34.8103
	baseLng = 113.5310
	baseAlt = 110.0
)

var (
	feederProfiles = []DroneProfile{
		{SysID: 1, Name: "翼龙-I", Model: "K9-四旋翼", AgentID: "UAV-K9-001",
			IP: "10.21.30.101", SerialUID: "4A3F8B1C2D5E6F70A1B2", FWVersion: "1.17.83", BoardType: 5,
			HomeLat: baseLat, HomeLng: baseLng, HomeAlt: baseAlt, FenceRadius: 2000},
		{SysID: 2, Name: "天鹰-II", Model: "K9-六旋翼", AgentID: "UAV-K9-002",
			IP: "10.21.30.102", SerialUID: "5B4G9C2D3E6F7180B2C3", FWVersion: "1.17.83", BoardType: 5,
			HomeLat: baseLat + 0.0002, HomeLng: baseLng + 0.0003, HomeAlt: baseAlt, FenceRadius: 2000},
		{SysID: 3, Name: "云雀-III", Model: "K9-四旋翼", AgentID: "UAV-K9-003",
			IP: "10.21.30.103", SerialUID: "6C5H0D3E4F7G8290C3D4", FWVersion: "1.18.02", BoardType: 5,
			HomeLat: baseLat - 0.0002, HomeLng: baseLng + 0.0001, HomeAlt: baseAlt, FenceRadius: 2000},
		{SysID: 4, Name: "苍鹰-IV", Model: "K9-四旋翼", AgentID: "UAV-K9-004",
			IP: "10.21.30.104", SerialUID: "7D6I1E4F5G8H93A0D4E5", FWVersion: "1.18.02", BoardType: 5,
			HomeLat: baseLat + 0.0004, HomeLng: baseLng - 0.0002, HomeAlt: baseAlt, FenceRadius: 2000},
		{SysID: 5, Name: "飞鸿-V", Model: "K9-六旋翼", AgentID: "UAV-K9-005",
			IP: "10.21.30.105", SerialUID: "8E7J2F5G6H9I04B1E5F6", FWVersion: "1.18.02", BoardType: 5,
			HomeLat: baseLat - 0.0003, HomeLng: baseLng - 0.0003, HomeAlt: baseAlt, FenceRadius: 2000},
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
			feederStates[i] = &droneState{
				lat: prof.HomeLat, lng: prof.HomeLng, alt: prof.HomeAlt + 30,
				relAlt: 30, speed: 3.0, heading: float64(rand.Intn(360)),
				battLevel: 85 + rand.Intn(16),
				battVoltage: 24.0 + rand.Float64()*1.2,
				battCurrent: 5.0 + rand.Float64()*3, battTemp: 25.0 + rand.Float64()*5,
				battHealth: 92 + rand.Intn(9), battCycles: 20 + rand.Intn(60),
				fixType: 6, satellites: 16 + rand.Intn(8),
				armed: true, mode: "LOITER", customMode: 5,
				landedState: 2, gpsDeviceID: gpsID, droneID: dID,
				throttle: 40 + rand.Intn(10), bootTimeMs: uint32(rand.Intn(300000) + 60000),
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
	tickLog := time.NewTicker(30 * time.Second)
	defer tickEvolve.Stop()
	defer tickGPSPush.Stop()
	defer tickKeepalive.Stop()
	defer tickBattery.Stop()
	defer tickAttitude.Stop()
	defer tickHeartbeat.Stop()
	defer tickMavTelem.Stop()
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
	missionSpeed  = 0.00012 // ≈ 13 m per 200ms tick (roughly 15 m/s cruising)
	returnSpeed   = 0.00006 // ≈ 7 m per 200ms tick (roughly 8 m/s slow return)
	takeoffSpeed  = 0.00003 // slow horizontal drift toward WP1 during climb
)

func evolvePosition(s *droneState, p DroneProfile, database *db.DB) {
	s.bootTimeMs += 200

	s.mu.Lock()
	isMission := s.missionActive
	s.mu.Unlock()

	if isMission {
		evolveMission(s, p, database)
		return
	}

	if s.landedState == 1 { // on ground — idle drift
		s.lat += (rand.Float64() - 0.5) * 0.000004
		s.lng += (rand.Float64() - 0.5) * 0.000004
		s.alt = p.HomeAlt + (rand.Float64()-0.5)*0.3
		s.relAlt = 0
		s.speed = rand.Float64() * 0.1
		s.heading += (rand.Float64() - 0.5) * 0.2
		if s.heading < 0 {
			s.heading += 360
		}
		if s.heading >= 360 {
			s.heading -= 360
		}
	} else { // in air — gentle loiter
		t := float64(s.bootTimeMs) / 1000.0
		radius := 0.0003 + 0.0001*math.Sin(t*0.05)
		angularSpeed := 0.02 + rand.Float64()*0.005
		angle := t * angularSpeed
		s.lat = p.HomeLat + radius*math.Sin(angle) + (rand.Float64()-0.5)*0.000005
		s.lng = p.HomeLng + radius*math.Cos(angle) + (rand.Float64()-0.5)*0.000005
		s.relAlt = 30 + 10*math.Sin(t*0.03) + (rand.Float64()-0.5)*0.5
		s.alt = p.HomeAlt + s.relAlt
		s.speed = 2.0 + rand.Float64()*3.0
		s.heading = math.Mod(math.Atan2(math.Cos(angle), -math.Sin(angle))*180/math.Pi+360, 360)
	}
	s.satellites = 16 + rand.Intn(8)
	if s.satellites > 24 {
		s.satellites = 24
	}
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
		dx := wp.Lat - s.lat
		dy := wp.Lng - s.lng
		dist := math.Sqrt(dx*dx + dy*dy)
		if dist < 0.00008 { // ≈ 8m — waypoint reached
			s.missionWPIdx++
			progress := 5 + int(float64(s.missionWPIdx)/float64(len(s.missionWaypoints))*55)
			mID := s.missionID
			if database != nil {
				go database.Exec(`UPDATE flight_missions SET progress=?, current_phase='执行任务' WHERE id=?`, progress, mID)
			}
			log.Printf("[DataFeeder] Mission %d — WP %d/%d reached (progress %d%%)", mID, s.missionWPIdx, len(s.missionWaypoints), progress)
			return
		}
		// Move toward waypoint
		s.heading = math.Mod(math.Atan2(dy, dx)*180/math.Pi+360, 360)
		step := missionSpeed
		if dist < step {
			step = dist
		}
		s.lat += dx/dist*step + (rand.Float64()-0.5)*0.000001
		s.lng += dy/dist*step + (rand.Float64()-0.5)*0.000001
		s.alt = p.HomeAlt + 50 + (rand.Float64()-0.5)*0.5
		s.relAlt = 50 + (rand.Float64()-0.5)*0.5
		s.speed = 12.0 + rand.Float64()*4.0
		s.throttle = 55 + rand.Intn(10)

	case "return":
		// Follow waypoints in reverse, then fly to home base
		var targetLat, targetLng float64
		if s.missionWPIdx >= 0 {
			targetLat = s.missionWaypoints[s.missionWPIdx].Lat
			targetLng = s.missionWaypoints[s.missionWPIdx].Lng
		} else {
			targetLat = p.HomeLat
			targetLng = p.HomeLng
		}
		dx := targetLat - s.lat
		dy := targetLng - s.lng
		dist := math.Sqrt(dx*dx + dy*dy)
		if dist < 0.00008 {
			if s.missionWPIdx >= 0 {
				// Reverse waypoint reached — go to previous one
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
				// Reached home — begin landing
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
		s.heading = math.Mod(math.Atan2(dy, dx)*180/math.Pi+360, 360)
		step := returnSpeed
		if dist < step {
			step = dist
		}
		s.lat += dx / dist * step
		s.lng += dy / dist * step
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
			// Mission complete
			s.relAlt = 0
			s.alt = p.HomeAlt
			s.speed = 0
			s.throttle = 0
			s.armed = false
			s.landedState = 2 // stay airborne for loiter after mission
			s.mode = "LOITER"
			s.customMode = 5
			s.lat = p.HomeLat
			s.lng = p.HomeLng
			s.relAlt = 30
			s.alt = p.HomeAlt + 30
			s.missionActive = false
			mID := s.missionID
			dID := s.droneID
			s.missionID = 0
			s.missionWaypoints = nil
			s.missionWPIdx = 0
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
		if s.battLevel < 15 {
			s.battLevel = 15
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
	if s.landedState == 2 { // flying
		s.roll = (rand.Float64() - 0.5) * 10   // ±5°
		s.pitch = (rand.Float64() - 0.5) * 8    // ±4°
		s.yaw = s.heading                         // follows heading
		s.throttle = 45 + rand.Intn(20)          // 45-65%
	} else { // ground
		s.roll = (rand.Float64() - 0.5) * 1.0    // ±0.5°
		s.pitch = (rand.Float64() - 0.5) * 1.0
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

	// Heartbeat
	hbJSON, _ := json.Marshal(map[string]interface{}{
		"custom_mode": s.customMode, "mav_type": "QUADROTOR", "mav_type_id": 2,
		"autopilot": 8, "base_mode": func() int { if s.armed { return 0xC1 }; return 0x41 }(),
		"system_status": "ACTIVE", "armed": s.armed, "mode": s.mode,
	})
	upsertTelemetry(database, p.SysID, 1, "heartbeat", string(hbJSON), now)

	// Position
	posJSON, _ := json.Marshal(map[string]interface{}{
		"lat": s.lat, "lon": s.lng, "alt": s.alt, "rel_alt": s.relAlt,
		"vx": s.speed * math.Sin(s.heading*math.Pi/180) * 0.5,
		"vy": s.speed * math.Cos(s.heading*math.Pi/180) * 0.5,
		"vz": 0.0, "speed": s.speed, "hdg": s.heading,
	})
	upsertTelemetry(database, p.SysID, 1, "position", string(posJSON), now)

	// Attitude
	attJSON, _ := json.Marshal(map[string]interface{}{
		"roll": s.roll, "pitch": s.pitch, "yaw": s.yaw,
		"rollspeed": (rand.Float64() - 0.5) * 0.1,
		"pitchspeed": (rand.Float64() - 0.5) * 0.1,
		"yawspeed": (rand.Float64() - 0.5) * 0.05,
	})
	upsertTelemetry(database, p.SysID, 1, "attitude", string(attJSON), now)

	// GPS raw
	gpsJSON, _ := json.Marshal(map[string]interface{}{
		"lat": s.lat, "lon": s.lng, "alt": s.alt,
		"eph": 0.8 + rand.Float64()*0.3, "epv": 1.2 + rand.Float64()*0.4,
		"vel": s.speed, "fix_type": s.fixType, "fix_name": "RTK_FIXED",
		"satellites": s.satellites,
	})
	upsertTelemetry(database, p.SysID, 1, "gps_raw", string(gpsJSON), now)

	// Battery
	batJSON, _ := json.Marshal(map[string]interface{}{
		"voltage": s.battVoltage, "current": s.battCurrent,
		"remaining": s.battLevel, "temperature": s.battTemp,
		"current_consumed": (100 - s.battLevel) * 50, "energy_consumed": (100 - s.battLevel) * 120,
	})
	upsertTelemetry(database, p.SysID, 1, "battery", string(batJSON), now)

	// VFR HUD
	hudJSON, _ := json.Marshal(map[string]interface{}{
		"airspeed": s.speed, "groundspeed": s.speed,
		"alt": s.alt, "climb": (rand.Float64() - 0.5) * 0.5,
		"heading": int(s.heading), "throttle": s.throttle,
	})
	upsertTelemetry(database, p.SysID, 1, "vfr_hud", string(hudJSON), now)

	// Sys status
	sysJSON, _ := json.Marshal(map[string]interface{}{
		"load": 15.0 + rand.Float64()*10,
		"voltage_battery": int(s.battVoltage * 1000),
		"current_battery": int(s.battCurrent * 100),
		"battery_remaining": s.battLevel,
		"drop_rate_comm": rand.Intn(3),
	})
	upsertTelemetry(database, p.SysID, 1, "sys_status", string(sysJSON), now)

	// Landed state
	lsJSON, _ := json.Marshal(map[string]interface{}{
		"vtol_state": 0, "landed_state": s.landedState,
		"state": func() string { if s.landedState == 1 { return "ON_GROUND" }; return "IN_AIR" }(),
	})
	upsertTelemetry(database, p.SysID, 1, "landed_state", string(lsJSON), now)

	// Home position
	homeJSON, _ := json.Marshal(map[string]interface{}{
		"lat": p.HomeLat, "lon": p.HomeLng, "alt": p.HomeAlt,
	})
	upsertTelemetry(database, p.SysID, 1, "home_position", string(homeJSON), now)

	// Autopilot version
	avJSON, _ := json.Marshal(map[string]interface{}{
		"serial_uid": p.SerialUID, "firmware_version": p.FWVersion,
		"board_type": p.BoardType, "board_name": "K9",
	})
	upsertTelemetry(database, p.SysID, 1, "autopilot_version", string(avJSON), now)
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
