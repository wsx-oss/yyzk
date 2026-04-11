package handlers

import (
	"context"
	"fmt"
	"log"
	"time"

	"smartcontrol/internal/db"
	"smartcontrol/internal/taskpool"
)

// StartPatrolInspection registers all patrol tasks as periodic jobs in the
// given worker pool. Each patrol runs on its own schedule via the pool's
// SchedulePeriodic mechanism instead of raw goroutines.
func StartPatrolInspection(database *db.DB, pool *taskpool.Pool) {
	pool.SchedulePeriodic("patrol:battery", "patrol", 30*time.Second, taskpool.PriorityNormal, patrolBatteryTask(database))
	pool.SchedulePeriodic("patrol:drones", "patrol", 30*time.Second, taskpool.PriorityNormal, patrolDronesTask(database))
	pool.SchedulePeriodic("patrol:alerts", "patrol", 60*time.Second, taskpool.PriorityNormal, patrolAlertsTask(database))
	pool.SchedulePeriodic("patrol:hardware", "patrol", 60*time.Second, taskpool.PriorityLow, patrolHardwareTask(database))
	pool.SchedulePeriodic("patrol:logs", "patrol", 120*time.Second, taskpool.PriorityLow, patrolLogsTask(database))
	pool.SchedulePeriodic("patrol:missions", "patrol", 30*time.Second, taskpool.PriorityNormal, patrolMissionsTask(database))
	log.Println("[Patrol] AI inspection tasks registered in pool")
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

// patrolBatteryTask returns a context-aware function that checks for low battery
// and high temperature conditions. State is captured in the closure.
func patrolBatteryTask(database *db.DB) func(ctx context.Context) error {
	notified := map[int]time.Time{}
	cooldown := 5 * time.Minute
	return func(ctx context.Context) error {
		rows, err := database.Query(`
			SELECT br.device_id, br.device_name, br.level, br.temperature
			FROM battery_records br
			INNER JOIN (SELECT device_id, MAX(created_at) as mt FROM battery_records GROUP BY device_id) latest
			ON br.device_id = latest.device_id AND br.created_at = latest.mt
			INNER JOIN gps_devices g ON g.id = br.device_id
			LEFT JOIN drones d ON d.linked_gps_device_id = g.id
			WHERE (br.level < 20 OR br.temperature > 50)
			  AND g.status = '在线'
			  AND (d.id IS NULL OR d.status = 'online')
		`)
		if err != nil {
			return err
		}
		defer rows.Close()
		now := time.Now()
		for rows.Next() {
			if ctx.Err() != nil {
				return ctx.Err()
			}
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
				insertNotification(database, "battery",
					fmt.Sprintf("⚡ %s 电量低", deviceName),
					fmt.Sprintf("设备 %s 当前电量仅 %d%%，请及时充电或更换电池", deviceName, level),
					"AI巡检", "/app/modules/battery.html")
			}
			if temp > 50 {
				insertNotification(database, "battery",
					fmt.Sprintf("🌡️ %s 温度异常", deviceName),
					fmt.Sprintf("设备 %s 电池温度 %.1f°C，超过安全阈值，请检查", deviceName, temp),
					"AI巡检", "/app/modules/battery.html")
			}
		}
		return nil
	}
}

// patrolDronesTask returns a function that checks for drones going offline.
// A 5-minute per-drone cooldown prevents repeated notifications when a drone
// flickers between online and offline states.
func patrolDronesTask(database *db.DB) func(ctx context.Context) error {
	prevOnline := map[int]bool{}
	notified := map[int]time.Time{}
	cooldown := 5 * time.Minute
	// Build initial state
	rows, err := database.Query("SELECT id FROM drones WHERE status='online'")
	if err == nil {
		for rows.Next() {
			var id int
			if rows.Scan(&id) == nil {
				prevOnline[id] = true
			}
		}
		rows.Close()
	}
	return func(ctx context.Context) error {
		currentOnline := map[int]bool{}
		rows, err := database.Query("SELECT id, name FROM drones WHERE status='online'")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id int
			var name string
			if rows.Scan(&id, &name) == nil {
				currentOnline[id] = true
			}
		}
		now := time.Now()
		for id := range prevOnline {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if !currentOnline[id] {
				if t, ok := notified[id]; ok && now.Sub(t) < cooldown {
					continue
				}
				var name string
				database.QueryRow("SELECT name FROM drones WHERE id=?", id).Scan(&name)
				if name == "" {
					name = fmt.Sprintf("ID:%d", id)
				}
				notified[id] = now
				insertNotification(database, "drone",
					fmt.Sprintf("🚁 无人机 %s 离线", name),
					fmt.Sprintf("无人机 %s 已断开连接，请检查设备状态和网络连接", name),
					"AI巡检", "/app/modules/drones.html")
			}
		}
		prevOnline = currentOnline
		return nil
	}
}

// patrolAlertsTask returns a function that checks for new critical alerts.
// A per-category cooldown prevents notification spam when the same alert
// category fires repeatedly in quick succession.
func patrolAlertsTask(database *db.DB) func(ctx context.Context) error {
	var lastCheckID int
	database.QueryRow("SELECT COALESCE(MAX(id),0) FROM alerts").Scan(&lastCheckID)
	catNotified := map[string]time.Time{}
	cooldown := 5 * time.Minute
	return func(ctx context.Context) error {
		rows, err := database.Query(
			"SELECT id, category, severity, message FROM alerts WHERE id > ? AND severity = 'critical' AND acknowledged = 0",
			lastCheckID,
		)
		if err != nil {
			return err
		}
		defer rows.Close()
		now := time.Now()
		var maxID int
		for rows.Next() {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			var id int
			var cat, sev, msg string
			if rows.Scan(&id, &cat, &sev, &msg) == nil {
				if id > maxID {
					maxID = id
				}
				if t, ok := catNotified[cat]; ok && now.Sub(t) < cooldown {
					continue
				}
				catNotified[cat] = now
				insertNotification(database, "alert",
					fmt.Sprintf("🔴 严重告警: %s", cat),
					msg,
					"AI巡检", "/app/modules/alerts.html")
			}
		}
		if maxID > lastCheckID {
			lastCheckID = maxID
		}
		return nil
	}
}

// patrolHardwareTask returns a function that checks for hardware issues.
func patrolHardwareTask(database *db.DB) func(ctx context.Context) error {
	notified := map[int]time.Time{}
	cooldown := 10 * time.Minute
	return func(ctx context.Context) error {
		now := time.Now()
		rows, err := database.Query(`
			SELECT id, name, type, status, cpu_usage, mem_usage, temperature
			FROM hardware_items
			WHERE status='离线' OR cpu_usage > 90 OR mem_usage > 90 OR temperature > 80
		`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			if ctx.Err() != nil {
				return ctx.Err()
			}
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
				insertNotification(database, "hardware",
					fmt.Sprintf("🖥️ 硬件 %s 离线", name),
					fmt.Sprintf("硬件设备 %s (%s) 已离线，请检查", name, typ),
					"AI巡检", "/app/modules/hardware.html")
			} else if cpu > 90 || mem > 90 || temp > 80 {
				notified[id] = now
				insertNotification(database, "hardware",
					fmt.Sprintf("⚠️ %s 资源告警", name),
					fmt.Sprintf("%s: CPU %.1f%%, 内存 %.1f%%, 温度 %.1f°C", name, cpu, mem, temp),
					"AI巡检", "/app/modules/hardware.html")
			}
		}
		return nil
	}
}

// patrolLogsTask returns a function that checks for error-level logs.
// A 10-minute cooldown prevents rapid-fire log error notifications.
func patrolLogsTask(database *db.DB) func(ctx context.Context) error {
	var lastCheckID int
	database.QueryRow("SELECT COALESCE(MAX(id),0) FROM logs").Scan(&lastCheckID)
	var lastNotified time.Time
	cooldown := 10 * time.Minute
	return func(ctx context.Context) error {
		var errorCnt int
		database.QueryRow("SELECT COUNT(*) FROM logs WHERE id > ? AND (level='error' OR level='fatal')", lastCheckID).Scan(&errorCnt)
		now := time.Now()
		if errorCnt > 0 && now.Sub(lastNotified) >= cooldown {
			lastNotified = now
			insertNotification(database, "log",
				fmt.Sprintf("📋 发现 %d 条错误日志", errorCnt),
				fmt.Sprintf("后台新增 %d 条错误级别日志，请及时查看", errorCnt),
				"AI巡检", "/app/modules/logs.html")
		}
		var maxID int
		database.QueryRow("SELECT COALESCE(MAX(id),0) FROM logs").Scan(&maxID)
		if maxID > lastCheckID {
			lastCheckID = maxID
		}
		return nil
	}
}

// patrolMissionsTask returns a function that checks for completed missions.
func patrolMissionsTask(database *db.DB) func(ctx context.Context) error {
	notifiedComplete := map[int]bool{}
	return func(ctx context.Context) error {
		rows, err := database.Query("SELECT id, name FROM flight_missions WHERE status='已完成'")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			var id int
			var name string
			if rows.Scan(&id, &name) == nil && !notifiedComplete[id] {
				notifiedComplete[id] = true
				insertNotification(database, "mission",
					fmt.Sprintf("✅ 任务 %s 已完成", name),
					fmt.Sprintf("飞行任务 \"%s\" 已完成", name),
					"AI巡检", "/app/modules/flight.html")
			}
		}
		return nil
	}
}
