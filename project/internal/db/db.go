package db

import (
    "database/sql"
    "fmt"
    "os"
    "path/filepath"

    _ "modernc.org/sqlite"
)

func Open(path string) (*sql.DB, error) {
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

func Migrate(db *sql.DB) error {
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
        // per-user stats
        `CREATE TABLE IF NOT EXISTS user_stats (
            user_id INTEGER PRIMARY KEY,
            total_connections INTEGER NOT NULL DEFAULT 0,
            updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
            FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
        );`,
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
    return nil
}