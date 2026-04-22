package handlers

import (
	"database/sql"
	"fmt"
	"io"
	"log"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"smartcontrol/internal/db"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// BackupAPI — 备份与数据回滚
// ---------------------------------------------------------------------------

// restoreProgress tracks async restore operation status for frontend polling.
type restoreProgress struct {
	Running    bool   `json:"running"`
	Phase      string `json:"phase"`
	Total      int    `json:"total"`
	Done       int    `json:"done"`
	Errors     int    `json:"errors"`
	Message    string `json:"message"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
}

// BackupAPI holds database reference, auto-backup state and restore progress.
type BackupAPI struct {
	db          *db.DB
	mu          sync.Mutex // serialise backup / restore operations
	autoEnabled int32      // atomic: 1 = on, 0 = off
	autoStop    chan struct{}
	autoOnce    sync.Once
	maxKeep     int
	interval    time.Duration
	// restore progress (latest)
	rpMu      sync.RWMutex
	rpCurrent restoreProgress
}

// NewBackupAPI creates a BackupAPI with DB-backed storage.
func NewBackupAPI(database *db.DB) *BackupAPI {
	return &BackupAPI{
		db:          database,
		autoEnabled: 1,
		autoStop:    make(chan struct{}),
		maxKeep:     10,
		interval:    24 * time.Hour,
	}
}

// ---------------------------------------------------------------------------
// Route registration
// ---------------------------------------------------------------------------

// RegisterBackupRoutes wires all backup/restore endpoints.
func RegisterBackupRoutes(r *gin.Engine, database *db.DB) *BackupAPI {
	api := NewBackupAPI(database)
	g := r.Group("/api/backup")
	{
		g.GET("/list", api.ListBackups)
		g.POST("/manual", api.ManualBackup)
		g.POST("/restore/:id", api.RestoreBackup)
		g.POST("/restore-upload", api.RestoreFromUpload)
		g.GET("/restore-progress", api.RestoreProgressAPI)
		g.DELETE("/:id", api.DeleteBackup)
		g.POST("/batch-delete", api.BatchDeleteBackups)
		g.GET("/download/:id", api.DownloadBackup)
		g.GET("/status", api.BackupStatus)
		g.POST("/cleanup", api.CleanupOldBackups)
		g.POST("/auto-toggle", api.ToggleAutoBackup)
	}
	// Start background cleaner for stale "running" backups
	api.startStaleBackupCleaner()
	return api
}

// ---------------------------------------------------------------------------
// Auto-backup goroutine (with on/off toggle)
// ---------------------------------------------------------------------------

// StartAutoBackup launches a background goroutine that performs periodic backups.
func (b *BackupAPI) StartAutoBackup(interval time.Duration, maxKeep int) {
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	if maxKeep <= 0 {
		maxKeep = 10
	}
	b.interval = interval
	b.maxKeep = maxKeep
	atomic.StoreInt32(&b.autoEnabled, 1)

	go func() {
		// Initial backup 30s after startup
		time.Sleep(30 * time.Second)
		if atomic.LoadInt32(&b.autoEnabled) == 1 {
			b.doBackup("auto", "system", "自动备份（启动后首次）")
			b.pruneOldBackups(b.maxKeep)
		}

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if atomic.LoadInt32(&b.autoEnabled) == 1 {
					b.doBackup("auto", "system", "定时自动备份")
					b.pruneOldBackups(b.maxKeep)
				}
			case <-b.autoStop:
				return
			}
		}
	}()
	log.Printf("[Backup] Auto-backup started: interval=%v, maxKeep=%d", interval, maxKeep)
}

// ToggleAutoBackup turns auto-backup on or off.
func (b *BackupAPI) ToggleAutoBackup(c *gin.Context) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "invalid request"})
		return
	}
	if req.Enabled {
		atomic.StoreInt32(&b.autoEnabled, 1)
		log.Println("[Backup] Auto-backup enabled")
	} else {
		atomic.StoreInt32(&b.autoEnabled, 0)
		log.Println("[Backup] Auto-backup disabled")
	}
	c.JSON(200, gin.H{"ok": true, "auto_enabled": req.Enabled})
}

// ---------------------------------------------------------------------------
// API handlers
// ---------------------------------------------------------------------------

// ListBackups returns all backup records ordered by creation time desc.
func (b *BackupAPI) ListBackups(c *gin.Context) {
	rows, err := b.db.Query(`SELECT id, backup_type, file_path, file_size, table_count, row_count, status, operator, remark, duration_ms, created_at, finished_at FROM backup_records ORDER BY created_at DESC`)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	items := []gin.H{}
	for rows.Next() {
		var id, tableCount, rowCount, durationMs int
		var fileSize int64
		var bType, filePath, status, operator, remark string
		var createdAt, finishedAt sql.NullString
		if err := rows.Scan(&id, &bType, &filePath, &fileSize, &tableCount, &rowCount, &status, &operator, &remark, &durationMs, &createdAt, &finishedAt); err != nil {
			continue
		}
		items = append(items, gin.H{
			"id":          id,
			"backup_type": bType,
			"file_path":   filePath,
			"file_size":   fileSize,
			"table_count": tableCount,
			"row_count":   rowCount,
			"status":      status,
			"operator":    operator,
			"remark":      remark,
			"duration_ms": durationMs,
			"created_at":  createdAt.String,
			"finished_at": finishedAt.String,
		})
	}
	c.JSON(200, gin.H{"items": items})
}

// ManualBackup triggers a manual backup.
func (b *BackupAPI) ManualBackup(c *gin.Context) {
	var req struct {
		Operator string `json:"operator"`
		Remark   string `json:"remark"`
	}
	c.BindJSON(&req)
	if req.Operator == "" {
		req.Operator = "user"
	}
	if req.Remark == "" {
		req.Remark = "手动备份"
	}

	go func() {
		b.doBackup("manual", req.Operator, req.Remark)
	}()

	c.JSON(200, gin.H{"ok": true, "message": "备份任务已提交"})
}

// RestoreBackup restores data from a specific backup record (async with progress).
func (b *BackupAPI) RestoreBackup(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid id"})
		return
	}

	// Check if restore already in progress
	b.rpMu.RLock()
	running := b.rpCurrent.Running
	b.rpMu.RUnlock()
	if running {
		c.JSON(409, gin.H{"error": "已有恢复任务正在进行中，请等待完成"})
		return
	}

	// Fetch backup record
	var sqlContent sql.NullString
	var status string
	err = b.db.QueryRow(`SELECT status, sql_content FROM backup_records WHERE id = ?`, id).Scan(&status, &sqlContent)
	if err != nil {
		c.JSON(404, gin.H{"error": "备份记录不存在"})
		return
	}
	if status != "success" {
		c.JSON(400, gin.H{"error": "该备份状态不是 success，无法恢复"})
		return
	}
	if !sqlContent.Valid || sqlContent.String == "" {
		c.JSON(400, gin.H{"error": "备份数据为空"})
		return
	}

	// Launch async restore
	go b.asyncRestore(sqlContent.String, fmt.Sprintf("从备份 #%d 恢复", id), id)

	c.JSON(200, gin.H{"ok": true, "message": "恢复任务已启动，请通过进度接口查看状态"})
}

// RestoreFromUpload restores data from an uploaded SQL file (async with progress).
func (b *BackupAPI) RestoreFromUpload(c *gin.Context) {
	// Check if restore already in progress
	b.rpMu.RLock()
	running := b.rpCurrent.Running
	b.rpMu.RUnlock()
	if running {
		c.JSON(409, gin.H{"error": "已有恢复任务正在进行中，请等待完成"})
		return
	}

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(400, gin.H{"error": "请上传 SQL 备份文件"})
		return
	}
	defer file.Close()

	if !strings.HasSuffix(strings.ToLower(header.Filename), ".sql") {
		c.JSON(400, gin.H{"error": "仅支持 .sql 文件"})
		return
	}

	data, err := io.ReadAll(file)
	if err != nil {
		c.JSON(500, gin.H{"error": "读取上传文件失败: " + err.Error()})
		return
	}
	if len(data) == 0 {
		c.JSON(400, gin.H{"error": "上传文件内容为空"})
		return
	}

	// Launch async restore
	go b.asyncRestore(string(data), fmt.Sprintf("从上传文件恢复 (%s)", header.Filename), 0)

	c.JSON(200, gin.H{"ok": true, "message": "恢复任务已启动，请通过进度接口查看状态"})
}

// RestoreProgressAPI returns current restore progress for frontend polling.
func (b *BackupAPI) RestoreProgressAPI(c *gin.Context) {
	b.rpMu.RLock()
	defer b.rpMu.RUnlock()
	c.JSON(200, b.rpCurrent)
}

// DeleteBackup removes a backup record from the database.
func (b *BackupAPI) DeleteBackup(c *gin.Context) {
	idStr := c.Param("id")
	result, err := b.db.Exec(`DELETE FROM backup_records WHERE id = ?`, idStr)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	if n, _ := result.RowsAffected(); n == 0 {
		c.JSON(404, gin.H{"error": "备份记录不存在"})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// BatchDeleteBackups removes multiple backup records by their IDs.
func (b *BackupAPI) BatchDeleteBackups(c *gin.Context) {
	var req struct {
		IDs []int `json:"ids"`
	}
	if err := c.BindJSON(&req); err != nil || len(req.IDs) == 0 {
		c.JSON(400, gin.H{"error": "请提供要删除的备份ID列表"})
		return
	}
	deleted := 0
	for _, id := range req.IDs {
		result, err := b.db.Exec(`DELETE FROM backup_records WHERE id = ?`, id)
		if err == nil {
			if n, _ := result.RowsAffected(); n > 0 {
				deleted++
			}
		}
	}
	c.JSON(200, gin.H{"ok": true, "deleted": deleted})
}

// DownloadBackup streams the SQL backup content from DB to the client.
func (b *BackupAPI) DownloadBackup(c *gin.Context) {
	idStr := c.Param("id")
	var fileName string
	var sqlContent sql.NullString
	err := b.db.QueryRow(`SELECT file_path, sql_content FROM backup_records WHERE id = ?`, idStr).Scan(&fileName, &sqlContent)
	if err != nil {
		c.JSON(404, gin.H{"error": "备份记录不存在"})
		return
	}
	if !sqlContent.Valid || sqlContent.String == "" {
		c.JSON(404, gin.H{"error": "备份数据为空"})
		return
	}
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, fileName))
	c.Data(200, "application/sql", []byte(sqlContent.String))
}

// BackupStatus returns a summary: total backups, last backup time, auto-backup status.
func (b *BackupAPI) BackupStatus(c *gin.Context) {
	var successCount int
	b.db.QueryRow(`SELECT COUNT(*) FROM backup_records WHERE status='success'`).Scan(&successCount)

	var failCount int
	b.db.QueryRow(`SELECT COUNT(*) FROM backup_records WHERE status IN ('failed','timeout_failed')`).Scan(&failCount)

	var runningCount int
	b.db.QueryRow(`SELECT COUNT(*) FROM backup_records WHERE status='running'`).Scan(&runningCount)

	// total = success + failed + running so stats are always consistent
	total := successCount + failCount + runningCount

	var lastBackup sql.NullString
	b.db.QueryRow(`SELECT created_at FROM backup_records WHERE status='success' ORDER BY created_at DESC LIMIT 1`).Scan(&lastBackup)

	var totalSize int64
	b.db.QueryRow(`SELECT COALESCE(SUM(file_size),0) FROM backup_records WHERE status='success'`).Scan(&totalSize)

	c.JSON(200, gin.H{
		"total":        total,
		"success":      successCount,
		"failed":       failCount,
		"running":      runningCount,
		"last_backup":  lastBackup.String,
		"total_size":   totalSize,
		"storage":      "database",
		"auto_enabled": atomic.LoadInt32(&b.autoEnabled) == 1,
	})
}

// CleanupOldBackups manually triggers cleanup of old backups (keep latest N).
func (b *BackupAPI) CleanupOldBackups(c *gin.Context) {
	var req struct {
		Keep int `json:"keep"`
	}
	c.BindJSON(&req)
	if req.Keep <= 0 {
		req.Keep = 10
	}
	deleted := b.pruneOldBackups(req.Keep)
	c.JSON(200, gin.H{"ok": true, "deleted": deleted})
}

// ---------------------------------------------------------------------------
// Core backup logic (pure-Go SQL dump)
// ---------------------------------------------------------------------------

// cleanupStaleBackups marks "running" backups older than 1 hour as "timeout_failed".
// This prevents tasks from being stuck in "running" status forever.
func (b *BackupAPI) cleanupStaleBackups() {
	result, err := b.db.Exec(
		`UPDATE backup_records SET status='timeout_failed', remark=COALESCE(remark,'') || ' | 超时自动标记失败' WHERE status='running' AND created_at < datetime('now', '-1 hour')`,
	)
	if err != nil {
		log.Printf("[Backup] stale cleanup query error: %v", err)
		return
	}
	affected, _ := result.RowsAffected()
	if affected > 0 {
		log.Printf("[Backup] cleaned up %d stale running backup(s)", affected)
	}
}

// startStaleBackupCleaner launches a background goroutine that periodically
// checks for and cleans up stale "running" backups.
func (b *BackupAPI) startStaleBackupCleaner() {
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		// Run once at startup
		b.cleanupStaleBackups()
		for range ticker.C {
			b.cleanupStaleBackups()
		}
	}()
}

func (b *BackupAPI) doBackup(backupType, operator, remark string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	start := time.Now()
	ts := start.Format("20060102_150405")
	filename := fmt.Sprintf("backup_%s_%s.sql", ts, backupType)

	// Insert running record
	res, err := b.db.Exec(
		`INSERT INTO backup_records(backup_type, file_path, status, operator, remark) VALUES(?,?,?,?,?)`,
		backupType, filename, "running", operator, remark,
	)
	if err != nil {
		log.Printf("[Backup] failed to insert record: %v", err)
		return
	}
	recordID, _ := res.LastInsertId()

	// Panic recovery: ensure backup status is updated even if a panic occurs
	defer func() {
		if r := recover(); r != nil {
			elapsed := time.Since(start).Milliseconds()
			log.Printf("[Backup] PANIC recovered in doBackup: %v", r)
			b.db.Exec(`UPDATE backup_records SET status='failed', remark=COALESCE(remark,'') || ' | panic: ' || ?, duration_ms=? WHERE id=?`,
				fmt.Sprintf("%v", r), elapsed, recordID)
			b.db.Exec(`INSERT INTO notifications(type, title, message, source) VALUES(?,?,?,?)`,
				"backup", "备份异常崩溃",
				fmt.Sprintf("[%s] %s 备份因异常崩溃而失败: %v", backupType, remark, r),
				"backup-system")
		}
	}()

	// Perform dump to string
	sqlContent, tableCount, rowCount, err := b.dumpToString()
	elapsed := time.Since(start).Milliseconds()

	if err != nil {
		log.Printf("[Backup] dump failed: %v", err)
		b.db.Exec(`UPDATE backup_records SET status='failed', remark=COALESCE(remark,'') || ' | 错误: ' || ?, duration_ms=?, finished_at=datetime('now') WHERE id=?`,
			err.Error(), elapsed, recordID)
		// Send failure notification
		b.db.Exec(`INSERT INTO notifications(type, title, message, source) VALUES(?,?,?,?)`,
			"backup", "备份失败",
			fmt.Sprintf("[%s] %s 备份失败: %s", backupType, remark, err.Error()),
			"backup-system")
		return
	}

	fileSize := int64(len(sqlContent))

	b.db.Exec(`UPDATE backup_records SET status='success', file_size=?, table_count=?, row_count=?, duration_ms=?, sql_content=?, finished_at=datetime('now') WHERE id=?`,
		fileSize, tableCount, rowCount, elapsed, sqlContent, recordID)

	log.Printf("[Backup] %s completed: %d tables, %d rows, %d bytes, %dms", backupType, tableCount, rowCount, fileSize, elapsed)
}

// dumpToString exports all user tables to a SQL string stored in DB. Returns (sqlContent, tableCount, totalRows, error).
func (b *BackupAPI) dumpToString() (string, int, int, error) {
	raw := b.db.Raw()

	// Get list of tables (SQLite compatible)
	rows, err := raw.Query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		return "", 0, 0, fmt.Errorf("query sqlite_master: %w", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		rows.Scan(&name)
		tables = append(tables, name)
	}
	rows.Close()

	var buf strings.Builder

	// Header
	fmt.Fprintf(&buf, "-- 云翼智控 Database Backup\n")
	fmt.Fprintf(&buf, "-- Generated at: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&buf, "-- Tables: %d\n", len(tables))
	fmt.Fprintf(&buf, "PRAGMA foreign_keys = OFF;\n\n")

	totalRows := 0
	tableCount := 0

	for _, table := range tables {
		// Get CREATE TABLE statement
		var createSQL string
		err := raw.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&createSQL)
		if err != nil {
			continue
		}
		tableCount++

		fmt.Fprintf(&buf, "-- ----------------------------\n")
		fmt.Fprintf(&buf, "-- Table: %s\n", table)
		fmt.Fprintf(&buf, "-- ----------------------------\n")
		fmt.Fprintf(&buf, "DROP TABLE IF EXISTS `%s`;\n", table)
		fmt.Fprintf(&buf, "%s;\n\n", createSQL)

		// Dump data
		dataRows, err := raw.Query("SELECT * FROM `" + table + "`")
		if err != nil {
			fmt.Fprintf(&buf, "-- ERROR reading data: %v\n\n", err)
			continue
		}

		cols, _ := dataRows.Columns()
		if len(cols) == 0 {
			dataRows.Close()
			continue
		}

		values := make([]interface{}, len(cols))
		valuePtrs := make([]interface{}, len(cols))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		rowCount := 0
		batchValues := []string{}

		for dataRows.Next() {
			dataRows.Scan(valuePtrs...)
			rowVals := make([]string, len(cols))
			for i, v := range values {
				if v == nil {
					rowVals[i] = "NULL"
				} else {
					switch val := v.(type) {
					case []byte:
						rowVals[i] = "'" + escapeSQLString(string(val)) + "'"
					case string:
						rowVals[i] = "'" + escapeSQLString(val) + "'"
					case time.Time:
						rowVals[i] = "'" + val.Format("2006-01-02 15:04:05") + "'"
					case int64:
						rowVals[i] = strconv.FormatInt(val, 10)
					case float64:
						rowVals[i] = strconv.FormatFloat(val, 'f', -1, 64)
					case bool:
						if val {
							rowVals[i] = "1"
						} else {
							rowVals[i] = "0"
						}
					default:
						rowVals[i] = "'" + escapeSQLString(fmt.Sprintf("%v", val)) + "'"
					}
				}
			}
			batchValues = append(batchValues, "("+strings.Join(rowVals, ", ")+")")
			rowCount++

			// Flush every 500 rows
			if len(batchValues) >= 500 {
				fmt.Fprintf(&buf, "INSERT INTO `%s` (`%s`) VALUES\n%s;\n",
					table, strings.Join(cols, "`, `"), strings.Join(batchValues, ",\n"))
				batchValues = batchValues[:0]
			}
		}
		dataRows.Close()

		// Flush remaining
		if len(batchValues) > 0 {
			fmt.Fprintf(&buf, "INSERT INTO `%s` (`%s`) VALUES\n%s;\n",
				table, strings.Join(cols, "`, `"), strings.Join(batchValues, ",\n"))
		}

		totalRows += rowCount
		fmt.Fprintf(&buf, "-- Rows: %d\n\n", rowCount)
	}

	fmt.Fprintf(&buf, "PRAGMA foreign_keys = ON;\n")
	fmt.Fprintf(&buf, "-- Backup complete: %d tables, %d rows\n", tableCount, totalRows)

	return buf.String(), tableCount, totalRows, nil
}

// ---------------------------------------------------------------------------
// Async restore with progress tracking
// ---------------------------------------------------------------------------

func (b *BackupAPI) asyncRestore(sqlContent, description string, backupID int) {
	now := time.Now()

	// Bug 18: panic recover to prevent goroutine crash
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[Restore] PANIC recovered in asyncRestore: %v", r)
			b.setProgress(restoreProgress{
				Running:    false,
				Phase:      "done",
				Errors:     1,
				Message:    fmt.Sprintf("恢复因异常崩溃而失败: %v", r),
				StartedAt:  now.Format("2006-01-02 15:04:05"),
				FinishedAt: time.Now().Format("2006-01-02 15:04:05"),
			})
			b.db.Exec(`INSERT INTO notifications(type, title, message, source) VALUES(?,?,?,?)`,
				"backup", "数据恢复异常崩溃",
				fmt.Sprintf("恢复操作因异常崩溃而失败: %v", r),
				"backup-system")
		}
	}()

	// Bug 19: acquire lock only for the critical DB section (pre-backup)
	b.mu.Lock()

	// Update progress: starting — pre-restore backup phase
	b.setProgress(restoreProgress{
		Running:   true,
		Phase:     "pre_backup",
		Message:   "正在创建恢复前安全备份...",
		StartedAt: now.Format("2006-01-02 15:04:05"),
	})

	// Synchronous pre-restore safety backup (NOT in goroutine)
	b.doBackupUnlocked("pre_restore", "system", fmt.Sprintf("恢复前自动备份 (%s)", description))
	b.mu.Unlock() // Bug 19: release lock after pre-backup; restore loop does not need global lock

	// Parse statements
	b.setProgress(restoreProgress{
		Running:   true,
		Phase:     "parsing",
		Message:   "正在解析 SQL 语句...",
		StartedAt: now.Format("2006-01-02 15:04:05"),
	})

	statements := splitSQLStatements(sqlContent)

	// Filter out backup_records related statements to preserve backup history
	filtered := filterRestoreStatements(statements)

	total := len(filtered)
	b.setProgress(restoreProgress{
		Running:   true,
		Phase:     "restoring",
		Total:     total,
		Done:      0,
		Message:   fmt.Sprintf("正在恢复数据 (共 %d 条语句)...", total),
		StartedAt: now.Format("2006-01-02 15:04:05"),
	})

	// Execute restore
	raw := b.db.Raw()
	raw.Exec("PRAGMA foreign_keys = OFF")

	errCount := 0
	done := 0
	for _, stmt := range filtered {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := raw.Exec(stmt); err != nil {
			log.Printf("[Restore] statement error: %v\n  SQL: %.200s", err, stmt)
			errCount++
		}
		done++
		// Update progress every 10 statements
		if done%10 == 0 || done == total {
			b.setProgress(restoreProgress{
				Running:   true,
				Phase:     "restoring",
				Total:     total,
				Done:      done,
				Errors:    errCount,
				Message:   fmt.Sprintf("已执行 %d/%d 条语句...", done, total),
				StartedAt: now.Format("2006-01-02 15:04:05"),
			})
		}
	}

	raw.Exec("PRAGMA foreign_keys = ON")

	elapsed := time.Since(now)
	// Re-run migration to ensure all tables exist after restore
	db.Migrate(b.db)

	// Final status
	var finalMsg string
	if errCount == 0 {
		finalMsg = fmt.Sprintf("恢复成功！共执行 %d 条语句，耗时 %dms", done, elapsed.Milliseconds())
	} else {
		finalMsg = fmt.Sprintf("恢复完成，共执行 %d 条语句，%d 条失败，耗时 %dms", done, errCount, elapsed.Milliseconds())
	}

	b.setProgress(restoreProgress{
		Running:    false,
		Phase:      "done",
		Total:      total,
		Done:       done,
		Errors:     errCount,
		Message:    finalMsg,
		StartedAt:  now.Format("2006-01-02 15:04:05"),
		FinishedAt: time.Now().Format("2006-01-02 15:04:05"),
	})

	// Write notification
	nType := "backup"
	nTitle := "数据恢复成功"
	if errCount > 0 {
		nTitle = "数据恢复完成（有错误）"
	}
	b.db.Exec(`INSERT INTO notifications(type, title, message, source) VALUES(?,?,?,?)`,
		nType, nTitle, finalMsg, "backup-system")

	log.Printf("[Restore] %s", finalMsg)
}

// doBackupUnlocked performs a backup without acquiring b.mu (caller must hold lock).
func (b *BackupAPI) doBackupUnlocked(backupType, operator, remark string) {
	start := time.Now()
	ts := start.Format("20060102_150405")
	filename := fmt.Sprintf("backup_%s_%s.sql", ts, backupType)

	res, err := b.db.Exec(
		`INSERT INTO backup_records(backup_type, file_path, status, operator, remark) VALUES(?,?,?,?,?)`,
		backupType, filename, "running", operator, remark,
	)
	if err != nil {
		log.Printf("[Backup] failed to insert record: %v", err)
		return
	}
	recordID, _ := res.LastInsertId()

	sqlContent, tableCount, rowCount, err := b.dumpToString()
	elapsed := time.Since(start).Milliseconds()

	if err != nil {
		log.Printf("[Backup] dump failed: %v", err)
		b.db.Exec(`UPDATE backup_records SET status='failed', remark=COALESCE(remark,'') || ' | 错误: ' || ?, duration_ms=?, finished_at=datetime('now') WHERE id=?`,
			err.Error(), elapsed, recordID)
		return
	}

	fileSize := int64(len(sqlContent))
	b.db.Exec(`UPDATE backup_records SET status='success', file_size=?, table_count=?, row_count=?, duration_ms=?, sql_content=?, finished_at=datetime('now') WHERE id=?`,
		fileSize, tableCount, rowCount, elapsed, sqlContent, recordID)
	log.Printf("[Backup] %s completed: %d tables, %d rows, %d bytes, %dms", backupType, tableCount, rowCount, fileSize, elapsed)
}

func (b *BackupAPI) setProgress(p restoreProgress) {
	b.rpMu.Lock()
	b.rpCurrent = p
	b.rpMu.Unlock()
}

// filterRestoreStatements removes statements that would drop/recreate the backup_records table
// so that backup history is preserved across restores.
func filterRestoreStatements(stmts []string) []string {
	out := make([]string, 0, len(stmts))
	skipInsert := false
	for _, s := range stmts {
		lower := strings.ToLower(strings.TrimSpace(s))
		// Skip DROP/CREATE for backup_records
		if strings.Contains(lower, "backup_records") &&
			(strings.HasPrefix(lower, "drop table") || strings.HasPrefix(lower, "create table") || strings.HasPrefix(lower, "insert into")) {
			if strings.HasPrefix(lower, "drop table") || strings.HasPrefix(lower, "create table") {
				skipInsert = true
			}
			continue
		}
		// Also skip INSERT into backup_records after its CREATE
		if skipInsert && strings.HasPrefix(lower, "insert into `backup_records`") {
			continue
		}
		if !strings.Contains(lower, "backup_records") {
			skipInsert = false
		}
		out = append(out, s)
	}
	return out
}

// ---------------------------------------------------------------------------
// Cleanup / prune
// ---------------------------------------------------------------------------

func (b *BackupAPI) pruneOldBackups(maxKeep int) int {
	rows, err := b.db.Query(`SELECT id FROM backup_records WHERE status='success' ORDER BY created_at DESC`)
	if err != nil {
		return 0
	}
	defer rows.Close()

	deleted := 0
	idx := 0
	for rows.Next() {
		var id int
		rows.Scan(&id)
		idx++
		if idx > maxKeep {
			b.db.Exec(`DELETE FROM backup_records WHERE id = ?`, id)
			deleted++
		}
	}

	// Clean up failed / timeout_failed / stale running records older than 7 days (SQLite syntax)
	res, err := b.db.Exec(`DELETE FROM backup_records WHERE status IN ('failed','timeout_failed','running') AND created_at < datetime('now', '-7 days')`)
	if err == nil {
		if n, _ := res.RowsAffected(); n > 0 {
			deleted += int(n)
		}
	}

	if deleted > 0 {
		log.Printf("[Backup] Pruned %d old backups (keeping latest %d)", deleted, maxKeep)
	}
	return deleted
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func escapeSQLString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	s = strings.ReplaceAll(s, "\x00", `\0`)
	s = strings.ReplaceAll(s, "\x1a", `\Z`)
	return s
}

func splitSQLStatements(content string) []string {
	var stmts []string
	var current strings.Builder
	inString := false
	escape := false
	var quoteChar byte

	for i := 0; i < len(content); i++ {
		ch := content[i]

		if escape {
			current.WriteByte(ch)
			escape = false
			continue
		}

		if ch == '\\' && inString {
			current.WriteByte(ch)
			escape = true
			continue
		}

		if inString {
			current.WriteByte(ch)
			if ch == quoteChar {
				inString = false
			}
			continue
		}

		if ch == '\'' || ch == '"' {
			inString = true
			quoteChar = ch
			current.WriteByte(ch)
			continue
		}

		// Skip line comments
		if ch == '-' && i+1 < len(content) && content[i+1] == '-' {
			for i < len(content) && content[i] != '\n' {
				i++
			}
			continue
		}

		if ch == ';' {
			s := strings.TrimSpace(current.String())
			if s != "" {
				stmts = append(stmts, s)
			}
			current.Reset()
			continue
		}

		current.WriteByte(ch)
	}

	s := strings.TrimSpace(current.String())
	if s != "" {
		stmts = append(stmts, s)
	}

	return stmts
}
