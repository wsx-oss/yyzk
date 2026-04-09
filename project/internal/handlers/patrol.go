package handlers

import (
	"fmt"
	"log"
	"time"

	"smartcontrol/internal/db"
)

// StartPatrolInspection launches background goroutines that periodically
// check various subsystems and generate notifications when issues are found.
func StartPatrolInspection(db *db.DB) {
	go patrolBattery(db)
	go patrolDrones(db)
	go patrolAlerts(db)
	go patrolHardware(db)
	go patrolLogs(db)
	go patrolMissions(db)
	log.Println("[Patrol] AI inspection goroutines started")
}

// insertNotification is a helper to create a notification record
func insertNotification(db *db.DB, nType, title, message, source, link string) {
	_, err := db.Exec(
		"INSERT INTO notifications(type, title, message, source, link) VALUES(?,?,?,?,?)",
		nType, title, message, source, link,
	)
	if err != nil {
		log.Printf("[Patrol] Failed to insert notification: %v", err)
	}
}

// patrolBattery checks for low battery and high temperature conditions
func patrolBattery(db *db.DB) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	// Track recently notified devices to avoid spam
	notified := map[int]time.Time{}
	cooldown := 5 * time.Minute

	for range ticker.C {
		// Check low battery (< 20%)
		rows, err := db.Query(`
			SELECT br.device_id, br.device_name, br.level, br.temperature
			FROM battery_records br
			INNER JOIN (SELECT device_id, MAX(created_at) as mt FROM battery_records GROUP BY device_id) latest
			ON br.device_id = latest.device_id AND br.created_at = latest.mt
			WHERE br.level < 20 OR br.temperature > 50
		`)
		if err != nil {
			continue
		}
		now := time.Now()
		for rows.Next() {
			var deviceID, level int
			var deviceName string
			var temp float64
			if rows.Scan(&deviceID, &deviceName, &level, &temp) != nil {
				continue
			}
			if t, ok := notified[deviceID]; ok && now.Sub(t) < cooldown {
				continue
			}
			notified[deviceID] = now

			if level < 20 {
				insertNotification(db, "battery",
					fmt.Sprintf("⚡ %s 电量低", deviceName),
					fmt.Sprintf("设备 %s 当前电量仅 %d%%，请及时充电或更换电池", deviceName, level),
					"AI巡检", "/app/modules/battery.html")
			}
			if temp > 50 {
				insertNotification(db, "battery",
					fmt.Sprintf("🌡️ %s 温度异常", deviceName),
					fmt.Sprintf("设备 %s 电池温度 %.1f°C，超过安全阈值，请检查", deviceName, temp),
					"AI巡检", "/app/modules/battery.html")
			}
		}
		rows.Close()
	}
}

// patrolDrones checks for drones that go offline unexpectedly
func patrolDrones(db *db.DB) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	prevOnline := map[int]bool{}

	// Build initial state
	rows, err := db.Query("SELECT id FROM drones WHERE status='online'")
	if err == nil {
		for rows.Next() {
			var id int
			if rows.Scan(&id) == nil {
				prevOnline[id] = true
			}
		}
		rows.Close()
	}

	for range ticker.C {
		currentOnline := map[int]bool{}
		rows, err := db.Query("SELECT id, name FROM drones WHERE status='online'")
		if err != nil {
			continue
		}
		names := map[int]string{}
		for rows.Next() {
			var id int
			var name string
			if rows.Scan(&id, &name) == nil {
				currentOnline[id] = true
				names[id] = name
			}
		}
		rows.Close()

		// Check for newly offline drones
		for id := range prevOnline {
			if !currentOnline[id] {
				var name string
				db.QueryRow("SELECT name FROM drones WHERE id=?", id).Scan(&name)
				if name == "" {
					name = fmt.Sprintf("ID:%d", id)
				}
				insertNotification(db, "drone",
					fmt.Sprintf("🚁 无人机 %s 离线", name),
					fmt.Sprintf("无人机 %s 已断开连接，请检查设备状态和网络连接", name),
					"AI巡检", "/app/modules/drones.html")
			}
		}

		prevOnline = currentOnline
	}
}

// patrolAlerts checks for new critical/unacknowledged alerts
func patrolAlerts(db *db.DB) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	var lastCheckID int
	db.QueryRow("SELECT COALESCE(MAX(id),0) FROM alerts").Scan(&lastCheckID)

	for range ticker.C {
		rows, err := db.Query(
			"SELECT id, category, severity, message FROM alerts WHERE id > ? AND severity = 'critical' AND acknowledged = 0",
			lastCheckID,
		)
		if err != nil {
			continue
		}
		var maxID int
		for rows.Next() {
			var id int
			var cat, sev, msg string
			if rows.Scan(&id, &cat, &sev, &msg) == nil {
				if id > maxID {
					maxID = id
				}
				insertNotification(db, "alert",
					fmt.Sprintf("🔴 严重告警: %s", cat),
					msg,
					"AI巡检", "/app/modules/alerts.html")
			}
		}
		rows.Close()
		if maxID > lastCheckID {
			lastCheckID = maxID
		}
	}
}

// patrolHardware checks for hardware items going offline or high resource usage
func patrolHardware(db *db.DB) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	notified := map[int]time.Time{}
	cooldown := 10 * time.Minute

	for range ticker.C {
		now := time.Now()
		rows, err := db.Query(`
			SELECT id, name, type, status, cpu_usage, mem_usage, temperature
			FROM hardware_items
			WHERE status='离线' OR cpu_usage > 90 OR mem_usage > 90 OR temperature > 80
		`)
		if err != nil {
			continue
		}
		for rows.Next() {
			var id int
			var name, typ, status string
			var cpu, mem, temp float64
			if rows.Scan(&id, &name, &typ, &status, &cpu, &mem, &temp) != nil {
				continue
			}
			if t, ok := notified[id]; ok && now.Sub(t) < cooldown {
				continue
			}

			if status == "离线" {
				notified[id] = now
				insertNotification(db, "hardware",
					fmt.Sprintf("🖥️ 硬件 %s 离线", name),
					fmt.Sprintf("硬件设备 %s (%s) 已离线，请检查", name, typ),
					"AI巡检", "/app/modules/hardware.html")
			} else if cpu > 90 || mem > 90 || temp > 80 {
				notified[id] = now
				insertNotification(db, "hardware",
					fmt.Sprintf("⚠️ %s 资源告警", name),
					fmt.Sprintf("%s: CPU %.1f%%, 内存 %.1f%%, 温度 %.1f°C", name, cpu, mem, temp),
					"AI巡检", "/app/modules/hardware.html")
			}
		}
		rows.Close()
	}
}

// patrolLogs checks for error-level log entries
func patrolLogs(db *db.DB) {
	ticker := time.NewTicker(120 * time.Second)
	defer ticker.Stop()
	var lastCheckID int
	db.QueryRow("SELECT COALESCE(MAX(id),0) FROM logs").Scan(&lastCheckID)

	for range ticker.C {
		var errorCnt int
		db.QueryRow("SELECT COUNT(*) FROM logs WHERE id > ? AND (level='error' OR level='fatal')", lastCheckID).Scan(&errorCnt)

		if errorCnt > 0 {
			insertNotification(db, "log",
				fmt.Sprintf("📋 发现 %d 条错误日志", errorCnt),
				fmt.Sprintf("后台新增 %d 条错误级别日志，请及时查看", errorCnt),
				"AI巡检", "/app/modules/logs.html")
		}

		var maxID int
		db.QueryRow("SELECT COALESCE(MAX(id),0) FROM logs").Scan(&maxID)
		if maxID > lastCheckID {
			lastCheckID = maxID
		}
	}
}

// patrolMissions checks for completed or abnormal flight missions
func patrolMissions(db *db.DB) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	notifiedComplete := map[int]bool{}

	for range ticker.C {
		// Notify on newly completed missions
		rows, err := db.Query("SELECT id, name FROM flight_missions WHERE status='已完成'")
		if err != nil {
			continue
		}
		for rows.Next() {
			var id int
			var name string
			if rows.Scan(&id, &name) == nil && !notifiedComplete[id] {
				notifiedComplete[id] = true
				insertNotification(db, "mission",
					fmt.Sprintf("✅ 任务 %s 已完成", name),
					fmt.Sprintf("飞行任务 \"%s\" 已完成", name),
					"AI巡检", "/app/modules/flight.html")
			}
		}
		rows.Close()
	}
}
