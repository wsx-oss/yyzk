package syncengine

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"smartcontrol/internal/db"
)

// Tables that can be synced between devices
var SyncableTables = []string{
	"hardware_items",
	"updates",
	"recordings",
	"logs",
	"alerts",
	"video_sources",
	"devices",
	"drones",
	"battery_records",
	"battery_alerts",
	"gps_devices",
	"gps_history",
	"gps_fence_alerts",
	"flight_missions",
	"mission_logs",
	"perf_reports",
	"flight_plans",
	"user_stats",
}

// TableData holds exported rows for one table
type TableData struct {
	Table   string                   `json:"table"`
	Columns []string                 `json:"columns"`
	Rows    []map[string]interface{} `json:"rows"`
	Count   int                      `json:"count"`
}

// ExportPayload is the full export from a device
type ExportPayload struct {
	Tables     []TableData `json:"tables"`
	ExportedAt string      `json:"exported_at"`
	DeviceIP   string      `json:"device_ip"`
}

// TaskState tracks a running sync goroutine
type TaskState struct {
	Cancel       chan struct{}
	Running      bool
	Progress     int // 0-100
	Message      string
	SyncedTables int
	TotalTables  int
	LastError    string
	StartedAt    time.Time
	EndTime      string // task end time (local time string "2006-01-02 15:04")
}

// Engine manages all sync tasks
type Engine struct {
	mu    sync.RWMutex
	tasks map[int]*TaskState // taskID -> state
	db    *db.DB
}

// New creates a new sync engine
func New(db *db.DB) *Engine {
	return &Engine{
		tasks: make(map[int]*TaskState),
		db:    db,
	}
}

// IsRunning checks if a task is currently running
func (e *Engine) IsRunning(taskID int) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	s, ok := e.tasks[taskID]
	return ok && s.Running
}

// GetState returns the current state of a task
func (e *Engine) GetState(taskID int) *TaskState {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.tasks[taskID]
}

// StartTask begins a sync task in a goroutine
func (e *Engine) StartTask(taskID int, source, target, mode, frequency, endTime string) error {
	e.mu.Lock()
	if s, ok := e.tasks[taskID]; ok && s.Running {
		e.mu.Unlock()
		return fmt.Errorf("任务 %d 已在运行中", taskID)
	}
	state := &TaskState{
		Cancel:      make(chan struct{}),
		Running:     true,
		Progress:    0,
		Message:     "准备同步...",
		TotalTables: len(SyncableTables),
		StartedAt:   time.Now(),
		EndTime:     endTime,
	}
	e.tasks[taskID] = state
	e.mu.Unlock()

	go e.runSync(taskID, source, target, mode, frequency, state)
	return nil
}

// StopTask stops a running sync task
func (e *Engine) StopTask(taskID int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if s, ok := e.tasks[taskID]; ok && s.Running {
		close(s.Cancel)
		s.Running = false
		s.Message = "已停止"
	}
}

// StopAll stops all running tasks
func (e *Engine) StopAll() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	stopped := 0
	for _, s := range e.tasks {
		if s.Running {
			close(s.Cancel)
			s.Running = false
			s.Message = "已停止"
			stopped++
		}
	}
	return stopped
}

// CheckIP verifies that a device is reachable and running smartcontrol.
// It tries /api/sync/ping first (whitelisted in auth middleware), then
// falls back to /api/healthz for compatibility with older versions.
func CheckIP(ip string) error {
	client := &http.Client{Timeout: 5 * time.Second}

	// Try multiple endpoints for robustness
	endpoints := []string{
		fmt.Sprintf("http://%s:8080/api/sync/ping", ip),
		fmt.Sprintf("http://%s:8080/api/healthz", ip),
	}

	var lastErr error
	for _, url := range endpoints {
		resp, err := client.Get(url)
		if err != nil {
			lastErr = fmt.Errorf("无法连接到 %s:8080，请确保目标设备已运行smartcontrol程序且8080端口已放行", ip)
			continue
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			lastErr = fmt.Errorf("目标设备 %s 响应异常 (HTTP %d)", ip, resp.StatusCode)
			continue
		}
		if readErr != nil {
			lastErr = fmt.Errorf("读取目标设备 %s 响应失败", ip)
			continue
		}
		// Try to parse as JSON with "ok" field (sync/ping response)
		var result struct {
			OK     bool   `json:"ok"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal(body, &result); err == nil {
			if result.OK || result.Status == "ok" {
				return nil // Device is reachable and running smartcontrol
			}
		}
		lastErr = fmt.Errorf("目标设备 %s 不是有效的smartcontrol实例", ip)
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("无法验证设备 %s 的连通性", ip)
}

// runSync is the main sync loop for a task
func (e *Engine) runSync(taskID int, source, target, mode, frequency string, state *TaskState) {
	defer func() {
		e.mu.Lock()
		state.Running = false
		e.mu.Unlock()
	}()

	interval := parseFrequency(frequency)

	for {
		// Perform one sync cycle
		e.doSyncCycle(taskID, source, target, mode, state)

		// Update DB with progress
		e.updateTaskDB(taskID, state)

		// Check if end_time has been reached after completing this cycle
		if state.EndTime != "" {
			if endT, err := parseLocalTime(state.EndTime); err == nil {
				if time.Now().After(endT) {
					state.Message = "已到达结束时间，自动停止"
					e.setTaskStatus(taskID, "已停止")
					log.Printf("[SyncEngine] Task %d: auto-stopped, end_time %s reached", taskID, state.EndTime)
					return
				}
			}
		}

		// Wait for next cycle or cancellation
		select {
		case <-state.Cancel:
			e.setTaskStatus(taskID, "已停止")
			return
		case <-time.After(interval):
			// Check if still supposed to run
			e.mu.RLock()
			running := state.Running
			e.mu.RUnlock()
			if !running {
				return
			}
		}
	}
}

// parseLocalTime parses a time string in common local formats
func parseLocalTime(s string) (time.Time, error) {
	formats := []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
	}
	for _, f := range formats {
		if t, err := time.ParseInLocation(f, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("无法解析时间: %s", s)
}

func (e *Engine) doSyncCycle(taskID int, source, target, mode string, state *TaskState) {
	state.SyncedTables = 0
	state.Progress = 0
	state.Message = "正在从数据源获取数据..."

	// Step 1: Export data from source (remote device)
	exportURL := fmt.Sprintf("http://%s:8080/api/sync/export-data", source)
	if mode == "增量同步" {
		var lastSynced sql.NullString
		_ = e.db.QueryRow("SELECT last_synced_at FROM sync_tasks WHERE id = ?", taskID).Scan(&lastSynced)
		if lastSynced.Valid && lastSynced.String != "" {
			exportURL += "?since=" + lastSynced.String
		}
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(exportURL)
	if err != nil {
		state.LastError = "连接数据源失败: " + err.Error()
		state.Message = "同步失败: 无法连接数据源"
		log.Printf("[SyncEngine] Task %d: export from %s failed: %v", taskID, source, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		state.LastError = fmt.Sprintf("数据源返回错误 (HTTP %d)", resp.StatusCode)
		state.Message = "同步失败: 数据源响应异常"
		log.Printf("[SyncEngine] Task %d: export from %s returned HTTP %d", taskID, source, resp.StatusCode)
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		state.LastError = "读取数据源响应失败: " + err.Error()
		state.Message = "同步失败: 读取数据失败"
		return
	}

	var payload ExportPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		state.LastError = "解析数据源响应失败: " + err.Error()
		state.Message = "同步失败: 数据格式错误"
		log.Printf("[SyncEngine] Task %d: unmarshal failed: %v, body: %s", taskID, err, string(body[:min(len(body), 200)]))
		return
	}

	state.TotalTables = len(payload.Tables)
	if state.TotalTables == 0 {
		state.Progress = 100
		state.Message = "同步完成（无数据需要同步）"
		state.SyncedTables = 0
		return
	}

	// Step 2: Import data locally (into this device's database)
	// This is the practical approach: pull data from source, save to local DB
	state.Message = "正在写入本地数据库..."
	payload.DeviceIP = source

	importMode := "full"
	if mode == "增量同步" {
		importMode = "incremental"
	}

	synced, total, importErr := ImportData(e.db, &payload, importMode)
	if importErr != nil {
		state.LastError = "本地导入失败: " + importErr.Error()
		state.Message = "同步失败: " + importErr.Error()
		log.Printf("[SyncEngine] Task %d: local import failed: %v", taskID, importErr)
		return
	}

	state.SyncedTables = synced
	state.Progress = 100
	state.Message = fmt.Sprintf("同步完成，已同步 %d/%d 张表", synced, total)
	state.LastError = ""
	log.Printf("[SyncEngine] Task %d: sync from %s completed, %d/%d tables synced", taskID, source, synced, total)
}

func (e *Engine) updateTaskDB(taskID int, state *TaskState) {
	duration := time.Since(state.StartedAt).Minutes()
	successRate := 0.0
	if state.TotalTables > 0 {
		successRate = float64(state.SyncedTables) / float64(state.TotalTables) * 100
	}
	nowLocal := time.Now().Format("2006-01-02 15:04:05")
	_, _ = e.db.Exec(`UPDATE sync_tasks SET progress = ?, synced_data = ?, total_data = ?, success_rate = ?, avg_duration = ?, last_synced_at = ?, updated_at = ? WHERE id = ?`,
		state.Progress, state.SyncedTables, state.TotalTables, successRate, duration, nowLocal, nowLocal, taskID)
}

func (e *Engine) setTaskStatus(taskID int, status string) {
	nowLocal := time.Now().Format("2006-01-02 15:04:05")
	_, _ = e.db.Exec(`UPDATE sync_tasks SET status = ?, updated_at = ? WHERE id = ?`, status, nowLocal, taskID)
}

// ParseFrequencyDuration returns the duration for a frequency string (exported for validation)
func ParseFrequencyDuration(freq string) time.Duration {
	switch freq {
	case "10秒":
		return 10 * time.Second
	case "30秒":
		return 30 * time.Second
	case "1分钟":
		return 1 * time.Minute
	case "5分钟":
		return 5 * time.Minute
	case "10分钟":
		return 10 * time.Minute
	case "30分钟":
		return 30 * time.Minute
	case "1小时":
		return 1 * time.Hour
	default:
		return 5 * time.Minute
	}
}

func parseFrequency(freq string) time.Duration {
	return ParseFrequencyDuration(freq)
}

// ExportLocalData exports all syncable tables from the local database
func ExportLocalData(db *db.DB, since string) (*ExportPayload, error) {
	payload := &ExportPayload{
		ExportedAt: time.Now().Format(time.RFC3339),
	}

	for _, table := range SyncableTables {
		td, err := exportTable(db, table, since)
		if err != nil {
			log.Printf("[SyncEngine] Warning: failed to export table %s: %v", table, err)
			continue
		}
		payload.Tables = append(payload.Tables, *td)
	}
	return payload, nil
}

func exportTable(db *db.DB, table, since string) (*TableData, error) {
	query := "SELECT * FROM " + table
	var args []interface{}

	// For incremental sync, filter by time columns
	if since != "" {
		timeCol := getTimeColumn(table)
		if timeCol != "" {
			query += " WHERE " + timeCol + " > ?"
			args = append(args, since)
		}
	}

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	td := &TableData{
		Table:   table,
		Columns: columns,
	}

	for rows.Next() {
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}
		if err := rows.Scan(valuePtrs...); err != nil {
			continue
		}
		row := make(map[string]interface{})
		for i, col := range columns {
			val := values[i]
			// Convert []byte to string for JSON
			if b, ok := val.([]byte); ok {
				row[col] = string(b)
			} else {
				row[col] = val
			}
		}
		td.Rows = append(td.Rows, row)
	}
	td.Count = len(td.Rows)
	return td, nil
}

// ImportData imports data into the local database
func ImportData(db *db.DB, payload *ExportPayload, mode string) (synced int, total int, err error) {
	total = len(payload.Tables)

	for _, td := range payload.Tables {
		if err := importTable(db, &td, mode); err != nil {
			log.Printf("[SyncEngine] Warning: failed to import table %s: %v", td.Table, err)
			continue
		}
		synced++
	}
	return synced, total, nil
}

func importTable(db *db.DB, td *TableData, mode string) error {
	if len(td.Rows) == 0 {
		return nil // nothing to import
	}

	// Validate table name is in allowed list
	allowed := false
	for _, t := range SyncableTables {
		if t == td.Table {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Errorf("table %s is not in syncable list", td.Table)
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if mode == "full" {
		// Full sync: clear existing data first
		if _, err := tx.Exec("DELETE FROM " + td.Table); err != nil {
			return fmt.Errorf("清空表 %s 失败: %w", td.Table, err)
		}
	}

	// Build INSERT OR REPLACE statement
	if len(td.Columns) == 0 || len(td.Rows) == 0 {
		return tx.Commit()
	}

	placeholders := make([]string, len(td.Columns))
	for i := range placeholders {
		placeholders[i] = "?"
	}
	insertSQL := fmt.Sprintf("INSERT OR REPLACE INTO %s (%s) VALUES (%s)",
		td.Table,
		strings.Join(td.Columns, ", "),
		strings.Join(placeholders, ", "))

	stmt, err := tx.Prepare(insertSQL)
	if err != nil {
		return fmt.Errorf("准备SQL失败: %w", err)
	}
	defer stmt.Close()

	for _, row := range td.Rows {
		vals := make([]interface{}, len(td.Columns))
		for i, col := range td.Columns {
			vals[i] = row[col]
		}
		if _, err := stmt.Exec(vals...); err != nil {
			log.Printf("[SyncEngine] Warning: insert row into %s failed: %v", td.Table, err)
			continue
		}
	}

	return tx.Commit()
}

func getTimeColumn(table string) string {
	switch table {
	case "recordings", "logs", "alerts":
		return "created_at"
	case "updates":
		return "updated_at"
	case "hardware_items":
		return "updated_at"
	case "video_sources":
		return "created_at"
	case "devices":
		return "updated_at"
	case "sync_tasks":
		return "updated_at"
	case "drones":
		return "updated_at"
	case "battery_records":
		return "created_at"
	case "battery_alerts":
		return "created_at"
	case "gps_devices":
		return "updated_at"
	case "gps_history":
		return "created_at"
	case "gps_fence_alerts":
		return "created_at"
	case "flight_missions":
		return "updated_at"
	case "mission_logs":
		return "created_at"
	case "perf_reports":
		return "created_at"
	case "flight_plans":
		return "created_at"
	case "user_stats":
		return "updated_at"
	default:
		return "created_at"
	}
}
