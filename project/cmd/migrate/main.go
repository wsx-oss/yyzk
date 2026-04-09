// cmd/migrate/main.go
// One-shot tool to copy data from an existing SQLite database into a MySQL database.
//
// Usage:
//
//	go run ./cmd/migrate --sqlite app.db --mysql "root:pass@tcp(127.0.0.1:3306)/smartcontrol?charset=utf8mb4"
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	_ "modernc.org/sqlite"
)

func main() {
	sqlitePath := flag.String("sqlite", "app.db", "Path to the SQLite database file")
	mysqlDSN := flag.String("mysql", "", "MySQL DSN (e.g. root:pass@tcp(127.0.0.1:3306)/smartcontrol?charset=utf8mb4)")
	flag.Parse()

	if *mysqlDSN == "" {
		log.Fatal("--mysql DSN is required")
	}

	// Open SQLite
	srcDB, err := sql.Open("sqlite", fmt.Sprintf("file:%s?cache=shared&mode=ro", *sqlitePath))
	if err != nil {
		log.Fatalf("open sqlite: %v", err)
	}
	defer srcDB.Close()

	// Open MySQL
	dstDB, err := sql.Open("mysql", *mysqlDSN)
	if err != nil {
		log.Fatalf("open mysql: %v", err)
	}
	defer dstDB.Close()
	if err := dstDB.Ping(); err != nil {
		log.Fatalf("mysql ping: %v", err)
	}

	tables := []string{
		"users", "sessions", "recordings", "logs", "alerts", "updates",
		"sync_status", "user_stats",
		"devices", "video_sources", "hardware_items", "sync_tasks", "perf_reports",
		"flight_missions", "mission_logs",
		"gps_devices", "gps_history", "gps_fence_alerts",
		"drones", "battery_records", "battery_alerts",
		"flight_plans", "no_fly_zones", "cot_chains",
		"notifications", "ai_chat_messages",
	}

	for _, table := range tables {
		n, err := copyTable(srcDB, dstDB, table)
		if err != nil {
			log.Printf("[WARN] %s: %v", table, err)
			continue
		}
		log.Printf("[OK]   %-25s  %d rows", table, n)
	}

	log.Println("Migration complete.")
}

func copyTable(src, dst *sql.DB, table string) (int, error) {
	rows, err := src.Query("SELECT * FROM " + table)
	if err != nil {
		return 0, fmt.Errorf("select: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return 0, fmt.Errorf("columns: %w", err)
	}
	if len(cols) == 0 {
		return 0, nil
	}

	placeholders := make([]string, len(cols))
	for i := range placeholders {
		placeholders[i] = "?"
	}
	insertSQL := fmt.Sprintf("REPLACE INTO %s (%s) VALUES (%s)",
		table,
		strings.Join(cols, ", "),
		strings.Join(placeholders, ", "))

	tx, err := dst.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(insertSQL)
	if err != nil {
		return 0, fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	count := 0
	for rows.Next() {
		vals := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			log.Printf("[WARN] %s row %d scan: %v", table, count+1, err)
			continue
		}
		// Convert []byte to string for MySQL compatibility
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				vals[i] = string(b)
			}
		}
		if _, err := stmt.Exec(vals...); err != nil {
			log.Printf("[WARN] %s row %d insert: %v", table, count+1, err)
			continue
		}
		count++
	}

	if err := tx.Commit(); err != nil {
		return count, fmt.Errorf("commit: %w", err)
	}
	return count, nil
}
