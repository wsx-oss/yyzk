package handlers

import (
    "database/sql"
    "encoding/json"
    "io"
    "mime/multipart"
    "net"
    "net/http"
    "os"
    "path/filepath"
    "strconv"
    "strings"
    "time"

    "github.com/gin-gonic/gin"
    "github.com/gorilla/websocket"
    "smartcontrol/internal/monitor"
    "smartcontrol/internal/utils"
)

type API struct {
    db *sql.DB
}

// DevicesList returns devices filtered by optional name and protocol, ordered with matching items first
func (a *API) DevicesList(c *gin.Context) {
    name := strings.TrimSpace(c.Query("name"))
    protocol := strings.TrimSpace(strings.ToUpper(c.Query("protocol")))

    var args []any
    // 不做 WHERE 过滤，仅按匹配程度置顶
    q := "SELECT id, name, ip, protocol, status, created_at, updated_at, last_connected_at FROM devices"
    // 按名称匹配、协议匹配进行权重排序，其次按创建时间倒序
    q += " ORDER BY (CASE WHEN ? != '' AND LOWER(name) LIKE LOWER(?) THEN 0 ELSE 1 END)"
    q += ", (CASE WHEN ? != '' AND UPPER(protocol) = ? THEN 0 ELSE 1 END)"
    q += ", datetime(created_at) DESC"
    args = append(args, name, "%"+name+"%", protocol, protocol)

    rows, err := a.db.Query(q, args...)
    if err != nil { c.JSON(500, gin.H{"error": err.Error()}); return }
    defer rows.Close()
    items := []gin.H{}
    for rows.Next() {
        var id int; var n, ip, proto, status string; var created, updated, last sql.NullString
        if err := rows.Scan(&id, &n, &ip, &proto, &status, &created, &updated, &last); err == nil {
            items = append(items, gin.H{
                "id": id, "name": n, "ip": ip, "protocol": proto, "status": status,
                "created_at": created.String, "updated_at": updated.String, "last_connected_at": last.String,
            })
        }
    }
    c.JSON(200, gin.H{"items": items})
}

// DevicesCreate inserts a new device with offline status
func (a *API) DevicesCreate(c *gin.Context) {
    var p struct { Name, IP, Protocol string }
    if err := c.BindJSON(&p); err != nil { c.JSON(400, gin.H{"error": "bad json"}); return }
    p.Name = strings.TrimSpace(p.Name)
    p.IP = strings.TrimSpace(p.IP)
    p.Protocol = strings.ToUpper(strings.TrimSpace(p.Protocol))
    if p.Name == "" || p.IP == "" || p.Protocol == "" {
        c.JSON(400, gin.H{"error": "name, ip, protocol required"}); return
    }
    if p.Protocol != "VNC" && p.Protocol != "RDP" && p.Protocol != "SSH" {
        c.JSON(400, gin.H{"error": "protocol must be VNC/RDP/SSH"}); return
    }
    // duplicate check
    var cnt int
    if err := a.db.QueryRow(`SELECT COUNT(1) FROM devices WHERE name = ? AND ip = ? AND protocol = ?`, p.Name, p.IP, p.Protocol).Scan(&cnt); err == nil && cnt > 0 {
        c.JSON(409, gin.H{"error": "device already exists"}); return
    }
    _, err := a.db.Exec(`INSERT INTO devices(name, ip, protocol, status, created_at, updated_at) VALUES(?,?,?,?,datetime('now'),datetime('now'))`, p.Name, p.IP, p.Protocol, "offline")
    if err != nil { c.JSON(500, gin.H{"error": err.Error()}); return }
    c.JSON(200, gin.H{"ok": true})
}

// DevicesDelete removes a device by id
func (a *API) DevicesDelete(c *gin.Context) {
    id := c.Param("id")
    if _, err := a.db.Exec(`DELETE FROM devices WHERE id = ?`, id); err != nil {
        c.JSON(500, gin.H{"error": err.Error()}); return
    }
    c.JSON(200, gin.H{"ok": true})
}

// DeviceSetStatus sets device online/offline and updates times
func (a *API) DeviceSetStatus(c *gin.Context) {
    id := c.Param("id")
    var p struct{ Status string }
    if err := c.BindJSON(&p); err != nil { c.JSON(400, gin.H{"error":"bad json"}); return }
    s := strings.ToLower(strings.TrimSpace(p.Status))
    if s != "online" && s != "offline" { c.JSON(400, gin.H{"error":"status must be online/offline"}); return }
    if s == "online" {
        _, err := a.db.Exec(`UPDATE devices SET status='online', last_connected_at=datetime('now'), updated_at=datetime('now') WHERE id=?`, id)
        if err != nil { c.JSON(500, gin.H{"error": err.Error()}); return }
    } else {
        _, err := a.db.Exec(`UPDATE devices SET status='offline', updated_at=datetime('now') WHERE id=?`, id)
        if err != nil { c.JSON(500, gin.H{"error": err.Error()}); return }
    }
    c.JSON(200, gin.H{"ok": true})
}

func (a *API) userIDFromToken(c *gin.Context) (int, error) {
    token := c.GetHeader("Authorization")
    if token == "" { token = c.Query("token") }
    if token == "" { return 0, sql.ErrNoRows }
    if len(token) > 7 && token[:7] == "Bearer " { token = token[7:] }
    var uid int
    err := a.db.QueryRow(`SELECT user_id FROM sessions WHERE token = ?`, token).Scan(&uid)
    if err != nil { return 0, err }
    return uid, nil
}

// UserStatsGet returns total_connections for current user
func (a *API) UserStatsGet(c *gin.Context) {
    uid, err := a.userIDFromToken(c)
    if err != nil { c.JSON(401, gin.H{"error":"unauthorized"}); return }
    var total sql.NullInt64
    _ = a.db.QueryRow(`SELECT total_connections FROM user_stats WHERE user_id = ?`, uid).Scan(&total)
    c.JSON(200, gin.H{"total_connections": total.Int64})
}

// UserStatsIncrConnection increments total_connections for current user
func (a *API) UserStatsIncrConnection(c *gin.Context) {
    uid, err := a.userIDFromToken(c)
    if err != nil { c.JSON(401, gin.H{"error":"unauthorized"}); return }
    // ensure row exists then increment
    if _, err := a.db.Exec(`INSERT OR IGNORE INTO user_stats(user_id, total_connections) VALUES(?, 0)`, uid); err != nil {
        c.JSON(500, gin.H{"error": err.Error()}); return
    }
    if _, err := a.db.Exec(`UPDATE user_stats SET total_connections = total_connections + 1, updated_at=datetime('now') WHERE user_id = ?`, uid); err != nil {
        c.JSON(500, gin.H{"error": err.Error()}); return
    }
    c.JSON(200, gin.H{"ok": true})
}

func RegisterRoutes(r *gin.Engine, database *sql.DB) {
    a := &API{db: database}
    api := r.Group("/api")
    {
        api.GET("/metrics/snapshot", a.MetricsSnapshot)
        api.GET("/metrics/stream", a.MetricsStream)
        api.GET("/hardware/snapshot", a.HardwareSnapshot)

        api.POST("/audio/upload", a.AudioUpload)
        api.POST("/audio/add", a.AudioAdd)
        api.GET("/audio/list", a.AudioList)
        api.GET("/audio/download/:id", a.AudioDownload)
        api.DELETE("/audio/:id", a.AudioDelete)

        api.GET("/alerts/list", a.AlertsList)
        api.POST("/alerts/ack/:id", a.AlertAck)
        api.POST("/alerts/new", a.AlertNew)

        api.POST("/logs/append", a.LogAppend)
        api.GET("/logs/list", a.LogList)

        api.GET("/updates/list", a.UpdatesList)
        api.POST("/updates/add", a.UpdatesAdd)
        api.GET("/updates/check", a.UpdatesCheck)

        api.GET("/sync/status", a.SyncStatusGet)
        api.POST("/sync/status", a.SyncStatusSet)

        api.GET("/report/perf", a.ReportPerf)

        r.GET("/api/vnc/ws", a.VNCProxyWS)

        // devices management
        api.GET("/devices", a.DevicesList)
        api.POST("/devices", a.DevicesCreate)
        api.DELETE("/devices/:id", a.DevicesDelete)
        api.POST("/devices/:id/status", a.DeviceSetStatus)

        // user stats
        api.GET("/user/stats", a.UserStatsGet)
        api.POST("/user/stats/incr_connection", a.UserStatsIncrConnection)
    }
}

func (a *API) MetricsSnapshot(c *gin.Context) {
    m, err := monitor.CollectMetrics()
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
        return
    }
    c.JSON(200, m)
}

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

func (a *API) MetricsStream(c *gin.Context) {
    ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
    if err != nil { return }
    defer ws.Close()
    for {
        m, err := monitor.CollectMetrics()
        if err != nil { return }
        b, _ := json.Marshal(m)
        if err := ws.WriteMessage(websocket.TextMessage, b); err != nil { return }
        time.Sleep(1 * time.Second)
    }
}

func (a *API) HardwareSnapshot(c *gin.Context) {
    hs, err := monitor.HardwareInfo()
    if err != nil {
        c.JSON(500, gin.H{"error": err.Error()})
        return
    }
    c.JSON(200, hs)
}

func saveUploadedFile(fh *multipart.FileHeader, dstDir string) (string, int64, error) {
    if err := os.MkdirAll(dstDir, 0o755); err != nil { return "", 0, err }
    f, err := fh.Open()
    if err != nil { return "", 0, err }
    defer f.Close()
    name := time.Now().Format("20060102_150405") + "_" + filepath.Base(fh.Filename)
    path := filepath.Join(dstDir, name)
    out, err := os.Create(path)
    if err != nil { return "", 0, err }
    defer out.Close()
    n, err := io.Copy(out, f)
    return path, n, err
}

func (a *API) AudioUpload(c *gin.Context) {
    fh, err := c.FormFile("file")
    if err != nil { c.JSON(400, gin.H{"error":"missing file"}); return }
    durationStr := c.PostForm("duration")
    var duration float64
    if d, err := strconv.ParseFloat(durationStr, 64); err == nil { duration = d }
    path, size, err := saveUploadedFile(fh, filepath.Join("data", "recordings"))
    if err != nil { c.JSON(500, gin.H{"error": err.Error()}); return }
    userId := c.PostForm("user_id")
    interactType := c.PostForm("interact_type")
    content := c.PostForm("content")
    clarity := c.PostForm("clarity")
    result := c.PostForm("result")
    scoreStr := c.PostForm("score")
    score := 4
    if s, err := strconv.Atoi(scoreStr); err == nil { score = s }
    tags := c.PostForm("tags")
    remark := c.PostForm("remark")
    interactTime := c.PostForm("interact_time")
    res, err := a.db.Exec(`INSERT INTO recordings(filename, mime, duration, size, user_id, interact_type, content, clarity, result, score, tags, remark, interact_time) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
        path, fh.Header.Get("Content-Type"), duration, size, userId, interactType, content, clarity, result, score, tags, remark, interactTime)
    if err != nil { c.JSON(500, gin.H{"error": err.Error()}); return }
    newId, _ := res.LastInsertId()
    c.JSON(200, gin.H{"ok": true, "id": newId})
}

func (a *API) AudioAdd(c *gin.Context) {
    var p struct {
        UserId      string `json:"user_id"`
        InteractType string `json:"interact_type"`
        Content     string `json:"content"`
        Clarity     string `json:"clarity"`
        Result      string `json:"result"`
        Score       int    `json:"score"`
        Tags        string `json:"tags"`
        Remark      string `json:"remark"`
        InteractTime string `json:"interact_time"`
    }
    if err := c.BindJSON(&p); err != nil { c.JSON(400, gin.H{"error":"bad json"}); return }
    if p.Score == 0 { p.Score = 4 }
    res, err := a.db.Exec(`INSERT INTO recordings(filename, mime, duration, size, user_id, interact_type, content, clarity, result, score, tags, remark, interact_time) VALUES('','',0,0,?,?,?,?,?,?,?,?,?)`,
        p.UserId, p.InteractType, p.Content, p.Clarity, p.Result, p.Score, p.Tags, p.Remark, p.InteractTime)
    if err != nil { c.JSON(500, gin.H{"error": err.Error()}); return }
    newId, _ := res.LastInsertId()
    c.JSON(200, gin.H{"ok": true, "id": newId})
}

func (a *API) AudioList(c *gin.Context) {
    pagination := utils.GetPagination(c)
    
    // Get total count
    var total int
    _ = a.db.QueryRow(`SELECT COUNT(*) FROM recordings`).Scan(&total)
    
    rows, err := a.db.Query(`SELECT id, filename, mime, duration, size, user_id, interact_type, content, clarity, result, score, tags, remark, interact_time, created_at FROM recordings ORDER BY id DESC LIMIT ? OFFSET ?`,
        pagination.PageSize, pagination.Offset)
    if err != nil { c.JSON(500, gin.H{"error": err.Error()}); return }
    defer rows.Close()
    var items []gin.H
    for rows.Next() {
        var id int; var filename, mime, userId, iType, content, clarity, result, tags, remark, iTime, created string; var duration float64; var size int64; var score int
        if err := rows.Scan(&id, &filename, &mime, &duration, &size, &userId, &iType, &content, &clarity, &result, &score, &tags, &remark, &iTime, &created); err == nil {
            items = append(items, gin.H{"id": id, "filename": filepath.Base(filename), "mime": mime, "duration": duration, "size": size, "user_id": userId, "interact_type": iType, "content": content, "clarity": clarity, "result": result, "score": score, "tags": tags, "remark": remark, "interact_time": iTime, "created_at": created})
        }
    }
    c.JSON(200, gin.H{
        "items": items,
        "total": total,
        "page": pagination.Page,
        "page_size": pagination.PageSize,
    })
}

func (a *API) AudioDownload(c *gin.Context) {
    id := c.Param("id")
    var filename string
    err := a.db.QueryRow(`SELECT filename FROM recordings WHERE id = ?`, id).Scan(&filename)
    if err != nil { c.JSON(404, gin.H{"error":"not found"}); return }
    c.FileAttachment(filename, filepath.Base(filename))
}

func (a *API) AudioDelete(c *gin.Context) {
    id := c.Param("id")
    var filename string
    err := a.db.QueryRow(`SELECT filename FROM recordings WHERE id = ?`, id).Scan(&filename)
    if err == sql.ErrNoRows {
        c.JSON(404, gin.H{"error": "recording not found"})
        return
    }
    if err != nil {
        c.JSON(500, gin.H{"error": err.Error()})
        return
    }
    // Delete from database
    _, err = a.db.Exec(`DELETE FROM recordings WHERE id = ?`, id)
    if err != nil {
        c.JSON(500, gin.H{"error": err.Error()})
        return
    }
    // Delete file from disk
    if err := os.Remove(filename); err != nil && !os.IsNotExist(err) {
        // Log error but don't fail the request
        c.JSON(200, gin.H{"ok": true, "warning": "file deletion failed"})
        return
    }
    c.JSON(200, gin.H{"ok": true})
}

func (a *API) AlertsList(c *gin.Context) {
    pagination := utils.GetPagination(c)
    
    var total int
    _ = a.db.QueryRow(`SELECT COUNT(*) FROM alerts`).Scan(&total)
    
    rows, err := a.db.Query(`SELECT id, category, severity, message, acknowledged, created_at FROM alerts ORDER BY id DESC LIMIT ? OFFSET ?`,
        pagination.PageSize, pagination.Offset)
    if err != nil { c.JSON(500, gin.H{"error": err.Error()}); return }
    defer rows.Close()
    var items []gin.H
    for rows.Next() {
        var id int; var cat, sev, msg string; var ack int; var created string
        if err := rows.Scan(&id, &cat, &sev, &msg, &ack, &created); err == nil {
            items = append(items, gin.H{"id": id, "category": cat, "severity": sev, "message": msg, "acknowledged": ack == 1, "created_at": created})
        }
    }
    c.JSON(200, gin.H{
        "items": items,
        "total": total,
        "page": pagination.Page,
        "page_size": pagination.PageSize,
    })
}

func (a *API) AlertAck(c *gin.Context) {
    id := c.Param("id")
    _, err := a.db.Exec(`UPDATE alerts SET acknowledged = 1 WHERE id = ?`, id)
    if err != nil { c.JSON(500, gin.H{"error": err.Error()}); return }
    c.JSON(200, gin.H{"ok": true})
}

func (a *API) AlertNew(c *gin.Context) {
    var p struct{ Category, Severity, Message string }
    if err := c.BindJSON(&p); err != nil { c.JSON(400, gin.H{"error":"bad json"}); return }
    _, err := a.db.Exec(`INSERT INTO alerts(category, severity, message) VALUES(?,?,?)`, p.Category, p.Severity, p.Message)
    if err != nil { c.JSON(500, gin.H{"error": err.Error()}); return }
    c.JSON(200, gin.H{"ok": true})
}

func (a *API) LogAppend(c *gin.Context) {
    var p struct{ Level, Message, Meta string }
    if err := c.BindJSON(&p); err != nil { c.JSON(400, gin.H{"error":"bad json"}); return }
    _, err := a.db.Exec(`INSERT INTO logs(level, message, meta) VALUES(?,?,?)`, p.Level, p.Message, p.Meta)
    if err != nil { c.JSON(500, gin.H{"error": err.Error()}); return }
    c.JSON(200, gin.H{"ok": true})
}

func (a *API) LogList(c *gin.Context) {
    pagination := utils.GetPagination(c)
    
    var total int
    _ = a.db.QueryRow(`SELECT COUNT(*) FROM logs`).Scan(&total)
    
    rows, err := a.db.Query(`SELECT id, level, message, meta, created_at FROM logs ORDER BY id DESC LIMIT ? OFFSET ?`,
        pagination.PageSize, pagination.Offset)
    if err != nil { c.JSON(500, gin.H{"error": err.Error()}); return }
    defer rows.Close()
    var items []gin.H
    for rows.Next() {
        var id int; var level, message, meta, created string
        if err := rows.Scan(&id, &level, &message, &meta, &created); err == nil {
            items = append(items, gin.H{"id": id, "level": level, "message": message, "meta": meta, "created_at": created})
        }
    }
    c.JSON(200, gin.H{
        "items": items,
        "total": total,
        "page": pagination.Page,
        "page_size": pagination.PageSize,
    })
}

func (a *API) UpdatesList(c *gin.Context) {
    rows, err := a.db.Query(`SELECT id, version, description, status, created_at FROM updates ORDER BY id DESC`)
    if err != nil { c.JSON(500, gin.H{"error": err.Error()}); return }
    defer rows.Close()
    var items []gin.H
    for rows.Next() {
        var id int; var v, d, s, created string
        if err := rows.Scan(&id, &v, &d, &s, &created); err == nil {
            items = append(items, gin.H{"id": id, "version": v, "description": d, "status": s, "created_at": created})
        }
    }
    c.JSON(200, gin.H{"items": items})
}

func (a *API) UpdatesAdd(c *gin.Context) {
    var p struct{ Version, Description, Status string }
    if err := c.BindJSON(&p); err != nil { c.JSON(400, gin.H{"error":"bad json"}); return }
    _, err := a.db.Exec(`INSERT INTO updates(version, description, status) VALUES(?,?,?)`, p.Version, p.Description, p.Status)
    if err != nil { c.JSON(500, gin.H{"error": err.Error()}); return }
    c.JSON(200, gin.H{"ok": true})
}

func (a *API) UpdatesCheck(c *gin.Context) {
    currentVersion := strings.TrimSpace(c.Query("current"))
    if currentVersion == "" {
        currentVersion = "0.0.0"
    }
    var latestVersion sql.NullString
    err := a.db.QueryRow(`SELECT version FROM updates ORDER BY id DESC LIMIT 1`).Scan(&latestVersion)
    if err != nil || !latestVersion.Valid || latestVersion.String == "" {
        c.JSON(200, gin.H{"latest_version": currentVersion, "has_update": false})
        return
    }
    hasUpdate := latestVersion.String != currentVersion
    c.JSON(200, gin.H{"latest_version": latestVersion.String, "has_update": hasUpdate})
}

func (a *API) SyncStatusGet(c *gin.Context) {
    var status, message, last, updated sql.NullString
    row := a.db.QueryRow(`SELECT status, message, last_synced_at, updated_at FROM sync_status WHERE id = 1`)
    _ = row.Scan(&status, &message, &last, &updated)
    c.JSON(200, gin.H{"status": status.String, "message": message.String, "last_synced_at": last.String, "updated_at": updated.String})
}

func (a *API) SyncStatusSet(c *gin.Context) {
    var p struct{ Status, Message string }
    if err := c.BindJSON(&p); err != nil { c.JSON(400, gin.H{"error":"bad json"}); return }
    _, err := a.db.Exec(`UPDATE sync_status SET status=?, message=?, last_synced_at=datetime('now'), updated_at=datetime('now') WHERE id = 1`, p.Status, p.Message)
    if err != nil { c.JSON(500, gin.H{"error": err.Error()}); return }
    c.JSON(200, gin.H{"ok": true})
}

func (a *API) ReportPerf(c *gin.Context) {
    m, _ := monitor.CollectMetrics()
    var cntLogs, cntAlerts, cntCrit, cntWarn int
    _ = a.db.QueryRow(`SELECT COUNT(1) FROM logs`).Scan(&cntLogs)
    _ = a.db.QueryRow(`SELECT COUNT(1) FROM alerts`).Scan(&cntAlerts)
    _ = a.db.QueryRow(`SELECT COUNT(1) FROM alerts WHERE severity = 'critical'`).Scan(&cntCrit)
    _ = a.db.QueryRow(`SELECT COUNT(1) FROM alerts WHERE severity = 'warning'`).Scan(&cntWarn)
    summary := gin.H{
        "timestamp": time.Now().Format(time.RFC3339),
        "metrics": m,
        "logs_count": cntLogs,
        "alerts_count": cntAlerts,
        "alerts_breakdown": gin.H{"critical": cntCrit, "warning": cntWarn},
        "notes": "本报告基于当前实时指标与历史事件计数，供快速评估使用",
    }
    c.JSON(200, summary)
}

func (a *API) VNCProxyWS(c *gin.Context) {
    target := c.Query("target")
    if target == "" { target = "127.0.0.1:5900" }
    conn, err := net.Dial("tcp", target)
    if err != nil { c.Status(502); return }
    defer conn.Close()
    ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
    if err != nil { return }
    defer ws.Close()
    done := make(chan struct{})
    go func() {
        buf := make([]byte, 8192)
        for {
            n, err := conn.Read(buf)
            if n > 0 {
                _ = ws.WriteMessage(websocket.BinaryMessage, buf[:n])
            }
            if err != nil { close(done); return }
        }
    }()
    go func() {
        for {
            mt, data, err := ws.ReadMessage()
            if err != nil { conn.Close(); return }
            if len(data) > 0 { _, _ = conn.Write(data) }
            _ = mt // not used distinction; always forward data
        }
    }()
    <-done
}

