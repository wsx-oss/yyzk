// AI辅助生成：DeepSeek v3.2, API调用, 2025-11-10 14:00-15:20
// 环节：数据库设计 — MySQL+Redis缓存设计
// 关键提示词：设计无人机系统数据库表结构
// AI回复：提供设备、任务、日志、遥测等基础表结构及索引优化
// 人工修改说明：拆分为18张业务表并规范字段命名
package db

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	_ "modernc.org/sqlite"
)

// Driver holds the active database driver name: "sqlite" or "mysql".
var Driver string = "sqlite"

// IsMySQL returns true when the active driver is MySQL.
func IsMySQL() bool { return Driver == "mysql" }

// ---------- SQL compatibility layer ----------

// reDateTime matches datetime(column) or datetime(table.column)
var reDateTime = regexp.MustCompile(`datetime\((\w+(?:\.\w+)?)\)`)

// reDateTimeFunc matches datetime(FUNC(...)) e.g. datetime(COALESCE(col,'val'))
var reDateTimeFunc = regexp.MustCompile(`datetime\(([A-Z]+\([^)]+\))\)`)

// AdaptSQL transparently converts SQLite-dialect SQL to MySQL when needed.
// When the driver is SQLite the input is returned unchanged.
func AdaptSQL(s string) string {
	if !IsMySQL() {
		return s
	}
	// DML
	s = strings.ReplaceAll(s, "INSERT OR IGNORE INTO", "INSERT IGNORE INTO")
	s = strings.ReplaceAll(s, "INSERT OR REPLACE INTO", "REPLACE INTO")
	// datetime with offsets (longer patterns first)
	s = strings.ReplaceAll(s, "datetime('now','-30 seconds')", "DATE_SUB(NOW(), INTERVAL 30 SECOND)")
	s = strings.ReplaceAll(s, "datetime('now', '-30 seconds')", "DATE_SUB(NOW(), INTERVAL 30 SECOND)")
	s = strings.ReplaceAll(s, "datetime('now','-15 seconds')", "DATE_SUB(NOW(), INTERVAL 15 SECOND)")
	s = strings.ReplaceAll(s, "datetime('now', '-15 seconds')", "DATE_SUB(NOW(), INTERVAL 15 SECOND)")
	s = strings.ReplaceAll(s, "datetime('now','-10 minutes')", "DATE_SUB(NOW(), INTERVAL 10 MINUTE)")
	s = strings.ReplaceAll(s, "datetime('now', '-10 minutes')", "DATE_SUB(NOW(), INTERVAL 10 MINUTE)")
	s = strings.ReplaceAll(s, "datetime('now','-6 hours')", "DATE_SUB(NOW(), INTERVAL 6 HOUR)")
	s = strings.ReplaceAll(s, "datetime('now', '-6 hours')", "DATE_SUB(NOW(), INTERVAL 6 HOUR)")
	s = strings.ReplaceAll(s, "datetime('now','-2 hours')", "DATE_SUB(NOW(), INTERVAL 2 HOUR)")
	s = strings.ReplaceAll(s, "datetime('now', '-2 hours')", "DATE_SUB(NOW(), INTERVAL 2 HOUR)")
	s = strings.ReplaceAll(s, "datetime('now','-1 hour')", "DATE_SUB(NOW(), INTERVAL 1 HOUR)")
	s = strings.ReplaceAll(s, "datetime('now', '-1 hour')", "DATE_SUB(NOW(), INTERVAL 1 HOUR)")
	s = strings.ReplaceAll(s, "datetime('now','-7 days')", "DATE_SUB(NOW(), INTERVAL 7 DAY)")
	s = strings.ReplaceAll(s, "datetime('now', '-7 days')", "DATE_SUB(NOW(), INTERVAL 7 DAY)")
	// datetime('now') -> NOW()
	s = strings.ReplaceAll(s, "datetime('now')", "NOW()")
	// datetime(COALESCE(...)) -> COALESCE(...)
	s = reDateTimeFunc.ReplaceAllString(s, "$1")
	// datetime(column) or datetime(t.column) -> column / t.column
	s = reDateTime.ReplaceAllString(s, "$1")
	return s
}

// ---------- DB wrapper (auto-adapts every query) ----------

// DB wraps *sql.DB and calls AdaptSQL on every Exec / Query / QueryRow.
type DB struct {
	inner *sql.DB
}

func (d *DB) Exec(query string, args ...any) (sql.Result, error) {
	return d.inner.Exec(AdaptSQL(query), args...)
}
func (d *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return d.inner.ExecContext(ctx, AdaptSQL(query), args...)
}
func (d *DB) Query(query string, args ...any) (*sql.Rows, error) {
	return d.inner.Query(AdaptSQL(query), args...)
}
func (d *DB) QueryRow(query string, args ...any) *sql.Row {
	return d.inner.QueryRow(AdaptSQL(query), args...)
}
func (d *DB) Begin() (*Tx, error) {
	tx, err := d.inner.Begin()
	if err != nil {
		return nil, err
	}
	return &Tx{inner: tx}, nil
}
func (d *DB) Close() error { return d.inner.Close() }

// Raw returns the underlying *sql.DB (e.g. for driver-specific operations).
func (d *DB) Raw() *sql.DB { return d.inner }

// Tx wraps *sql.Tx with automatic SQL adaptation.
type Tx struct {
	inner *sql.Tx
}

func (t *Tx) Exec(query string, args ...any) (sql.Result, error) {
	return t.inner.Exec(AdaptSQL(query), args...)
}
func (t *Tx) Query(query string, args ...any) (*sql.Rows, error) {
	return t.inner.Query(AdaptSQL(query), args...)
}
func (t *Tx) QueryRow(query string, args ...any) *sql.Row {
	return t.inner.QueryRow(AdaptSQL(query), args...)
}
func (t *Tx) Prepare(query string) (*sql.Stmt, error) {
	return t.inner.Prepare(AdaptSQL(query))
}
func (t *Tx) Commit() error   { return t.inner.Commit() }
func (t *Tx) Rollback() error { return t.inner.Rollback() }

// Open connects to the database identified by driver ("sqlite" or "mysql") and dsn.
// For SQLite dsn is the file path; for MySQL it is a DSN like user:pass@tcp(host:port)/dbname.
func Open(driver, dsn string) (*DB, error) {
	Driver = driver
	var raw *sql.DB
	var err error
	switch driver {
	case "mysql":
		raw, err = openMySQL(dsn)
	default:
		raw, err = openSQLite(dsn)
	}
	if err != nil {
		return nil, err
	}
	return &DB{inner: raw}, nil
}

func openSQLite(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	dsn := fmt.Sprintf("file:%s?cache=shared&mode=rwc", path)
	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if _, err := database.Exec("PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;"); err != nil {
		return nil, err
	}
	return database, nil
}

func openMySQL(dsn string) (*sql.DB, error) {
	// 解析 DSN 并注入 time_zone 参数，确保连接池中每个连接都使用上海时区
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse DSN: %w", err)
	}
	if cfg.Params == nil {
		cfg.Params = make(map[string]string)
	}
	cfg.Params["time_zone"] = "'+08:00'"
	cfg.Loc = time.FixedZone("CST", 8*3600)

	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		return nil, fmt.Errorf("mysql connector: %w", err)
	}
	database := sql.OpenDB(connector)
	database.SetMaxOpenConns(50)
	database.SetMaxIdleConns(25)
	database.SetConnMaxLifetime(5 * time.Minute)
	database.SetConnMaxIdleTime(2 * time.Minute)
	if err := database.Ping(); err != nil {
		return nil, fmt.Errorf("MySQL ping: %w", err)
	}
	// Enable PIPES_AS_CONCAT so || works as string concat (matching SQLite)
	database.Exec("SET SESSION sql_mode = CONCAT(@@sql_mode, ',PIPES_AS_CONCAT')")
	log.Println("[DB] Connected to MySQL (timezone: Asia/Shanghai, every connection)")
	return database, nil
}

// Migrate creates all tables and indexes for the active driver.
func Migrate(d *DB) error {
	if IsMySQL() {
		return migrateMySQL(d.inner)
	}
	return migrateSQLite(d.inner)
}

func migrateSQLite(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS recordings (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            filename TEXT NOT NULL,
            mime TEXT,
            duration REAL,
            size INTEGER,
            user_id TEXT DEFAULT '',
            interact_type TEXT DEFAULT '',
            content TEXT DEFAULT '',
            clarity TEXT DEFAULT '',
            result TEXT DEFAULT '',
            score INTEGER DEFAULT 4,
            tags TEXT DEFAULT '',
            remark TEXT DEFAULT '',
            interact_time TEXT DEFAULT '',
            created_at DATETIME DEFAULT CURRENT_TIMESTAMP
        );`,
		`CREATE TABLE IF NOT EXISTS logs (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            level TEXT,
            message TEXT,
            meta TEXT,
            created_at DATETIME DEFAULT CURRENT_TIMESTAMP
        );`,
		`CREATE TABLE IF NOT EXISTS alerts (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            category TEXT,
            severity TEXT,
            message TEXT,
            acknowledged INTEGER DEFAULT 0,
            created_at DATETIME DEFAULT CURRENT_TIMESTAMP
        );`,
		`CREATE TABLE IF NOT EXISTS updates (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            version TEXT,
            description TEXT,
            status TEXT,
            created_at DATETIME DEFAULT CURRENT_TIMESTAMP
        );`,
		`CREATE TABLE IF NOT EXISTS sync_status (
            id INTEGER PRIMARY KEY CHECK (id = 1),
            status TEXT,
            message TEXT,
            last_synced_at DATETIME,
            updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
        );`,
		`INSERT OR IGNORE INTO sync_status(id, status, message) VALUES(1, 'idle', '');`,
		`CREATE TABLE IF NOT EXISTS users (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            username TEXT UNIQUE NOT NULL,
            password_hash TEXT NOT NULL,
            created_at DATETIME DEFAULT CURRENT_TIMESTAMP
        );`,
		`CREATE TABLE IF NOT EXISTS sessions (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            user_id INTEGER NOT NULL,
            token TEXT UNIQUE NOT NULL,
            expires_at DATETIME NOT NULL,
            created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
            FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
        );`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_token ON sessions(token);`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);`,
		`CREATE INDEX IF NOT EXISTS idx_users_username ON users(username);`,
		`CREATE INDEX IF NOT EXISTS idx_recordings_created ON recordings(created_at);`,
		`CREATE INDEX IF NOT EXISTS idx_alerts_created ON alerts(created_at);`,
		`CREATE INDEX IF NOT EXISTS idx_logs_created ON logs(created_at);`,
		// devices for remote desktop management
		`CREATE TABLE IF NOT EXISTS devices (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            name TEXT NOT NULL,
            ip TEXT NOT NULL,
            protocol TEXT NOT NULL, -- VNC/RDP/SSH
            port INTEGER DEFAULT 0,
            username TEXT DEFAULT '',
            password TEXT DEFAULT '',
            auto_connect INTEGER DEFAULT 0,
            log_enabled INTEGER DEFAULT 0,
            description TEXT DEFAULT '',
            status TEXT NOT NULL DEFAULT 'offline', -- online/offline
            created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
            updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
            last_connected_at DATETIME
        );`,
		`CREATE INDEX IF NOT EXISTS idx_devices_name ON devices(name);`,
		`CREATE INDEX IF NOT EXISTS idx_devices_protocol ON devices(protocol);`,
		`CREATE INDEX IF NOT EXISTS idx_devices_created ON devices(created_at);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_devices_name_ip_protocol ON devices(name, ip, protocol);`,
		// video sources for video monitoring
		`CREATE TABLE IF NOT EXISTS video_sources (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            name TEXT NOT NULL,
            url TEXT NOT NULL,
            region TEXT DEFAULT '',
            clarity TEXT DEFAULT '',
            status TEXT DEFAULT '正常',
            recording INTEGER DEFAULT 0,
            start_time TEXT DEFAULT '',
            end_time TEXT DEFAULT '',
            created_at DATETIME DEFAULT CURRENT_TIMESTAMP
        );`,
		`CREATE INDEX IF NOT EXISTS idx_video_sources_name ON video_sources(name);`,
		`CREATE INDEX IF NOT EXISTS idx_video_sources_region ON video_sources(region);`,
		// hardware items for hardware status detection module
		`CREATE TABLE IF NOT EXISTS hardware_items (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            name TEXT NOT NULL,
            type TEXT NOT NULL DEFAULT '服务器',
            ip TEXT NOT NULL,
            status TEXT NOT NULL DEFAULT '在线',
            description TEXT DEFAULT '',
            temperature REAL DEFAULT 0,
            cpu_usage REAL DEFAULT 0,
            mem_usage REAL DEFAULT 0,
            network_bandwidth TEXT DEFAULT '0Mbps',
            detected_at DATETIME DEFAULT CURRENT_TIMESTAMP,
            created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
            updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
        );`,
		`CREATE INDEX IF NOT EXISTS idx_hardware_items_name ON hardware_items(name);`,
		`CREATE INDEX IF NOT EXISTS idx_hardware_items_type ON hardware_items(type);`,
		`CREATE INDEX IF NOT EXISTS idx_hardware_items_status ON hardware_items(status);`,
		`CREATE INDEX IF NOT EXISTS idx_hardware_items_ip ON hardware_items(ip);`,
		`CREATE INDEX IF NOT EXISTS idx_hardware_items_detected ON hardware_items(detected_at);`,
		// sync tasks for data sync management module
		`CREATE TABLE IF NOT EXISTS sync_tasks (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            source TEXT NOT NULL DEFAULT '',
            target TEXT NOT NULL DEFAULT '',
            frequency TEXT NOT NULL DEFAULT '5分钟',
            mode TEXT NOT NULL DEFAULT '全量同步',
            start_time TEXT DEFAULT '',
            end_time TEXT DEFAULT '',
            status TEXT NOT NULL DEFAULT '待启动',
            sync_status_enabled INTEGER DEFAULT 0,
            log_enabled INTEGER DEFAULT 0,
            progress INTEGER DEFAULT 0,
            synced_data INTEGER DEFAULT 0,
            total_data INTEGER DEFAULT 1000,
            success_rate REAL DEFAULT 0,
            avg_duration REAL DEFAULT 0,
            last_synced_at DATETIME,
            created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
            updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
        );`,
		`CREATE INDEX IF NOT EXISTS idx_sync_tasks_status ON sync_tasks(status);`,
		`CREATE INDEX IF NOT EXISTS idx_sync_tasks_mode ON sync_tasks(mode);`,
		`CREATE INDEX IF NOT EXISTS idx_sync_tasks_created ON sync_tasks(created_at);`,
		// performance analysis reports
		`CREATE TABLE IF NOT EXISTS perf_reports (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            analysis_time TEXT NOT NULL DEFAULT '',
            module_name TEXT NOT NULL DEFAULT '',
            user_id TEXT NOT NULL DEFAULT '',
            response_time TEXT NOT NULL DEFAULT '',
            throughput TEXT NOT NULL DEFAULT '',
            error_rate TEXT NOT NULL DEFAULT '',
            description TEXT DEFAULT '',
            analysis_type TEXT DEFAULT '整体性能',
            created_at DATETIME DEFAULT CURRENT_TIMESTAMP
        );`,
		`CREATE INDEX IF NOT EXISTS idx_perf_reports_module ON perf_reports(module_name);`,
		`CREATE INDEX IF NOT EXISTS idx_perf_reports_user ON perf_reports(user_id);`,
		`CREATE INDEX IF NOT EXISTS idx_perf_reports_time ON perf_reports(analysis_time);`,
		// per-user stats
		`CREATE TABLE IF NOT EXISTS user_stats (
            user_id INTEGER PRIMARY KEY,
            total_connections INTEGER NOT NULL DEFAULT 0,
            updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
            FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
        );`,
		// flight missions management
		`CREATE TABLE IF NOT EXISTS flight_missions (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            name TEXT NOT NULL,
            route TEXT NOT NULL DEFAULT '',
            target TEXT NOT NULL DEFAULT '',
            estimated_duration TEXT DEFAULT '',
            status TEXT NOT NULL DEFAULT '待起飞',
            current_phase TEXT NOT NULL DEFAULT '待命',
            progress INTEGER DEFAULT 0,
            start_time TEXT DEFAULT '',
            end_time TEXT DEFAULT '',
            description TEXT DEFAULT '',
            created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
            updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
        );`,
		`CREATE INDEX IF NOT EXISTS idx_flight_missions_name ON flight_missions(name);`,
		`CREATE INDEX IF NOT EXISTS idx_flight_missions_status ON flight_missions(status);`,
		`CREATE INDEX IF NOT EXISTS idx_flight_missions_created ON flight_missions(created_at);`,
		// mission logs (linked to flight missions)
		`CREATE TABLE IF NOT EXISTS mission_logs (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            mission_id INTEGER NOT NULL,
            phase TEXT NOT NULL DEFAULT '',
            message TEXT DEFAULT '',
            created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
            FOREIGN KEY (mission_id) REFERENCES flight_missions(id) ON DELETE CASCADE
        );`,
		`CREATE INDEX IF NOT EXISTS idx_mission_logs_mission ON mission_logs(mission_id);`,
		// GPS devices for location tracking
		`CREATE TABLE IF NOT EXISTS gps_devices (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            name TEXT NOT NULL,
            device_type TEXT NOT NULL DEFAULT '无人机',
            latitude REAL DEFAULT 0,
            longitude REAL DEFAULT 0,
            altitude REAL DEFAULT 0,
            speed REAL DEFAULT 0,
            heading REAL DEFAULT 0,
            accuracy REAL DEFAULT 0,
            status TEXT NOT NULL DEFAULT '在线',
            fence_enabled INTEGER DEFAULT 0,
            fence_lat REAL DEFAULT 0,
            fence_lng REAL DEFAULT 0,
            fence_radius REAL DEFAULT 0,
            last_update DATETIME DEFAULT CURRENT_TIMESTAMP,
            created_at DATETIME DEFAULT CURRENT_TIMESTAMP
        );`,
		`CREATE INDEX IF NOT EXISTS idx_gps_devices_name ON gps_devices(name);`,
		`CREATE INDEX IF NOT EXISTS idx_gps_devices_status ON gps_devices(status);`,
		// GPS position history
		`CREATE TABLE IF NOT EXISTS gps_history (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            device_id INTEGER NOT NULL,
            latitude REAL DEFAULT 0,
            longitude REAL DEFAULT 0,
            altitude REAL DEFAULT 0,
            speed REAL DEFAULT 0,
            heading REAL DEFAULT 0,
            created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
            FOREIGN KEY (device_id) REFERENCES gps_devices(id) ON DELETE CASCADE
        );`,
		`CREATE INDEX IF NOT EXISTS idx_gps_history_device ON gps_history(device_id);`,
		`CREATE INDEX IF NOT EXISTS idx_gps_history_created ON gps_history(created_at);`,
		// GPS geofence alerts
		`CREATE TABLE IF NOT EXISTS gps_fence_alerts (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            device_id INTEGER NOT NULL,
            device_name TEXT DEFAULT '',
            latitude REAL DEFAULT 0,
            longitude REAL DEFAULT 0,
            fence_lat REAL DEFAULT 0,
            fence_lng REAL DEFAULT 0,
            fence_radius REAL DEFAULT 0,
            distance REAL DEFAULT 0,
            message TEXT DEFAULT '',
            acknowledged INTEGER DEFAULT 0,
            created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
            FOREIGN KEY (device_id) REFERENCES gps_devices(id) ON DELETE CASCADE
        );`,
		`CREATE INDEX IF NOT EXISTS idx_gps_fence_alerts_device ON gps_fence_alerts(device_id);`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	// safely add new columns to recordings (ignore "duplicate column" errors for existing DBs)
	alterCols := []string{
		`ALTER TABLE recordings ADD COLUMN user_id TEXT DEFAULT ''`,
		`ALTER TABLE recordings ADD COLUMN interact_type TEXT DEFAULT ''`,
		`ALTER TABLE recordings ADD COLUMN content TEXT DEFAULT ''`,
		`ALTER TABLE recordings ADD COLUMN clarity TEXT DEFAULT ''`,
		`ALTER TABLE recordings ADD COLUMN result TEXT DEFAULT ''`,
		`ALTER TABLE recordings ADD COLUMN score INTEGER DEFAULT 4`,
		`ALTER TABLE recordings ADD COLUMN tags TEXT DEFAULT ''`,
		`ALTER TABLE recordings ADD COLUMN remark TEXT DEFAULT ''`,
		`ALTER TABLE recordings ADD COLUMN interact_time TEXT DEFAULT ''`,
	}
	for _, s := range alterCols {
		db.Exec(s) // ignore error if column already exists
	}
	// safely add new columns to logs
	logCols := []string{
		`ALTER TABLE logs ADD COLUMN op_type TEXT DEFAULT ''`,
		`ALTER TABLE logs ADD COLUMN operator TEXT DEFAULT ''`,
		`ALTER TABLE logs ADD COLUMN op_time TEXT DEFAULT ''`,
		`ALTER TABLE logs ADD COLUMN op_result TEXT DEFAULT ''`,
		`ALTER TABLE logs ADD COLUMN device_name TEXT DEFAULT ''`,
		`ALTER TABLE logs ADD COLUMN log_status TEXT DEFAULT '启用'`,
		`ALTER TABLE logs ADD COLUMN detail TEXT DEFAULT ''`,
	}
	for _, s := range logCols {
		db.Exec(s)
	}
	// safely add new columns to devices
	deviceCols := []string{
		`ALTER TABLE devices ADD COLUMN port INTEGER DEFAULT 0`,
		`ALTER TABLE devices ADD COLUMN username TEXT DEFAULT ''`,
		`ALTER TABLE devices ADD COLUMN password TEXT DEFAULT ''`,
		`ALTER TABLE devices ADD COLUMN auto_connect INTEGER DEFAULT 0`,
		`ALTER TABLE devices ADD COLUMN log_enabled INTEGER DEFAULT 0`,
		`ALTER TABLE devices ADD COLUMN description TEXT DEFAULT ''`,
	}
	for _, s := range deviceCols {
		db.Exec(s)
	}
	// safely add new columns to alerts
	alertCols := []string{
		`ALTER TABLE alerts ADD COLUMN alert_time TEXT DEFAULT ''`,
		`ALTER TABLE alerts ADD COLUMN priority TEXT DEFAULT '中'`,
		`ALTER TABLE alerts ADD COLUMN device TEXT DEFAULT ''`,
		`ALTER TABLE alerts ADD COLUMN description TEXT DEFAULT ''`,
		`ALTER TABLE alerts ADD COLUMN status TEXT DEFAULT '未解决'`,
	}
	for _, s := range alertCols {
		db.Exec(s)
	}
	// safely add new columns to updates
	updatesCols := []string{
		`ALTER TABLE updates ADD COLUMN name TEXT DEFAULT ''`,
		`ALTER TABLE updates ADD COLUMN type TEXT DEFAULT '功能更新'`,
		`ALTER TABLE updates ADD COLUMN size TEXT DEFAULT ''`,
		`ALTER TABLE updates ADD COLUMN auto_update INTEGER DEFAULT 0`,
		`ALTER TABLE updates ADD COLUMN force_update INTEGER DEFAULT 0`,
		`ALTER TABLE updates ADD COLUMN publish_date TEXT DEFAULT ''`,
		`ALTER TABLE updates ADD COLUMN updated_at DATETIME DEFAULT CURRENT_TIMESTAMP`,
	}
	for _, s := range updatesCols {
		db.Exec(s)
	}
	// safely add device_id column to flight_missions for drone association
	flightCols := []string{
		`ALTER TABLE flight_missions ADD COLUMN device_id INTEGER DEFAULT 0`,
	}
	for _, s := range flightCols {
		db.Exec(s)
	}
	// safely add agent_id column to gps_devices for agent push matching
	gpsCols := []string{
		`ALTER TABLE gps_devices ADD COLUMN agent_id TEXT DEFAULT ''`,
	}
	for _, s := range gpsCols {
		db.Exec(s)
	}

	// unified drone registry – central place to register a drone once
	droneTables := []string{
		`CREATE TABLE IF NOT EXISTS drones (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			serial_number TEXT DEFAULT '',
			model TEXT DEFAULT '',
			description TEXT DEFAULT '',
			ip TEXT DEFAULT '',
			ssh_port INTEGER DEFAULT 22,
			vnc_port INTEGER DEFAULT 5900,
			rdp_port INTEGER DEFAULT 3389,
			protocol TEXT DEFAULT 'SSH',
			username TEXT DEFAULT '',
			password TEXT DEFAULT '',
			agent_id TEXT DEFAULT '',
			initial_lat REAL DEFAULT 0,
			initial_lng REAL DEFAULT 0,
			initial_alt REAL DEFAULT 0,
			fence_enabled INTEGER DEFAULT 0,
			fence_lat REAL DEFAULT 0,
			fence_lng REAL DEFAULT 0,
			fence_radius REAL DEFAULT 500,
			auto_connect INTEGER DEFAULT 0,
			log_enabled INTEGER DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'offline',
			linked_device_id INTEGER DEFAULT 0,
			linked_gps_device_id INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_drones_name ON drones(name)`,
		`CREATE INDEX IF NOT EXISTS idx_drones_agent_id ON drones(agent_id)`,
		`CREATE INDEX IF NOT EXISTS idx_drones_status ON drones(status)`,
	}
	for _, s := range droneTables {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("drones table: %w", err)
		}
	}

	// add drone_id to devices and gps_devices for back-reference
	droneLinks := []string{
		`ALTER TABLE devices ADD COLUMN drone_id INTEGER DEFAULT 0`,
		`ALTER TABLE gps_devices ADD COLUMN drone_id INTEGER DEFAULT 0`,
	}
	for _, s := range droneLinks {
		db.Exec(s) // ignore if column already exists
	}

	// add video_url to drones for camera/video stream
	db.Exec(`ALTER TABLE drones ADD COLUMN video_url TEXT DEFAULT ''`)

	// add map_visible to gps_devices: 0=hidden from map (default), 1=shown on map
	db.Exec(`ALTER TABLE gps_devices ADD COLUMN map_visible INTEGER DEFAULT 0`)

	// battery monitoring tables
	batteryTables := []string{
		`CREATE TABLE IF NOT EXISTS battery_records (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			device_id INTEGER NOT NULL,
			device_name TEXT NOT NULL DEFAULT '',
			voltage REAL DEFAULT 0,
			current_val REAL DEFAULT 0,
			level INTEGER DEFAULT 100,
			temperature REAL DEFAULT 25,
			health INTEGER DEFAULT 100,
			status TEXT DEFAULT '正常',
			charge_cycles INTEGER DEFAULT 0,
			remaining_time TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_battery_records_device ON battery_records(device_id)`,
		`CREATE INDEX IF NOT EXISTS idx_battery_records_created ON battery_records(created_at)`,
		`CREATE TABLE IF NOT EXISTS battery_alerts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			device_id INTEGER NOT NULL,
			device_name TEXT NOT NULL DEFAULT '',
			level INTEGER DEFAULT 0,
			voltage REAL DEFAULT 0,
			temperature REAL DEFAULT 0,
			alert_type TEXT DEFAULT '',
			message TEXT DEFAULT '',
			acknowledged INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_battery_alerts_device ON battery_alerts(device_id)`,
	}
	for _, s := range batteryTables {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("battery table: %w", err)
		}
	}

	// LLM flight plan drafts
	flightPlanTables := []string{
		`CREATE TABLE IF NOT EXISTS flight_plans (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			drone_id INTEGER DEFAULT 0,
			request_json TEXT DEFAULT '{}',
			result_json TEXT DEFAULT '{}',
			source TEXT DEFAULT 'llm',
			status TEXT DEFAULT 'draft',
			mission_id INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_flight_plans_status ON flight_plans(status)`,
		`CREATE INDEX IF NOT EXISTS idx_flight_plans_drone ON flight_plans(drone_id)`,
		`CREATE INDEX IF NOT EXISTS idx_flight_plans_created ON flight_plans(created_at)`,
	}
	for _, s := range flightPlanTables {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("flight_plans table: %w", err)
		}
	}

	// No-fly zones management
	noFlyZoneTables := []string{
		`CREATE TABLE IF NOT EXISTS no_fly_zones (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			zone_type TEXT NOT NULL DEFAULT '禁飞区',
			shape_type TEXT NOT NULL DEFAULT 'polygon',
			shape_json TEXT NOT NULL DEFAULT '[]',
			altitude_limit INTEGER DEFAULT -1,
			altitude_enabled INTEGER DEFAULT 0,
			area_m2 REAL DEFAULT 0,
			address TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_no_fly_zones_name ON no_fly_zones(name)`,
		`CREATE INDEX IF NOT EXISTS idx_no_fly_zones_type ON no_fly_zones(zone_type)`,
	}
	for _, s := range noFlyZoneTables {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("no_fly_zones table: %w", err)
		}
	}

	// Add waypoints_json to flight_missions for detail map rendering
	db.Exec(`ALTER TABLE flight_missions ADD COLUMN waypoints_json TEXT DEFAULT ''`)

	// CoT (Chain of Thought) reasoning chains
	cotTables := []string{
		`CREATE TABLE IF NOT EXISTS cot_chains (
			id TEXT PRIMARY KEY,
			task_type TEXT NOT NULL,
			task_id TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			steps TEXT NOT NULL DEFAULT '[]',
			final_decision TEXT DEFAULT '',
			overall_confidence REAL DEFAULT 0.0,
			metadata TEXT DEFAULT '{}'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_cot_chains_task_type ON cot_chains(task_type)`,
		`CREATE INDEX IF NOT EXISTS idx_cot_chains_task_id ON cot_chains(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_cot_chains_created ON cot_chains(created_at)`,
	}
	for _, s := range cotTables {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("cot_chains table: %w", err)
		}
	}

	// Notifications center
	notifTables := []string{
		`CREATE TABLE IF NOT EXISTS notifications (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			type TEXT NOT NULL DEFAULT 'system',
			title TEXT NOT NULL DEFAULT '',
			message TEXT NOT NULL DEFAULT '',
			source TEXT DEFAULT '',
			link TEXT DEFAULT '',
			is_read INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_notifications_type ON notifications(type)`,
		`CREATE INDEX IF NOT EXISTS idx_notifications_is_read ON notifications(is_read)`,
		`CREATE INDEX IF NOT EXISTS idx_notifications_created ON notifications(created_at)`,
	}
	for _, s := range notifTables {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("notifications table: %w", err)
		}
	}

	// AI assistant chat history
	aiChatTables := []string{
		`CREATE TABLE IF NOT EXISTS ai_chat_messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL DEFAULT 'default',
			role TEXT NOT NULL DEFAULT 'user',
			content TEXT NOT NULL DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ai_chat_session ON ai_chat_messages(session_id)`,
		`CREATE INDEX IF NOT EXISTS idx_ai_chat_created ON ai_chat_messages(created_at)`,
	}
	for _, s := range aiChatTables {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("ai_chat_messages table: %w", err)
		}
	}

	// Simulation tables
	simTables := []string{
		`CREATE TABLE IF NOT EXISTS sim_batches (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL DEFAULT '',
			count INTEGER DEFAULT 0,
			model TEXT DEFAULT '',
			center_lat REAL DEFAULT 0,
			center_lng REAL DEFAULT 0,
			spread_m REAL DEFAULT 500,
			cruise_speed REAL DEFAULT 15,
			max_alt REAL DEFAULT 120,
			loop_route INTEGER DEFAULT 0,
			waypoints_json TEXT DEFAULT '[]',
			status TEXT DEFAULT 'created',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sim_batches_status ON sim_batches(status)`,
		`CREATE TABLE IF NOT EXISTS sim_instances (
			id TEXT PRIMARY KEY,
			batch_id TEXT DEFAULT '',
			name TEXT NOT NULL DEFAULT '',
			model TEXT DEFAULT '',
			state TEXT DEFAULT 'created',
			flight_phase TEXT DEFAULT '待飞',
			task_status TEXT DEFAULT '未开始',
			lat REAL DEFAULT 0,
			lng REAL DEFAULT 0,
			alt REAL DEFAULT 0,
			speed REAL DEFAULT 0,
			heading REAL DEFAULT 0,
			battery_level INTEGER DEFAULT 100,
			battery_voltage REAL DEFAULT 25.2,
			battery_temp REAL DEFAULT 25,
			battery_health INTEGER DEFAULT 100,
			total_flight_sec REAL DEFAULT 0,
			config_json TEXT DEFAULT '{}',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sim_instances_batch ON sim_instances(batch_id)`,
		`CREATE INDEX IF NOT EXISTS idx_sim_instances_state ON sim_instances(state)`,
		`CREATE TABLE IF NOT EXISTS sim_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			instance_id TEXT NOT NULL DEFAULT '',
			event_type TEXT NOT NULL DEFAULT '',
			level TEXT DEFAULT '提示',
			message TEXT DEFAULT '',
			detail_json TEXT DEFAULT '{}',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sim_events_instance ON sim_events(instance_id)`,
		`CREATE INDEX IF NOT EXISTS idx_sim_events_type ON sim_events(event_type)`,
		`CREATE INDEX IF NOT EXISTS idx_sim_events_created ON sim_events(created_at)`,
		`CREATE TABLE IF NOT EXISTS sim_telemetry_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			instance_id TEXT NOT NULL DEFAULT '',
			lat REAL DEFAULT 0,
			lng REAL DEFAULT 0,
			alt REAL DEFAULT 0,
			speed REAL DEFAULT 0,
			heading REAL DEFAULT 0,
			battery_level INTEGER DEFAULT 100,
			flight_phase TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sim_telemetry_instance ON sim_telemetry_log(instance_id)`,
		`CREATE INDEX IF NOT EXISTS idx_sim_telemetry_created ON sim_telemetry_log(created_at)`,
		`CREATE TABLE IF NOT EXISTS rl_training_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			episode INTEGER DEFAULT 0,
			avg_reward REAL DEFAULT 0,
			route_efficiency REAL DEFAULT 0,
			safety_score REAL DEFAULT 0,
			energy_score REAL DEFAULT 0,
			task_completion REAL DEFAULT 0,
			anomaly_score REAL DEFAULT 0,
			epsilon REAL DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_rl_training_created ON rl_training_log(created_at)`,
	}
	for _, s := range simTables {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("simulation table: %w", err)
		}
	}

	// knowledge base documents & generic data store
	dataTables := []string{
		`CREATE TABLE IF NOT EXISTS knowledge_docs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			content TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_knowledge_docs_name ON knowledge_docs(name)`,
		`CREATE TABLE IF NOT EXISTS data_store (
			store_key TEXT PRIMARY KEY,
			content TEXT,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
	}
	for _, s := range dataTables {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("data table: %w", err)
		}
	}

	// add new columns to existing tables
	db.Exec(`ALTER TABLE backup_records ADD COLUMN sql_content TEXT`)
	db.Exec(`ALTER TABLE recordings ADD COLUMN file_data BLOB`)

	// add indexes for backup_records
	db.Exec(`CREATE INDEX IF NOT EXISTS idx_backup_records_status ON backup_records(status)`)
	db.Exec(`CREATE INDEX IF NOT EXISTS idx_backup_records_created ON backup_records(created_at)`)

	// add profile columns to users table
	userProfileCols := []string{
		`ALTER TABLE users ADD COLUMN email TEXT DEFAULT ''`,
		`ALTER TABLE users ADD COLUMN phone TEXT DEFAULT ''`,
		`ALTER TABLE users ADD COLUMN avatar TEXT DEFAULT ''`,
		`ALTER TABLE users ADD COLUMN nickname TEXT DEFAULT ''`,
	}
	for _, s := range userProfileCols {
		db.Exec(s)
	}

	return nil
}
