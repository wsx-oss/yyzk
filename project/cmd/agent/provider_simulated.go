package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"time"
)

// geolocateByIP queries free IP geolocation APIs to get the approximate location of this machine.
// Returns (lat, lng, ok). Falls back to (0, 0, false) on failure.
// NOTE: IP geolocation is city-level accuracy only (error: 5~50km). Real GPS is 2~10m.
func geolocateByIP() (float64, float64, bool) {
	client := &http.Client{Timeout: 5 * time.Second}

	// Try ip-api.com (free, no key needed)
	resp, err := client.Get("http://ip-api.com/json/?fields=status,lat,lon,city,regionName,query")
	if err == nil {
		defer resp.Body.Close()
		var result struct {
			Status string  `json:"status"`
			Lat    float64 `json:"lat"`
			Lon    float64 `json:"lon"`
			City   string  `json:"city"`
			Region string  `json:"regionName"`
			IP     string  `json:"query"`
		}
		if json.NewDecoder(resp.Body).Decode(&result) == nil && result.Status == "success" && (result.Lat != 0 || result.Lon != 0) {
			log.Printf("[Simulate] IP geolocation result:")
			log.Printf("[Simulate]   Public IP : %s", result.IP)
			log.Printf("[Simulate]   Location  : %s, %s", result.City, result.Region)
			log.Printf("[Simulate]   Coords    : %.4f, %.4f", result.Lat, result.Lon)
			log.Printf("[Simulate]   ⚠ IP定位精度为城市级别（误差5~50公里），与实际位置有偏差属于正常现象")
			log.Printf("[Simulate]   ⚠ 真实无人机GPS模块精度为2~10米，不会有此偏差")
			log.Printf("[Simulate]   提示: 可用 -sim-lat %.4f -sim-lng %.4f 手动指定更精确的坐标", result.Lat, result.Lon)
			return result.Lat, result.Lon, true
		}
	}

	return 0, 0, false
}

// SimulatedProvider generates fake GPS, battery, and flight phase data for demo/testing.
// This is the original simulation logic extracted into the DroneDataProvider interface.
type SimulatedProvider struct {
	// GPS: simulate circular flight around a center point
	centerLat  float64
	centerLng  float64
	radius     float64 // degrees offset (~0.005 ≈ 500m)
	altitude   float64
	angle      float64 // current angle in radians
	angleStep  float64 // radians per push cycle
	ipLocated  bool    // true if center was determined by IP geolocation (city-level accuracy)
	// Battery: simulate gradual drain
	level      int
	health     int
	cycles     int
	voltage    float64
	drainRate  float64 // fractional level decrease per push cycle
	drainAccum float64 // accumulator for fractional drain
	// Flight mission: simulate phase progression
	flightPhases    []string
	flightPhaseDur  []int // how many push cycles each phase lasts
	flightPhaseIdx  int   // current phase index
	flightPhaseTick int   // ticks spent in current phase
	flightCooldown  int   // ticks to wait before starting a new cycle
	flightCoolTick  int   // current cooldown tick counter
}

func NewSimulatedProvider(centerLat, centerLng float64) *SimulatedProvider {
	ipLocated := false
	if centerLat == 0 && centerLng == 0 {
		// Try to auto-detect location from public IP
		if lat, lng, ok := geolocateByIP(); ok {
			centerLat = lat
			centerLng = lng
			ipLocated = true
		} else {
			log.Printf("[Simulate] IP geolocation failed, using Beijing defaults")
			centerLat = 39.908
			centerLng = 116.397
			ipLocated = true // Beijing default is also not your real position
		}
	}
	return &SimulatedProvider{
		centerLat: centerLat,
		centerLng: centerLng,
		ipLocated: ipLocated,
		radius:    0.005,    // ~500m radius
		altitude:  80,
		angle:     0,
		angleStep: 0.1,     // ~6 degrees per cycle
		level:     100,
		health:    95,
		cycles:    42,
		voltage:   12.6,
		drainRate: 0.15,    // ~1% every 7 cycles
		// Flight mission phases and durations (in push cycles)
		// 待命(5) → 起飞(8) → 巡航(15) → 执行任务(20) → 返航(12) → 降落(5)
		flightPhases:   []string{"待命", "起飞", "巡航", "执行任务", "返航", "降落"},
		flightPhaseDur: []int{5, 8, 15, 20, 12, 5},
		flightPhaseIdx: 0,
		flightPhaseTick: 0,
		flightCooldown: 15, // wait 15 cycles before restarting
		flightCoolTick: 0,
	}
}

func (s *SimulatedProvider) Name() string { return "Simulated" }

func (s *SimulatedProvider) Start() error { return nil }

func (s *SimulatedProvider) Stop() {}

func (s *SimulatedProvider) IsReady() bool { return true }

func (s *SimulatedProvider) Tick() {
	s.angle += s.angleStep
	if s.angle >= 2*math.Pi {
		s.angle -= 2 * math.Pi
	}
	// Battery drains slowly using accumulator for fractional values
	s.drainAccum += s.drainRate
	if s.drainAccum >= 1.0 {
		drop := int(s.drainAccum)
		s.level -= drop
		s.drainAccum -= float64(drop)
	}
	if s.level < 0 {
		s.level = 0
	}
	if s.level <= 5 {
		// Simulate battery swap
		s.level = 100
		s.voltage = 12.6
		s.cycles++
		s.drainAccum = 0
	}
	// Voltage correlates with level
	s.voltage = 10.0 + float64(s.level)*0.026

	// Flight mission phase progression
	if s.flightPhaseIdx >= len(s.flightPhases) {
		// In cooldown after completing a full cycle
		s.flightCoolTick++
		if s.flightCoolTick >= s.flightCooldown {
			// Restart the mission cycle
			s.flightPhaseIdx = 0
			s.flightPhaseTick = 0
			s.flightCoolTick = 0
		}
	} else {
		s.flightPhaseTick++
		if s.flightPhaseTick >= s.flightPhaseDur[s.flightPhaseIdx] {
			s.flightPhaseIdx++
			s.flightPhaseTick = 0
		}
	}
}

func (s *SimulatedProvider) FlightPhase() string {
	if s.flightPhaseIdx >= len(s.flightPhases) {
		return "" // in cooldown, no active phase to push
	}
	return s.flightPhases[s.flightPhaseIdx]
}

func (s *SimulatedProvider) FlightPayload(agentID string) map[string]interface{} {
	return map[string]interface{}{
		"agent_id": agentID,
		"phase":    s.FlightPhase(),
	}
}

// gpsAccuracy returns the simulated GPS accuracy in meters.
// IP-geolocated positions report 5000m (city-level), manually specified positions report 2.5m (simulating real GPS).
func (s *SimulatedProvider) gpsAccuracy() float64 {
	if s.ipLocated {
		return 5000.0
	}
	return 2.5
}

func (s *SimulatedProvider) GPSPayload(agentID string) map[string]interface{} {
	lat := s.centerLat + s.radius*math.Sin(s.angle)
	lng := s.centerLng + s.radius*math.Cos(s.angle)
	speed := s.radius * s.angleStep * 111000 // approximate m/s
	heading := math.Mod(s.angle*180/math.Pi+90, 360)
	return map[string]interface{}{
		"agent_id":  agentID,
		"latitude":  math.Round(lat*1e6) / 1e6,
		"longitude": math.Round(lng*1e6) / 1e6,
		"altitude":  s.altitude + (math.Sin(s.angle*2) * 5), // slight altitude variation
		"speed":     math.Round(speed*10) / 10,
		"heading":   math.Round(heading*10) / 10,
		"accuracy":  s.gpsAccuracy(),
	}
}

func (s *SimulatedProvider) BatteryPayload(agentID string) map[string]interface{} {
	temp := 32.0 + float64(100-s.level)*0.12 // hotter when lower
	remaining := fmt.Sprintf("%d分钟", s.level*30/100)
	return map[string]interface{}{
		"agent_id":       agentID,
		"voltage":        math.Round(s.voltage*10) / 10,
		"current_val":    math.Round((3.0+float64(100-s.level)*0.05)*10) / 10,
		"level":          s.level,
		"temperature":    math.Round(temp*10) / 10,
		"health":         s.health,
		"charge_cycles":  s.cycles,
		"remaining_time": remaining,
	}
}
