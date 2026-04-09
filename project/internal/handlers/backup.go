package handlers

import (
	"database/sql"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
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
	backupDir   string
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

// NewBackupAPI creates a BackupAPI with the given backup directory.
func NewBackupAPI(database *db.DB, backupDir string) *BackupAPI {
	if backupDir == "" {
		backupDir = "data/backups"
	}
	os.MkdirAll(backupDir, 0o755)
	return &BackupAPI{
		db:          database,
		backupDir:   backupDir,
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
	api := NewBackupAPI(database, "data/backups")
	g := r.Group("/api/backup")
	{
		g.GET("/list", api.ListBackups)
		g.POST("/manual", api.ManualBackup)
		g.POST("/restore/:id", api.RestoreBackup)
		g.POST("/restore-upload", api.RestoreFromUpload)
		g.GET("/restore-progress", api.RestoreProgressAPI)
		g.DELETE("/:id", api.DeleteBackup)
		g.GET("/download/:id", api.DownloadBackup)
		g.GET("/status", api.BackupStatus)
		g.POST("/cleanup", api.CleanupOldBackups)
		g.POST("/auto-toggle", api.ToggleAutoBackup)
	}
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
	var filePath, status string
	err = b.db.QueryRow(`SELECT file_path, status FROM backup_records WHERE id = ?`, id).Scan(&filePath, &status)
	if err != nil {
		c.JSON(404, gin.H{"error": "备份记录不存在"})
		return
	}
	if status != "success" {
		c.JSON(400, gin.H{"error": "该备份状态不是 success，无法恢复"})
		return
	}

	// Safety check: file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		c.JSON(400, gin.H{"error": "备份文件不存在: " + filePath})
		return
	}

	// Read SQL content
	content, err := os.ReadFile(filePath)
	if err != nil {
		c.JSON(500, gin.H{"error": "读取备份文件失败: " + err.Error()})
		return
	}

	// Launch async restore
	go b.asyncRestore(string(content), fmt.Sprintf("从备份 #%d 恢复", id), id)

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

	// Save uploaded file to backup dir for reference
	savedPath := filepath.Join(b.backupDir, "upload_"+time.Now().Format("20060102_150405")+".sql")
	os.WriteFile(savedPath, data, 0o644)

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

// DeleteBackup removes a backup record and its file.
func (b *BackupAPI) DeleteBackup(c *gin.Context) {
	idStr := c.Param("id")
	var filePath string
	err := b.db.QueryRow(`SELECT file_path FROM backup_records WHERE id = ?`, idStr).Scan(&filePath)
	if err != nil {
		c.JSON(404, gin.H{"error": "备份记录不存在"})
		return
	}
	os.Remove(filePath)
	b.db.Exec(`DELETE FROM backup_records WHERE id = ?`, idStr)
	c.JSON(200, gin.H{"ok": true})
}

// DownloadBackup streams the SQL backup file to the client.
func (b *BackupAPI) DownloadBackup(c *gin.Context) {
	idStr := c.Param("id")
	var filePath string
	err := b.db.QueryRow(`SELECT file_path FROM backup_records WHERE id = ?`, idStr).Scan(&filePath)
	if err != nil {
		c.JSON(404, gin.H{"error": "备份记录不存在"})
		return
	}
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		c.JSON(404, gin.H{"error": "备份文件不存在"})
		return
	}
	c.FileAttachment(filePath, filepath.Base(filePath))
}

// BackupStatus returns a summary: total backups, last backup time, auto-backup status.
func (b *BackupAPI) BackupStatus(c *gin.Context) {
	var total int
	b.db.QueryRow(`SELECT COUNT(*) FROM backup_records`).Scan(&total)

	var successCount int
	b.db.QueryRow(`SELECT COUNT(*) FROM backup_records WHERE status='success'`).Scan(&successCount)

	var failCount int
	b.db.QueryRow(`SELECT COUNT(*) FROM backup_records WHERE status='failed'`).Scan(&failCount)

	var lastBackup sql.NullString
	b.db.QueryRow(`SELECT created_at FROM backup_records WHERE status='success' ORDER BY created_at DESC LIMIT 1`).Scan(&lastBackup)

	var totalSize int64
	b.db.QueryRow(`SELECT COALESCE(SUM(file_size),0) FROM backup_records WHERE status='success'`).Scan(&totalSize)

	c.JSON(200, gin.H{
		"total":        total,
		"success":      successCount,
		"failed":       failCount,
		"last_backup":  lastBackup.String,
		"total_size":   totalSize,
		"backup_dir":   b.backupDir,
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

func (b *BackupAPI) doBackup(backupType, operator, remark string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	start := time.Now()
	ts := start.Format("20060102_150405")
	filename := fmt.Sprintf("backup_%s_%s.sql", ts, backupType)
	filePath := filepath.Join(b.backupDir, filename)

	// Insert running record
	res, err := b.db.Exec(
		`INSERT INTO backup_records(backup_type, file_path, status, operator, remark) VALUES(?,?,?,?,?)`,
		backupType, filePath, "running", operator, remark,
	)
	if err != nil {
		log.Printf("[Backup] failed to insert record: %v", err)
		return
	}
	recordID, _ := res.LastInsertId()

	// Perform dump
	tableCount, rowCount, err := b.dumpToFile(filePath)
	elapsed := time.Since(start).Milliseconds()

	if err != nil {
		log.Printf("[Backup] dump failed: %v", err)
		b.db.Exec(`UPDATE backup_records SET status='failed', remark=CONCAT(remark, ' | 错误: ', ?), duration_ms=?, finished_at=NOW() WHERE id=?`,
			err.Error(), elapsed, recordID)
		// Send failure notification
		b.db.Exec(`INSERT INTO notifications(type, title, message, source) VALUES(?,?,?,?)`,
			"backup", "备份失败",
			fmt.Sprintf("[%s] %s 备份失败: %s", backupType, remark, err.Error()),
			"backup-system")
		return
	}

	// Get file size
	var fileSize int64
	if fi, err := os.Stat(filePath); err == nil {
		fileSize = fi.Size()
	}

	b.db.Exec(`UPDATE backup_records SET status='success', file_size=?, table_count=?, row_count=?, duration_ms=?, finished_at=NOW() WHERE id=?`,
		fileSize, tableCount, rowCount, elapsed, recordID)

	log.Printf("[Backup] %s completed: %d tables, %d rows, %d bytes, %dms", backupType, tableCount, rowCount, fileSize, elapsed)
}

// dumpToFile exports all user tables to a SQL file. Returns (tableCount, totalRows, error).
func (b *BackupAPI) dumpToFile(filePath string) (int, int, error) {
	raw := b.db.Raw()

	// Get list of tables
	rows, err := raw.Query("SHOW TABLES")
	if err != nil {
		return 0, 0, fmt.Errorf("SHOW TABLES: %w", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		rows.Scan(&name)
		tables = append(tables, name)
	}
	rows.Close()

	f, err := os.Create(filePath)
	if err != nil {
		return 0, 0, fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	// Header
	fmt.Fprintf(f, "-- CloudControl Database Backup\n")
	fmt.Fprintf(f, "-- Generated at: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(f, "-- Tables: %d\n", len(tables))
	fmt.Fprintf(f, "SET NAMES utf8mb4;\n")
	fmt.Fprintf(f, "SET FOREIGN_KEY_CHECKS = 0;\n\n")

	totalRows := 0
	tableCount := 0

	for _, table := range tables {
		// Get CREATE TABLE statement
		var tblName, createSQL string
		err := raw.QueryRow("SHOW CREATE TABLE `"+table+"`").Scan(&tblName, &createSQL)
		if err != nil {
			continue
		}
		tableCount++

		fmt.Fprintf(f, "-- ----------------------------\n")
		fmt.Fprintf(f, "-- Table: %s\n", table)
		fmt.Fprintf(f, "-- ----------------------------\n")
		fmt.Fprintf(f, "DROP TABLE IF EXISTS `%s`;\n", table)
		fmt.Fprintf(f, "%s;\n\n", createSQL)

		// Dump data
		dataRows, err := raw.Query("SELECT * FROM `" + table + "`")
		if err != nil {
			fmt.Fprintf(f, "-- ERROR reading data: %v\n\n", err)
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
				fmt.Fprintf(f, "INSERT INTO `%s` (`%s`) VALUES\n%s;\n",
					table, strings.Join(cols, "`, `"), strings.Join(batchValues, ",\n"))
				batchValues = batchValues[:0]
			}
		}
		dataRows.Close()

		// Flush remaining
		if len(batchValues) > 0 {
			fmt.Fprintf(f, "INSERT INTO `%s` (`%s`) VALUES\n%s;\n",
				table, strings.Join(cols, "`, `"), strings.Join(batchValues, ",\n"))
		}

		totalRows += rowCount
		fmt.Fprintf(f, "-- Rows: %d\n\n", rowCount)
	}

	fmt.Fprintf(f, "SET FOREIGN_KEY_CHECKS = 1;\n")
	fmt.Fprintf(f, "-- Backup complete: %d tables, %d rows\n", tableCount, totalRows)

	return tableCount, totalRows, nil
}

// ---------------------------------------------------------------------------
// Async restore with progress tracking
// ---------------------------------------------------------------------------

func (b *BackupAPI) asyncRestore(sqlContent, description string, backupID int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()

	// Update progress: starting — pre-restore backup phase
	b.setProgress(restoreProgress{
		Running:   true,
		Phase:     "pre_backup",
		Message:   "正在创建恢复前安全备份...",
		StartedAt: now.Format("2006-01-02 15:04:05"),
	})

	// Synchronous pre-restore safety backup (NOT in goroutine)
	b.doBackupUnlocked("pre_restore", "system", fmt.Sprintf("恢复前自动备份 (%s)", description))

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
	raw.Exec("SET FOREIGN_KEY_CHECKS = 0")

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

	raw.Exec("SET FOREIGN_KEY_CHECKS = 1")

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
	filePath := filepath.Join(b.backupDir, filename)

	res, err := b.db.Exec(
		`INSERT INTO backup_records(backup_type, file_path, status, operator, remark) VALUES(?,?,?,?,?)`,
		backupType, filePath, "running", operator, remark,
	)
	if err != nil {
		log.Printf("[Backup] failed to insert record: %v", err)
		return
	}
	recordID, _ := res.LastInsertId()

	tableCount, rowCount, err := b.dumpToFile(filePath)
	elapsed := time.Since(start).Milliseconds()

	if err != nil {
		log.Printf("[Backup] dump failed: %v", err)
		b.db.Exec(`UPDATE backup_records SET status='failed', remark=CONCAT(remark, ' | 错误: ', ?), duration_ms=?, finished_at=NOW() WHERE id=?`,
			err.Error(), elapsed, recordID)
		return
	}

	var fileSize int64
	if fi, err := os.Stat(filePath); err == nil {
		fileSize = fi.Size()
	}
	b.db.Exec(`UPDATE backup_records SET status='success', file_size=?, table_count=?, row_count=?, duration_ms=?, finished_at=NOW() WHERE id=?`,
		fileSize, tableCount, rowCount, elapsed, recordID)
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
	rows, err := b.db.Query(`SELECT id, file_path FROM backup_records WHERE status='success' ORDER BY created_at DESC`)
	if err != nil {
		return 0
	}
	defer rows.Close()

	deleted := 0
	idx := 0
	for rows.Next() {
		var id int
		var filePath string
		rows.Scan(&id, &filePath)
		idx++
		if idx > maxKeep {
			os.Remove(filePath)
			b.db.Exec(`DELETE FROM backup_records WHERE id = ?`, id)
			deleted++
		}
	}

	b.db.Exec(`DELETE FROM backup_records WHERE status='failed' AND created_at < DATE_SUB(NOW(), INTERVAL 7 DAY)`)

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
