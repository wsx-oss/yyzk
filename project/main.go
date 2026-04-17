package main

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"smartcontrol/internal/agent"
	"smartcontrol/internal/cache"
	"smartcontrol/internal/db"
	"smartcontrol/internal/handlers"
	"smartcontrol/internal/middleware"
	"smartcontrol/internal/monitor"
	"smartcontrol/internal/taskpool"
	"smartcontrol/internal/tcpgateway"

	"github.com/gin-gonic/gin"
)

//go:embed web/*
var webFS embed.FS

func init() {
	// 设置默认时区为中国上海 (UTC+8)
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	time.Local = loc
}

func main() {
	addr := getenv("SC_LISTEN_ADDR", ":8080")
	dbDriver := getenv("SC_DB_DRIVER", "sqlite")
	dbDSN := getenv("SC_DB_PATH", "app.db")
	if dbDriver == "mysql" {
		dbDSN = getenv("SC_MYSQL_DSN", "")
		if dbDSN == "" {
			dbDSN = getenv("MYSQL_DSN", "root:@tcp(127.0.0.1:3306)/smartcontrol?charset=utf8mb4")
		}
	}
	apiToken := getenv("SC_API_TOKEN", "")
	maxUploadMB := getenvInt("SC_MAX_UPLOAD_MB", 64)
	trusted := getenv("SC_TRUSTED_PROXIES", "127.0.0.1")
	thCPU := getenvInt("SC_THRESH_CPU", 85)
	thMEM := getenvInt("SC_THRESH_MEM", 85)
	thDISK := getenvInt("SC_THRESH_DISK", 90)
	thInterval := getenvInt("SC_ALERT_INTERVAL_SEC", 10)

	database, err := db.Open(dbDriver, dbDSN)
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		log.Fatal(err)
	}

	// ---- Redis initialization ----
	redisHost := getenv("REDIS_HOST", "")
	redisPort := getenv("REDIS_PORT", "6379")
	redisPassword := getenv("REDIS_PASSWORD", "")
	redisDB := getenvInt("REDIS_DB", 0)
	if redisHost != "" {
		if err := cache.Init(cache.RedisConfig{
			Host:     redisHost,
			Port:     redisPort,
			Password: redisPassword,
			DB:       redisDB,
		}); err != nil {
			log.Printf("[main] Redis init warning: %v (will degrade to in-memory)", err)
		}
	} else {
		log.Println("[main] REDIS_HOST not set, Redis disabled")
	}
	defer cache.Close()

	// Start embedded hardware agent on port 9100 (for local machine monitoring)
	agentPort := getenvInt("SC_AGENT_PORT", 9100)
	agent.StartBackground(agentPort)

	// Create Gin engine without default middleware
	r := gin.New()

	// Add custom middleware
	r.Use(middleware.Recovery())
	r.Use(middleware.RequestLogger())
	r.Use(corsMiddleware())

	// Rate limiting: 3000 requests per minute per IP (high for local dev with many polling modules)
	rateLimiter := middleware.NewRateLimiter(3000, 1*time.Minute)
	r.Use(rateLimiter.Middleware())

	startTime := time.Now()
	r.GET("/api/healthz", func(c *gin.Context) {
		// DB check
		dbOK := true
		var one int
		if err := database.QueryRow("SELECT 1").Scan(&one); err != nil {
			dbOK = false
		}

		// Redis check
		redisOK := cache.Available()

		// LLM API check (lightweight HEAD, 3s timeout)
		llmOK := false
		llmURL := getenv("LLM_BASE_URL", "https://dashscope.aliyuncs.com")
		hc := &http.Client{Timeout: 3 * time.Second}
		if resp, err := hc.Head(llmURL); err == nil {
			resp.Body.Close()
			llmOK = true
		}

		// Patrol state
		patrolOK := handlers.IsPatrolOnline()

		overall := "ok"
		if !dbOK {
			overall = "degraded"
		}

		c.JSON(200, gin.H{
			"status":          overall,
			"uptime_s":        int(time.Since(startTime).Seconds()),
			"db_reachable":    dbOK,
			"redis_reachable": redisOK,
			"llm_reachable":   llmOK,
			"patrol_online":   patrolOK,
		})
	})

	// security & limits
	if err := r.SetTrustedProxies(strings.Split(trusted, ",")); err != nil {
		log.Printf("warn: SetTrustedProxies: %v", err)
	}
	r.MaxMultipartMemory = int64(maxUploadMB) << 20

	// Register auth routes (no middleware)
	handlers.RegisterAuthRoutes(r, database)

	// Apply auth middleware to API routes
	r.Use(authMiddleware(apiToken))

	handlers.RegisterRoutes(r, database)
	handlers.RegisterCoTRoutes(r, database)
	handlers.RegisterNotificationRoutes(r, database)
	handlers.RegisterAIAssistantRoutes(r, database)
	backupAPI := handlers.RegisterBackupRoutes(r, database)

	// ---- Unified task pool (created early so simulation can use it) ----
	pool := taskpool.New(taskpool.PoolConfig{
		IOWorkers:      16,
		CPUWorkers:     4,
		QueueSize:      1024,
		DefaultTimeout: 30 * time.Second,
	})
	handlers.PoolRef = pool
	handlers.RegisterTaskPoolRoutes(r, pool)

	// ---- Simulation engine & RL trainer (uses pool for async DB writes) ----
	simEngine, rlTrainer := handlers.InitSimEngine(database, pool)
	handlers.RegisterSimulationRoutes(r, database, simEngine, rlTrainer)

	// Shared RAG engine + RAG-enhanced endpoints (alerts analyze, RL explain)
	handlers.InitSharedRAG(database)
	handlers.RegisterRAGEndpoints(r, database)

	// Stats caches & cached stats API
	handlers.InitStatsCaches(database)
	handlers.RegisterCachedStatsRoutes(r)

	sub, _ := fs.Sub(webFS, "web")
	r.StaticFS("/app", http.FS(sub))
	r.GET("/", func(c *gin.Context) {
		// First run logic: if no users, go to register; else go to login
		var cnt int
		if err := database.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&cnt); err == nil {
			if cnt == 0 {
				c.Redirect(http.StatusFound, "/app/register.html")
				return
			}
		}
		c.Redirect(http.StatusFound, "/app/login.html")
	})

	// ---- Register background tasks in the pool ----

	// Threshold alerting (was: raw goroutine)
	pool.SchedulePeriodic("threshold:alerts", "monitoring", time.Duration(thInterval)*time.Second,
		taskpool.PriorityHigh, thresholdAlertTask(database, thCPU, thMEM, thDISK))

	// Drone / GPS offline detection (was: raw goroutine)
	pool.SchedulePeriodic("offline:detection", "monitoring", 10*time.Second,
		taskpool.PriorityHigh, offlineDetectionTask(database))

	// AI patrol inspection (was: 6 raw goroutines)
	handlers.StartPatrolInspection(database, pool)

	// Auto-backup: every 24 hours, keep latest 10 backups
	backupAPI.StartAutoBackup(24*time.Hour, 10)

	// ---- TCP Gateway for raw device connections ----
	tcpPort := getenv("SC_TCP_PORT", "9080")
	tcpGW := tcpgateway.New(":"+tcpPort, database)
	if err := tcpGW.EnsureTable(); err != nil {
		log.Printf("[main] TCP gateway table init warning: %v", err)
	}
	if err := tcpGW.Start(); err != nil {
		log.Printf("[main] TCP gateway start warning: %v (raw TCP disabled)", err)
	}
	handlers.TCPGatewayRef = tcpGW
	handlers.RegisterTCPGatewayRoutes(r)

	// ---- MAVLink Telemetry API ----
	handlers.RegisterMavlinkRoutes(r, database)
	log.Printf("[main] MAVLink telemetry API registered at /api/mavlink/*")

	// ---- Drone Telemetry Data Feeder (ground station relay) ----
	handlers.StartDroneDataFeeder(database)

	srv := &http.Server{Addr: addr, Handler: r}
	go func() {
		log.Printf("listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Printf("shutting down...")

	// Graceful shutdown: flush batchers, stop caches, stop sim engine, stop pool, then HTTP server
	handlers.StopDroneDataFeeder()
	tcpGW.Stop()
	handlers.StopBatchers()
	handlers.StopStatsCaches()
	rlTrainer.StopTraining()
	simEngine.Shutdown()
	pool.Shutdown(5 * time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Server Shutdown: %v", err)
	}
}

func getenv(k, def string) string {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	return v
}

func getenvInt(k string, def int) int {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	if n, err := strconv.Atoi(v); err == nil {
		return n
	}
	return def
}

func authMiddleware(token string) gin.HandlerFunc {
	if token == "" {
		return func(c *gin.Context) { c.Next() }
	}
	return func(c *gin.Context) {
		p := c.Request.URL.Path
		// allow unauth for health, websocket metrics, vnc proxy, sync data exchange and static
		if p == "/api/healthz" || p == "/api/config" || p == "/api/metrics/stream" || p == "/api/vnc/ws" || p == "/api/ssh/ws" ||
			p == "/api/sync/ping" || p == "/api/sync/export-data" || p == "/api/sync/import-data" ||
			p == "/api/hardware/push" || p == "/api/gps/push" || p == "/api/battery/push" || p == "/api/flight/missions/push" ||
			p == "/api/gps/stream" || p == "/api/battery/stream" || p == "/api/flight/stream" || p == "/api/sim/stream" ||
			p == "/" || strings.HasPrefix(p, "/app/") {
			c.Next()
			return
		}
		if strings.HasPrefix(p, "/api/") {
			// header or query token (for ws or limited clients)
			hdr := c.GetHeader("Authorization")
			if strings.HasPrefix(hdr, "Bearer ") && strings.TrimSpace(hdr[7:]) == token {
				c.Next()
				return
			}
			if c.Query("token") == token {
				c.Next()
				return
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		c.Next()
	}
}

func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE, PATCH")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}

// thresholdAlertTask returns a pool-compatible function for threshold alerting.
func thresholdAlertTask(database *db.DB, thCPU, thMEM, thDISK int) func(ctx context.Context) error {
	last := map[string]time.Time{}
	cooldown := 60 * time.Second
	return func(ctx context.Context) error {
		m, err := monitor.CollectMetrics()
		if err != nil {
			return err
		}
		now := time.Now()
		check := func(key, label string, val float64, th int) {
			if th <= 0 || val < float64(th) {
				return
			}
			if t, ok := last[key]; ok && now.Sub(t) < cooldown {
				return
			}
			sev := "warning"
			if val >= float64(th+10) {
				sev = "critical"
			}
			msg := label + " 警告: 当前值=" + strconv.FormatFloat(val, 'f', 1, 64) + "% 阈值=" + strconv.Itoa(th) + "%"
			_, _ = database.Exec(`INSERT INTO alerts(category, severity, message) VALUES(?,?,?)`, "threshold", sev, msg)
			last[key] = now
		}
		check("cpu", "CPU利用率", m.CPUPercent, thCPU)
		check("mem", "内存占用", m.MemPercent, thMEM)
		check("disk", "磁盘使用率", m.DiskUsedPercent, thDISK)
		return nil
	}
}

// offlineDetectionTask returns a pool-compatible function for offline detection.
// When drones transition to offline, their unread anomaly notifications are
// automatically marked as read so that stale alerts don't accumulate.
func offlineDetectionTask(database *db.DB) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		// 1. Collect names of drones that are about to go offline (still 'online' but GPS stale)
		//    Exclude drones managed by the data feeder (agent_id LIKE 'UAV-K9-%') — they never go offline.
		var newlyOfflineNames []string
		rows, err := database.Query(`
			SELECT d.name FROM drones d
			INNER JOIN gps_devices g ON g.id = d.linked_gps_device_id
			WHERE d.status='online'
			  AND d.agent_id NOT LIKE 'UAV-K9-%'
			  AND g.status='在线'
			  AND g.last_update IS NOT NULL
			  AND datetime(g.last_update) < datetime('now','-30 seconds')`)
		if err == nil {
			for rows.Next() {
				var name string
				if rows.Scan(&name) == nil && name != "" {
					newlyOfflineNames = append(newlyOfflineNames, name)
				}
			}
			rows.Close()
		}

		// 2. Standard offline transition updates — exclude feeder-managed GPS devices and drones
		database.Exec(`UPDATE gps_devices SET status='离线' WHERE status='在线' AND last_update IS NOT NULL AND agent_id NOT LIKE 'UAV-K9-%' AND datetime(last_update) < datetime('now','-30 seconds')`)
		database.Exec(`UPDATE drones SET status='offline', updated_at=datetime('now') WHERE status='online' AND agent_id NOT LIKE 'UAV-K9-%' AND linked_gps_device_id > 0 AND linked_gps_device_id IN (SELECT id FROM gps_devices WHERE status='离线')`)
		database.Exec(`UPDATE devices SET status='offline', updated_at=datetime('now') WHERE status='online' AND drone_id > 0 AND drone_id IN (SELECT id FROM drones WHERE status='offline' AND agent_id NOT LIKE 'UAV-K9-%')`)
		database.Exec(`UPDATE hardware_items SET status='离线' WHERE status='在线' AND detected_at IS NOT NULL AND datetime(detected_at) < datetime('now','-30 seconds')`)

		// 3. Auto-clear unread notifications for newly-offline drones
		for _, name := range newlyOfflineNames {
			pattern := "%" + name + "%"
			database.Exec(`UPDATE notifications SET is_read=1 WHERE is_read=0 AND (type='battery' OR type='drone' OR type='alert') AND (title LIKE ? OR message LIKE ?)`, pattern, pattern)
		}
		return nil
	}
}
