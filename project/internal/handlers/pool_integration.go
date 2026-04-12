package handlers

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"smartcontrol/internal/cache"
	"smartcontrol/internal/db"
	"smartcontrol/internal/taskpool"

	"github.com/gin-gonic/gin"
)

// PoolRef holds a reference to the global task pool so handlers can submit tasks.
var PoolRef *taskpool.Pool

// ---------- GPS history write batcher ----------

var gpsBatcher *taskpool.WriteBatcher
var gpsBatcherOnce sync.Once

func getGPSBatcher() *taskpool.WriteBatcher {
	gpsBatcherOnce.Do(func() {
		gpsBatcher = taskpool.NewWriteBatcher("gps_history", 50, 2*time.Second)
		log.Println("[PoolIntegration] GPS history write batcher started")
	})
	return gpsBatcher
}

// BatchGPSHistory queues a GPS history insert to be flushed in batch.
func BatchGPSHistory(database *db.DB, deviceID int, lat, lng, alt, speed, heading float64) {
	getGPSBatcher().Add(func() {
		database.Exec(`INSERT INTO gps_history(device_id, latitude, longitude, altitude, speed, heading) VALUES(?,?,?,?,?,?)`,
			deviceID, lat, lng, alt, speed, heading)
	})
}

// ---------- Battery record write batcher ----------

var batteryBatcher *taskpool.WriteBatcher
var batteryBatcherOnce sync.Once

func getBatteryBatcher() *taskpool.WriteBatcher {
	batteryBatcherOnce.Do(func() {
		batteryBatcher = taskpool.NewWriteBatcher("battery", 30, 3*time.Second)
		log.Println("[PoolIntegration] Battery write batcher started")
	})
	return batteryBatcher
}

// BatchBatteryRecord queues a battery record write.
func BatchBatteryRecord(database *db.DB, fn func()) {
	getBatteryBatcher().Add(fn)
}

// ---------- WebSocket broadcast throttler ----------

var wsThrottlers sync.Map // topic -> *taskpool.Throttler

// ThrottledBroadcast sends a WebSocket event but throttles to avoid flooding.
// For high-frequency topics like "gps" and "battery", limit to one push per 200ms.
func ThrottledBroadcast(topic string, event WSEvent) {
	v, loaded := wsThrottlers.Load(topic)
	if !loaded {
		interval := 200 * time.Millisecond
		// Different intervals for different topics
		switch topic {
		case "gps":
			interval = 200 * time.Millisecond
		case "battery":
			interval = 500 * time.Millisecond
		case "flight":
			interval = 300 * time.Millisecond
		default:
			interval = 100 * time.Millisecond
		}
		t := taskpool.NewThrottler(interval, func(data interface{}) {
			if evt, ok := data.(WSEvent); ok {
				hub.Broadcast(topic, evt)
			}
		})
		actual, _ := wsThrottlers.LoadOrStore(topic, t)
		v = actual
	}
	v.(*taskpool.Throttler).Update(event)
}

// ---------- Stats cache for expensive queries ----------

// StatsCacheStore manages multiple stats caches.
type StatsCacheStore struct {
	mu     sync.RWMutex
	caches map[string]*taskpool.StatsCache
}

var statsStore = &StatsCacheStore{caches: make(map[string]*taskpool.StatsCache)}

// statsRedisKey returns the Redis key used for a stats cache entry.
func statsRedisKey(key string) string {
	return "stats:" + key
}

// writeStatsToRedis writes the computed stats to Redis (best-effort).
func writeStatsToRedis(key string, data interface{}, ttl time.Duration) {
	if !cache.Available() {
		return
	}
	if err := cache.SetJSON(statsRedisKey(key), data, ttl); err != nil {
		log.Printf("[StatsCache] Redis write %s failed: %v", key, err)
	}
}

// wrapWithRedisSync wraps a stats compute function so that every refresh
// also writes the result to Redis.
func wrapWithRedisSync(key string, ttl time.Duration, fn func() interface{}) func() interface{} {
	return func() interface{} {
		result := fn()
		writeStatsToRedis(key, result, ttl)
		return result
	}
}

// InitStatsCaches starts background cache refresh for expensive stats queries.
func InitStatsCaches(database *db.DB) {
	// Drone stats cache (refreshes every 10s)
	statsStore.mu.Lock()
	statsStore.caches["drones"] = taskpool.NewStatsCache(10*time.Second, wrapWithRedisSync("drones", 20*time.Second, func() interface{} {
		result := gin.H{}
		var total, online, offline int
		database.QueryRow(`SELECT COUNT(*) FROM drones`).Scan(&total)
		database.QueryRow(`SELECT COUNT(*) FROM drones WHERE status='online'`).Scan(&online)
		database.QueryRow(`SELECT COUNT(*) FROM drones WHERE status='offline'`).Scan(&offline)
		result["total"] = total
		result["online"] = online
		result["offline"] = offline
		return result
	}))

	// GPS stats cache (refreshes every 10s)
	statsStore.caches["gps"] = taskpool.NewStatsCache(10*time.Second, wrapWithRedisSync("gps", 20*time.Second, func() interface{} {
		result := gin.H{}
		var total, online, offline, waiting, fenceEnabled, alertCount int
		database.QueryRow(`SELECT COUNT(*) FROM gps_devices WHERE COALESCE(drone_id,0)>0`).Scan(&total)
		database.QueryRow(`SELECT COUNT(*) FROM gps_devices WHERE COALESCE(drone_id,0)>0 AND status='在线'`).Scan(&online)
		database.QueryRow(`SELECT COUNT(*) FROM gps_devices WHERE COALESCE(drone_id,0)>0 AND status='离线'`).Scan(&offline)
		database.QueryRow(`SELECT COUNT(*) FROM gps_devices WHERE COALESCE(drone_id,0)>0 AND status='等待连接'`).Scan(&waiting)
		database.QueryRow(`SELECT COUNT(*) FROM gps_devices WHERE COALESCE(drone_id,0)>0 AND fence_enabled=1`).Scan(&fenceEnabled)
		database.QueryRow(`SELECT COUNT(*) FROM gps_fence_alerts WHERE acknowledged=0`).Scan(&alertCount)
		result["total"] = total
		result["online"] = online
		result["offline"] = offline
		result["waiting"] = waiting
		result["fence_enabled"] = fenceEnabled
		result["alert_count"] = alertCount
		return result
	}))

	// Battery stats cache (refreshes every 15s)
	statsStore.caches["battery"] = taskpool.NewStatsCache(15*time.Second, wrapWithRedisSync("battery", 30*time.Second, func() interface{} {
		result := gin.H{}
		var total, alertCount int
		var avgLevel float64
		database.QueryRow(`SELECT COUNT(DISTINCT device_id) FROM battery_records`).Scan(&total)
		database.QueryRow(`SELECT COALESCE(AVG(level),0) FROM battery_records br INNER JOIN (SELECT device_id, MAX(created_at) as mt FROM battery_records GROUP BY device_id) latest ON br.device_id = latest.device_id AND br.created_at = latest.mt`).Scan(&avgLevel)
		database.QueryRow(`SELECT COUNT(*) FROM battery_alerts WHERE acknowledged=0`).Scan(&alertCount)
		result["device_count"] = total
		result["avg_level"] = avgLevel
		result["alert_count"] = alertCount
		return result
	}))

	// Alert stats cache (refreshes every 10s)
	statsStore.caches["alerts"] = taskpool.NewStatsCache(10*time.Second, wrapWithRedisSync("alerts", 20*time.Second, func() interface{} {
		result := gin.H{}
		var total, unack, critical, warning int
		database.QueryRow(`SELECT COUNT(*) FROM alerts`).Scan(&total)
		database.QueryRow(`SELECT COUNT(*) FROM alerts WHERE acknowledged=0`).Scan(&unack)
		database.QueryRow(`SELECT COUNT(*) FROM alerts WHERE severity='critical' AND acknowledged=0`).Scan(&critical)
		database.QueryRow(`SELECT COUNT(*) FROM alerts WHERE severity='warning' AND acknowledged=0`).Scan(&warning)
		result["total"] = total
		result["unacknowledged"] = unack
		result["critical"] = critical
		result["warning"] = warning
		return result
	}))

	// Hardware stats cache (refreshes every 15s)
	statsStore.caches["hardware"] = taskpool.NewStatsCache(15*time.Second, wrapWithRedisSync("hardware", 30*time.Second, func() interface{} {
		result := gin.H{}
		var total, online, offline int
		database.QueryRow(`SELECT COUNT(*) FROM hardware_items`).Scan(&total)
		database.QueryRow(`SELECT COUNT(*) FROM hardware_items WHERE status='在线'`).Scan(&online)
		database.QueryRow(`SELECT COUNT(*) FROM hardware_items WHERE status='离线'`).Scan(&offline)

		statusDist := gin.H{}
		rows, err := database.Query("SELECT status, COUNT(*) FROM hardware_items GROUP BY status")
		if err == nil {
			for rows.Next() {
				var s string
				var cnt int
				if rows.Scan(&s, &cnt) == nil {
					statusDist[s] = cnt
				}
			}
			rows.Close()
		}

		typeDist := gin.H{}
		rows2, err := database.Query("SELECT type, COUNT(*) FROM hardware_items GROUP BY type")
		if err == nil {
			for rows2.Next() {
				var t string
				var cnt int
				if rows2.Scan(&t, &cnt) == nil {
					typeDist[t] = cnt
				}
			}
			rows2.Close()
		}

		result["total"] = total
		result["online"] = online
		result["offline"] = offline
		result["status_distribution"] = statusDist
		result["type_distribution"] = typeDist
		return result
	}))

	// Flight missions stats cache (refreshes every 15s)
	statsStore.caches["flight_missions"] = taskpool.NewStatsCache(15*time.Second, wrapWithRedisSync("flight_missions", 30*time.Second, func() interface{} {
		result := gin.H{}
		var total int
		database.QueryRow(`SELECT COUNT(*) FROM flight_missions`).Scan(&total)

		statusDist := gin.H{}
		rows, err := database.Query("SELECT status, COUNT(*) FROM flight_missions GROUP BY status")
		if err == nil {
			for rows.Next() {
				var s string
				var cnt int
				if rows.Scan(&s, &cnt) == nil {
					statusDist[s] = cnt
				}
			}
			rows.Close()
		}
		result["total"] = total
		result["status_distribution"] = statusDist
		return result
	}))

	statsStore.mu.Unlock()
	log.Println("[PoolIntegration] Stats caches initialized (drones, gps, battery, alerts, hardware, flight_missions) with Redis sync")
}

// GetCachedStats returns the cached stats for a given key, or nil if not found.
// It tries Redis first for cross-instance consistency, then falls back to
// the local in-memory StatsCache.
func GetCachedStats(key string) interface{} {
	// Try Redis first
	if cache.Available() {
		var result gin.H
		if err := cache.GetJSON(statsRedisKey(key), &result); err == nil {
			return result
		}
	}
	// Fallback to in-memory
	statsStore.mu.RLock()
	defer statsStore.mu.RUnlock()
	if c, ok := statsStore.caches[key]; ok {
		return c.Get()
	}
	return nil
}

// StopStatsCaches cleans up all caches.
func StopStatsCaches() {
	statsStore.mu.Lock()
	defer statsStore.mu.Unlock()
	for _, c := range statsStore.caches {
		c.Stop()
	}
}

// StopBatchers flushes and stops all write batchers.
func StopBatchers() {
	if gpsBatcher != nil {
		gpsBatcher.Stop()
	}
	if batteryBatcher != nil {
		batteryBatcher.Stop()
	}
}

// ---------- Pool-based flight plan computation ----------

// SubmitFlightPlanTask submits an LLM flight plan computation to the CPU worker pool.
// The handler can call this for long-running LLM calls instead of blocking the HTTP goroutine.
func SubmitFlightPlanTask(pool *taskpool.Pool, name string, fn func(ctx context.Context) error) error {
	return pool.Submit(taskpool.Task{
		Name:     name,
		Group:    "route_planning",
		Priority: taskpool.PriorityHigh,
		Mode:     taskpool.ModeCPU,
		Timeout:  60 * time.Second,
		Fn:       fn,
	})
}

// SubmitCoTAnalysis submits a CoT analysis task to the CPU worker pool.
func SubmitCoTAnalysis(pool *taskpool.Pool, name string, fn func(ctx context.Context) error) error {
	return pool.Submit(taskpool.Task{
		Name:     name,
		Group:    "cot_analysis",
		Priority: taskpool.PriorityNormal,
		Mode:     taskpool.ModeCPU,
		Timeout:  60 * time.Second,
		Fn:       fn,
	})
}

// ---------- Async stats endpoint (uses cached data) ----------

// RegisterCachedStatsRoutes adds a fast stats endpoint that returns all cached stats.
func RegisterCachedStatsRoutes(r *gin.Engine) {
	r.GET("/api/stats/cached", func(c *gin.Context) {
		keys := []string{"drones", "gps", "battery", "alerts", "hardware", "flight_missions"}
		result := gin.H{}
		for _, k := range keys {
			if v := GetCachedStats(k); v != nil {
				result[k] = v
			}
		}
		// Also include pool metrics if available
		if PoolRef != nil {
			metrics := PoolRef.Metrics()
			result["pool"] = metrics
		}
		c.JSON(200, result)
	})
}

// ---------- Simulation helper ----------

// SubmitSimulationTask submits a drone simulation step to the IO worker pool.
func SubmitSimulationTask(pool *taskpool.Pool, droneID int, fn func(ctx context.Context) error) error {
	return pool.Submit(taskpool.Task{
		Name:     fmt.Sprintf("sim:drone:%d", droneID),
		Group:    "simulation",
		Priority: taskpool.PriorityRealtime,
		Mode:     taskpool.ModeIO,
		Timeout:  10 * time.Second,
		Fn:       fn,
	})
}
