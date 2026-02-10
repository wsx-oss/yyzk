package main

import (
    "database/sql"
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

    "github.com/gin-gonic/gin"
    "smartcontrol/internal/db"
    "smartcontrol/internal/handlers"
    "smartcontrol/internal/middleware"
    "smartcontrol/internal/monitor"
)

//go:embed web/*
var webFS embed.FS

func main() {
    addr := getenv("SC_LISTEN_ADDR", ":8080")
    dbPath := getenv("SC_DB_PATH", "app.db")
    apiToken := getenv("SC_API_TOKEN", "")
    maxUploadMB := getenvInt("SC_MAX_UPLOAD_MB", 64)
    trusted := getenv("SC_TRUSTED_PROXIES", "127.0.0.1")
    thCPU := getenvInt("SC_THRESH_CPU", 85)
    thMEM := getenvInt("SC_THRESH_MEM", 85)
    thDISK := getenvInt("SC_THRESH_DISK", 90)
    thInterval := getenvInt("SC_ALERT_INTERVAL_SEC", 10)

    database, err := db.Open(dbPath)
    if err != nil {
        log.Fatal(err)
    }
    defer database.Close()
    if err := db.Migrate(database); err != nil {
        log.Fatal(err)
    }

    // Create Gin engine without default middleware
    r := gin.New()
    
    // Add custom middleware
    r.Use(middleware.Recovery())
    r.Use(middleware.RequestLogger())
    r.Use(corsMiddleware())
    
    // Rate limiting: 100 requests per minute per IP
    rateLimiter := middleware.NewRateLimiter(500, 1*time.Minute)
    r.Use(rateLimiter.Middleware())
    
    r.GET("/api/healthz", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })

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

    // background threshold alerting
    go runThresholdAlerts(database, thCPU, thMEM, thDISK, time.Duration(thInterval)*time.Second)

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
    if v == "" { return def }
    if n, err := strconv.Atoi(v); err == nil { return n }
    return def
}

func authMiddleware(token string) gin.HandlerFunc {
    if token == "" {
        return func(c *gin.Context) { c.Next() }
    }
    return func(c *gin.Context) {
        p := c.Request.URL.Path
        // allow unauth for health, websocket metrics, vnc proxy and static
        if p == "/api/healthz" || p == "/api/metrics/stream" || p == "/api/vnc/ws" || p == "/api/ssh/ws" || p == "/" || strings.HasPrefix(p, "/app/") {
            c.Next(); return
        }
        if strings.HasPrefix(p, "/api/") {
            // header or query token (for ws or limited clients)
            hdr := c.GetHeader("Authorization")
            if strings.HasPrefix(hdr, "Bearer ") && strings.TrimSpace(hdr[7:]) == token { c.Next(); return }
            if c.Query("token") == token { c.Next(); return }
            c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error":"unauthorized"})
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

func runThresholdAlerts(database *sql.DB, thCPU, thMEM, thDISK int, interval time.Duration) {
    if interval <= 0 { interval = 10 * time.Second }
    ticker := time.NewTicker(interval)
    defer ticker.Stop()
    last := map[string]time.Time{}
    cooldown := 60 * time.Second
    for {
        <-ticker.C
        m, err := monitor.CollectMetrics()
        if err != nil { continue }
        now := time.Now()
        check := func(key, label string, val float64, th int) {
            if th <= 0 { return }
            if val < float64(th) { return }
            if t, ok := last[key]; ok && now.Sub(t) < cooldown { return }
            sev := "warning"
            if val >= float64(th+10) { sev = "critical" }
            msg := label + " 警告: 当前值=" + strconv.FormatFloat(val, 'f', 1, 64) + "% 阈值=" + strconv.Itoa(th) + "%"
            _, _ = database.Exec(`INSERT INTO alerts(category, severity, message) VALUES(?,?,?)`, "threshold", sev, msg)
            last[key] = now
        }
        check("cpu", "CPU利用率", m.CPUPercent, thCPU)
        check("mem", "内存占用", m.MemPercent, thMEM)
        check("disk", "磁盘使用率", m.DiskUsedPercent, thDISK)
    }
}
