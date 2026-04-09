package db

import (
	"database/sql"
	"fmt"
	"log"
)

func migrateMySQL(db *sql.DB) error {
	tables := []string{
		`CREATE TABLE IF NOT EXISTS recordings (
			id INT AUTO_INCREMENT PRIMARY KEY,
			filename TEXT NOT NULL,
			mime TEXT,
			duration DOUBLE,
			size INT,
			user_id VARCHAR(500) DEFAULT '',
			interact_type VARCHAR(500) DEFAULT '',
			content VARCHAR(500) DEFAULT '',
			clarity VARCHAR(500) DEFAULT '',
			result VARCHAR(500) DEFAULT '',
			score INT DEFAULT 4,
			tags VARCHAR(500) DEFAULT '',
			remark VARCHAR(500) DEFAULT '',
			interact_time VARCHAR(500) DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS logs (
			id INT AUTO_INCREMENT PRIMARY KEY,
			level TEXT,
			message TEXT,
			meta TEXT,
			op_type VARCHAR(500) DEFAULT '',
			operator VARCHAR(500) DEFAULT '',
			op_time VARCHAR(500) DEFAULT '',
			op_result VARCHAR(500) DEFAULT '',
			device_name VARCHAR(500) DEFAULT '',
			log_status VARCHAR(500) DEFAULT '启用',
			detail VARCHAR(500) DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS alerts (
			id INT AUTO_INCREMENT PRIMARY KEY,
			category TEXT,
			severity TEXT,
			message TEXT,
			acknowledged INT DEFAULT 0,
			alert_time VARCHAR(500) DEFAULT '',
			priority VARCHAR(500) DEFAULT '中',
			device VARCHAR(500) DEFAULT '',
			description VARCHAR(500) DEFAULT '',
			status VARCHAR(500) DEFAULT '未解决',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS updates (
			id INT AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(500) DEFAULT '',
			version TEXT,
			description TEXT,
			status TEXT,
			type VARCHAR(500) DEFAULT '功能更新',
			size VARCHAR(500) DEFAULT '',
			auto_update INT DEFAULT 0,
			force_update INT DEFAULT 0,
			publish_date VARCHAR(500) DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS sync_status (
			id INT PRIMARY KEY CHECK (id = 1),
			status TEXT,
			message TEXT,
			last_synced_at DATETIME,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS users (
			id INT AUTO_INCREMENT PRIMARY KEY,
			username VARCHAR(255) UNIQUE NOT NULL,
			password_hash TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS sessions (
			id INT AUTO_INCREMENT PRIMARY KEY,
			user_id INT NOT NULL,
			token VARCHAR(255) UNIQUE NOT NULL,
			expires_at DATETIME NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS devices (
			id INT AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			ip VARCHAR(255) NOT NULL,
			protocol VARCHAR(100) NOT NULL,
			port INT DEFAULT 0,
			username VARCHAR(500) DEFAULT '',
			password VARCHAR(500) DEFAULT '',
			auto_connect INT DEFAULT 0,
			log_enabled INT DEFAULT 0,
			description VARCHAR(500) DEFAULT '',
			status VARCHAR(50) NOT NULL DEFAULT 'offline',
			drone_id INT DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			last_connected_at DATETIME
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS video_sources (
			id INT AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			url TEXT NOT NULL,
			region VARCHAR(255) DEFAULT '',
			clarity VARCHAR(500) DEFAULT '',
			status VARCHAR(500) DEFAULT '正常',
			recording INT DEFAULT 0,
			start_time VARCHAR(500) DEFAULT '',
			end_time VARCHAR(500) DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS hardware_items (
			id INT AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			type VARCHAR(255) NOT NULL DEFAULT '服务器',
			ip VARCHAR(255) NOT NULL,
			status VARCHAR(50) NOT NULL DEFAULT '在线',
			description VARCHAR(500) DEFAULT '',
			temperature DOUBLE DEFAULT 0,
			cpu_usage DOUBLE DEFAULT 0,
			mem_usage DOUBLE DEFAULT 0,
			network_bandwidth VARCHAR(500) DEFAULT '0Mbps',
			detected_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS sync_tasks (
			id INT AUTO_INCREMENT PRIMARY KEY,
			source VARCHAR(500) NOT NULL DEFAULT '',
			target VARCHAR(500) NOT NULL DEFAULT '',
			frequency VARCHAR(500) NOT NULL DEFAULT '5分钟',
			mode VARCHAR(100) NOT NULL DEFAULT '全量同步',
			start_time VARCHAR(500) DEFAULT '',
			end_time VARCHAR(500) DEFAULT '',
			status VARCHAR(100) NOT NULL DEFAULT '待启动',
			sync_status_enabled INT DEFAULT 0,
			log_enabled INT DEFAULT 0,
			progress INT DEFAULT 0,
			synced_data INT DEFAULT 0,
			total_data INT DEFAULT 1000,
			success_rate DOUBLE DEFAULT 0,
			avg_duration DOUBLE DEFAULT 0,
			last_synced_at DATETIME,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS perf_reports (
			id INT AUTO_INCREMENT PRIMARY KEY,
			analysis_time VARCHAR(255) NOT NULL DEFAULT '',
			module_name VARCHAR(255) NOT NULL DEFAULT '',
			user_id VARCHAR(255) NOT NULL DEFAULT '',
			response_time VARCHAR(500) NOT NULL DEFAULT '',
			throughput VARCHAR(500) NOT NULL DEFAULT '',
			error_rate VARCHAR(500) NOT NULL DEFAULT '',
			description VARCHAR(500) DEFAULT '',
			analysis_type VARCHAR(500) DEFAULT '整体性能',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS user_stats (
			user_id INT PRIMARY KEY,
			total_connections INT NOT NULL DEFAULT 0,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS flight_missions (
			id INT AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			route VARCHAR(500) NOT NULL DEFAULT '',
			target VARCHAR(500) NOT NULL DEFAULT '',
			estimated_duration VARCHAR(500) DEFAULT '',
			status VARCHAR(100) NOT NULL DEFAULT '待起飞',
			current_phase VARCHAR(100) NOT NULL DEFAULT '待命',
			progress INT DEFAULT 0,
			start_time VARCHAR(500) DEFAULT '',
			end_time VARCHAR(500) DEFAULT '',
			description VARCHAR(500) DEFAULT '',
			device_id INT DEFAULT 0,
			waypoints_json TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS mission_logs (
			id INT AUTO_INCREMENT PRIMARY KEY,
			mission_id INT NOT NULL,
			phase VARCHAR(500) NOT NULL DEFAULT '',
			message VARCHAR(500) DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (mission_id) REFERENCES flight_missions(id) ON DELETE CASCADE
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS gps_devices (
			id INT AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			device_type VARCHAR(500) NOT NULL DEFAULT '无人机',
			latitude DOUBLE DEFAULT 0,
			longitude DOUBLE DEFAULT 0,
			altitude DOUBLE DEFAULT 0,
			speed DOUBLE DEFAULT 0,
			heading DOUBLE DEFAULT 0,
			accuracy DOUBLE DEFAULT 0,
			status VARCHAR(50) NOT NULL DEFAULT '在线',
			fence_enabled INT DEFAULT 0,
			fence_lat DOUBLE DEFAULT 0,
			fence_lng DOUBLE DEFAULT 0,
			fence_radius DOUBLE DEFAULT 0,
			last_update DATETIME DEFAULT CURRENT_TIMESTAMP,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			agent_id VARCHAR(500) DEFAULT '',
			drone_id INT DEFAULT 0,
			map_visible INT DEFAULT 0
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS gps_history (
			id INT AUTO_INCREMENT PRIMARY KEY,
			device_id INT NOT NULL,
			latitude DOUBLE DEFAULT 0,
			longitude DOUBLE DEFAULT 0,
			altitude DOUBLE DEFAULT 0,
			speed DOUBLE DEFAULT 0,
			heading DOUBLE DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (device_id) REFERENCES gps_devices(id) ON DELETE CASCADE
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS gps_fence_alerts (
			id INT AUTO_INCREMENT PRIMARY KEY,
			device_id INT NOT NULL,
			device_name VARCHAR(500) DEFAULT '',
			latitude DOUBLE DEFAULT 0,
			longitude DOUBLE DEFAULT 0,
			fence_lat DOUBLE DEFAULT 0,
			fence_lng DOUBLE DEFAULT 0,
			fence_radius DOUBLE DEFAULT 0,
			distance DOUBLE DEFAULT 0,
			message VARCHAR(500) DEFAULT '',
			acknowledged INT DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (device_id) REFERENCES gps_devices(id) ON DELETE CASCADE
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS drones (
			id INT AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			serial_number VARCHAR(500) DEFAULT '',
			model VARCHAR(500) DEFAULT '',
			description VARCHAR(500) DEFAULT '',
			ip VARCHAR(500) DEFAULT '',
			ssh_port INT DEFAULT 22,
			vnc_port INT DEFAULT 5900,
			rdp_port INT DEFAULT 3389,
			protocol VARCHAR(500) DEFAULT 'SSH',
			username VARCHAR(500) DEFAULT '',
			password VARCHAR(500) DEFAULT '',
			agent_id VARCHAR(255) DEFAULT '',
			initial_lat DOUBLE DEFAULT 0,
			initial_lng DOUBLE DEFAULT 0,
			initial_alt DOUBLE DEFAULT 0,
			fence_enabled INT DEFAULT 0,
			fence_lat DOUBLE DEFAULT 0,
			fence_lng DOUBLE DEFAULT 0,
			fence_radius DOUBLE DEFAULT 500,
			auto_connect INT DEFAULT 0,
			log_enabled INT DEFAULT 0,
			status VARCHAR(50) NOT NULL DEFAULT 'offline',
			linked_device_id INT DEFAULT 0,
			linked_gps_device_id INT DEFAULT 0,
			video_url VARCHAR(500) DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS battery_records (
			id INT AUTO_INCREMENT PRIMARY KEY,
			device_id INT NOT NULL,
			device_name VARCHAR(500) NOT NULL DEFAULT '',
			voltage DOUBLE DEFAULT 0,
			current_val DOUBLE DEFAULT 0,
			level INT DEFAULT 100,
			temperature DOUBLE DEFAULT 25,
			health INT DEFAULT 100,
			status VARCHAR(500) DEFAULT '正常',
			charge_cycles INT DEFAULT 0,
			remaining_time VARCHAR(500) DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS battery_alerts (
			id INT AUTO_INCREMENT PRIMARY KEY,
			device_id INT NOT NULL,
			device_name VARCHAR(500) NOT NULL DEFAULT '',
			level INT DEFAULT 0,
			voltage DOUBLE DEFAULT 0,
			temperature DOUBLE DEFAULT 0,
			alert_type VARCHAR(500) DEFAULT '',
			message VARCHAR(500) DEFAULT '',
			acknowledged INT DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS flight_plans (
			id INT AUTO_INCREMENT PRIMARY KEY,
			drone_id INT DEFAULT 0,
			request_json TEXT,
			result_json TEXT,
			source VARCHAR(500) DEFAULT 'llm',
			status VARCHAR(100) DEFAULT 'draft',
			mission_id INT DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS no_fly_zones (
			id INT AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(255) NOT NULL UNIQUE,
			zone_type VARCHAR(255) NOT NULL DEFAULT '禁飞区',
			shape_type VARCHAR(100) NOT NULL DEFAULT 'polygon',
			shape_json TEXT NOT NULL,
			altitude_limit INT DEFAULT -1,
			altitude_enabled INT DEFAULT 0,
			area_m2 DOUBLE DEFAULT 0,
			address VARCHAR(500) DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS cot_chains (
			id VARCHAR(255) PRIMARY KEY,
			task_type VARCHAR(255) NOT NULL,
			task_id VARCHAR(255) NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			steps TEXT NOT NULL,
			final_decision VARCHAR(500) DEFAULT '',
			overall_confidence DOUBLE DEFAULT 0.0,
			metadata TEXT
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS notifications (
			id INT AUTO_INCREMENT PRIMARY KEY,
			type VARCHAR(100) NOT NULL DEFAULT 'system',
			title VARCHAR(500) NOT NULL DEFAULT '',
			message VARCHAR(500) NOT NULL DEFAULT '',
			source VARCHAR(500) DEFAULT '',
			link VARCHAR(500) DEFAULT '',
			is_read INT DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS ai_chat_messages (
			id INT AUTO_INCREMENT PRIMARY KEY,
			session_id VARCHAR(255) NOT NULL DEFAULT 'default',
			role VARCHAR(100) NOT NULL DEFAULT 'user',
			content TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,

		`CREATE TABLE IF NOT EXISTS backup_records (
			id INT AUTO_INCREMENT PRIMARY KEY,
			backup_type VARCHAR(50) NOT NULL DEFAULT 'manual',
			file_path VARCHAR(500) NOT NULL DEFAULT '',
			file_size BIGINT DEFAULT 0,
			table_count INT DEFAULT 0,
			row_count INT DEFAULT 0,
			status VARCHAR(50) NOT NULL DEFAULT 'running',
			operator VARCHAR(255) DEFAULT 'system',
			remark VARCHAR(500) DEFAULT '',
			duration_ms INT DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			finished_at DATETIME
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	}

	for _, s := range tables {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("create table: %w\nSQL: %s", err, s[:min(len(s), 120)])
		}
	}

	// Seed data (INSERT IGNORE to avoid duplicates)
	db.Exec(`INSERT IGNORE INTO sync_status(id, status, message) VALUES(1, 'idle', '')`)

	// Indexes — ignore "Duplicate key name" errors for idempotency
	indexes := []string{
		`CREATE INDEX idx_sessions_token ON sessions(token)`,
		`CREATE INDEX idx_sessions_expires ON sessions(expires_at)`,
		`CREATE INDEX idx_users_username ON users(username)`,
		`CREATE INDEX idx_recordings_created ON recordings(created_at)`,
		`CREATE INDEX idx_alerts_created ON alerts(created_at)`,
		`CREATE INDEX idx_logs_created ON logs(created_at)`,
		`CREATE INDEX idx_devices_name ON devices(name)`,
		`CREATE INDEX idx_devices_protocol ON devices(protocol)`,
		`CREATE INDEX idx_devices_created ON devices(created_at)`,
		`CREATE UNIQUE INDEX uq_devices_name_ip_protocol ON devices(name, ip, protocol)`,
		`CREATE INDEX idx_video_sources_name ON video_sources(name)`,
		`CREATE INDEX idx_video_sources_region ON video_sources(region)`,
		`CREATE INDEX idx_hardware_items_name ON hardware_items(name)`,
		`CREATE INDEX idx_hardware_items_type ON hardware_items(type)`,
		`CREATE INDEX idx_hardware_items_status ON hardware_items(status)`,
		`CREATE INDEX idx_hardware_items_ip ON hardware_items(ip)`,
		`CREATE INDEX idx_hardware_items_detected ON hardware_items(detected_at)`,
		`CREATE INDEX idx_sync_tasks_status ON sync_tasks(status)`,
		`CREATE INDEX idx_sync_tasks_mode ON sync_tasks(mode)`,
		`CREATE INDEX idx_sync_tasks_created ON sync_tasks(created_at)`,
		`CREATE INDEX idx_perf_reports_module ON perf_reports(module_name)`,
		`CREATE INDEX idx_perf_reports_user ON perf_reports(user_id)`,
		`CREATE INDEX idx_perf_reports_time ON perf_reports(analysis_time)`,
		`CREATE INDEX idx_flight_missions_name ON flight_missions(name)`,
		`CREATE INDEX idx_flight_missions_status ON flight_missions(status)`,
		`CREATE INDEX idx_flight_missions_created ON flight_missions(created_at)`,
		`CREATE INDEX idx_mission_logs_mission ON mission_logs(mission_id)`,
		`CREATE INDEX idx_gps_devices_name ON gps_devices(name)`,
		`CREATE INDEX idx_gps_devices_status ON gps_devices(status)`,
		`CREATE INDEX idx_gps_history_device ON gps_history(device_id)`,
		`CREATE INDEX idx_gps_history_created ON gps_history(created_at)`,
		`CREATE INDEX idx_gps_fence_alerts_device ON gps_fence_alerts(device_id)`,
		`CREATE INDEX idx_drones_name ON drones(name)`,
		`CREATE INDEX idx_drones_agent_id ON drones(agent_id)`,
		`CREATE INDEX idx_drones_status ON drones(status)`,
		`CREATE INDEX idx_battery_records_device ON battery_records(device_id)`,
		`CREATE INDEX idx_battery_records_created ON battery_records(created_at)`,
		`CREATE INDEX idx_battery_alerts_device ON battery_alerts(device_id)`,
		`CREATE INDEX idx_flight_plans_status ON flight_plans(status)`,
		`CREATE INDEX idx_flight_plans_drone ON flight_plans(drone_id)`,
		`CREATE INDEX idx_flight_plans_created ON flight_plans(created_at)`,
		`CREATE INDEX idx_no_fly_zones_name ON no_fly_zones(name)`,
		`CREATE INDEX idx_no_fly_zones_type ON no_fly_zones(zone_type)`,
		`CREATE INDEX idx_cot_chains_task_type ON cot_chains(task_type)`,
		`CREATE INDEX idx_cot_chains_task_id ON cot_chains(task_id)`,
		`CREATE INDEX idx_cot_chains_created ON cot_chains(created_at)`,
		`CREATE INDEX idx_notifications_type ON notifications(type)`,
		`CREATE INDEX idx_notifications_is_read ON notifications(is_read)`,
		`CREATE INDEX idx_notifications_created ON notifications(created_at)`,
		`CREATE INDEX idx_ai_chat_session ON ai_chat_messages(session_id)`,
		`CREATE INDEX idx_ai_chat_created ON ai_chat_messages(created_at)`,
		`CREATE INDEX idx_backup_records_type ON backup_records(backup_type)`,
		`CREATE INDEX idx_backup_records_status ON backup_records(status)`,
		`CREATE INDEX idx_backup_records_created ON backup_records(created_at)`,
	}
	for _, s := range indexes {
		db.Exec(s) // ignore duplicate index errors
	}

	log.Println("[DB] MySQL migration completed")
	return nil
}
