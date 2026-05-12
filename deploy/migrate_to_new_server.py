"""
Migrate data from local SQLite (app.db) to the NEW server's MySQL.
Run on the SERVER after MySQL is initialized:
    pip3 install pymysql
    python3 migrate_to_new_server.py
"""
import sqlite3
import pymysql
import subprocess
import sys
import os

# ── Configuration ──────────────────────────────────────────────────────────────
SQLITE_PATH = "/opt/smartcontrol/app.db"
MYSQL_HOST = "127.0.0.1"
MYSQL_PORT = 3306
MYSQL_USER = "smartcontrol"
MYSQL_PASS = "SC_Db_2024!Secure"
MYSQL_DB   = "smartcontrol"

# Tables to migrate in order (respects foreign-key dependencies).
ORDERED_TABLES = [
    "users",
    "sessions",
    "user_stats",
    "recordings",
    "logs",
    "alerts",
    "updates",
    "sync_status",
    "devices",
    "video_sources",
    "hardware_items",
    "sync_tasks",
    "perf_reports",
    "flight_missions",
    "mission_logs",
    "gps_devices",
    "gps_history",
    "gps_fence_alerts",
    "drones",
    "battery_records",
    "battery_alerts",
    "flight_plans",
    "no_fly_zones",
    "cot_chains",
    "notifications",
    "ai_chat_messages",
    "sim_batches",
    "sim_instances",
    "sim_events",
    "sim_telemetry_log",
    "rl_training_log",
    "knowledge_docs",
    "data_store",
]


def get_sqlite_columns(cursor, table):
    """Return list of column names for a SQLite table."""
    cursor.execute(f"PRAGMA table_info([{table}])")
    return [row[1] for row in cursor.fetchall()]


def get_mysql_columns(cursor, table):
    """Return list of column names for a MySQL table."""
    cursor.execute(f"SHOW COLUMNS FROM `{table}`")
    return [row[0] for row in cursor.fetchall()]


def migrate_table(sqlite_cur, mysql_conn, mysql_cur, table):
    """Migrate one table from SQLite to MySQL."""
    sqlite_cols = get_sqlite_columns(sqlite_cur, table)
    try:
        mysql_cols = get_mysql_columns(mysql_cur, table)
    except Exception as e:
        print(f"  ⚠ Table `{table}` does not exist in MySQL, skipping: {e}")
        return 0

    common_cols = [c for c in sqlite_cols if c in mysql_cols]
    if not common_cols:
        print(f"  ⚠ No common columns for `{table}`, skipping")
        return 0

    col_list = ", ".join(f"[{c}]" for c in common_cols)
    sqlite_cur.execute(f"SELECT {col_list} FROM [{table}]")
    rows = sqlite_cur.fetchall()
    if not rows:
        print(f"  `{table}`: 0 rows (empty)")
        return 0

    mysql_col_list = ", ".join(f"`{c}`" for c in common_cols)
    placeholders = ", ".join(["%s"] * len(common_cols))

    if table in ("cot_chains", "sync_status", "user_stats", "data_store"):
        sql = f"REPLACE INTO `{table}` ({mysql_col_list}) VALUES ({placeholders})"
    else:
        sql = f"INSERT IGNORE INTO `{table}` ({mysql_col_list}) VALUES ({placeholders})"

    batch_size = 100
    inserted = 0
    for i in range(0, len(rows), batch_size):
        batch = rows[i:i+batch_size]
        cleaned = []
        for row in batch:
            cleaned_row = []
            for val in row:
                if isinstance(val, bytes):
                    try:
                        val = val.decode("utf-8")
                    except Exception:
                        val = val.hex()
                cleaned_row.append(val)
            cleaned.append(tuple(cleaned_row))
        try:
            mysql_cur.executemany(sql, cleaned)
            inserted += mysql_cur.rowcount
        except Exception as e:
            print(f"  ✗ Error inserting batch into `{table}`: {e}")
            for row in cleaned:
                try:
                    mysql_cur.execute(sql, row)
                    inserted += mysql_cur.rowcount
                except Exception as e2:
                    print(f"    ✗ Skipping row: {e2}")

    mysql_conn.commit()
    print(f"  ✓ `{table}`: {inserted}/{len(rows)} rows migrated (common cols: {len(common_cols)}/{len(sqlite_cols)})")
    return inserted


def reset_auto_increment(mysql_cur, mysql_conn, table):
    """Reset AUTO_INCREMENT to max(id)+1."""
    try:
        mysql_cur.execute(f"SELECT MAX(id) FROM `{table}`")
        max_id = mysql_cur.fetchone()[0]
        if max_id is not None:
            mysql_cur.execute(f"ALTER TABLE `{table}` AUTO_INCREMENT = {max_id + 1}")
            mysql_conn.commit()
    except Exception:
        pass


def ensure_tables_exist():
    """
    Start the smartcontrol binary briefly to auto-create MySQL tables via Migrate(),
    then stop it. This ensures all tables exist before data migration.
    """
    print("启动 smartcontrol 初始化数据库表结构...")
    env = os.environ.copy()
    env["SC_DB_DRIVER"] = "mysql"
    env["SC_MYSQL_DSN"] = f"{MYSQL_USER}:{MYSQL_PASS}@tcp({MYSQL_HOST}:{MYSQL_PORT})/{MYSQL_DB}?charset=utf8mb4&parseTime=true"
    env["SC_LISTEN_ADDR"] = ":19999"  # temp port to avoid conflict

    try:
        proc = subprocess.Popen(
            ["/opt/smartcontrol/smartcontrol"],
            env=env,
            cwd="/opt/smartcontrol",
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        import time
        time.sleep(5)  # wait for Migrate() to complete
        proc.terminate()
        proc.wait(timeout=10)
        print("  ✓ 表结构初始化完成")
    except Exception as e:
        print(f"  ⚠ 初始化失败: {e}")
        print("  请确保 /opt/smartcontrol/smartcontrol 可执行且 MySQL 已启动")
        sys.exit(1)


def main():
    if not os.path.exists(SQLITE_PATH):
        print(f"错误: SQLite 数据库不存在: {SQLITE_PATH}")
        print("请确保已将 app.db 上传到服务器")
        sys.exit(1)

    # Step 0: Run smartcontrol briefly to create all tables
    ensure_tables_exist()

    # Step 1: Connect to SQLite
    print("\nConnecting to SQLite...")
    sqlite_conn = sqlite3.connect(SQLITE_PATH)
    sqlite_cur = sqlite_conn.cursor()

    # Step 2: Connect to MySQL
    print("Connecting to MySQL...")
    mysql_conn = pymysql.connect(
        host=MYSQL_HOST,
        port=MYSQL_PORT,
        user=MYSQL_USER,
        password=MYSQL_PASS,
        database=MYSQL_DB,
        charset="utf8mb4",
    )
    mysql_cur = mysql_conn.cursor()

    # Disable FK checks
    mysql_cur.execute("SET FOREIGN_KEY_CHECKS = 0")
    mysql_conn.commit()

    print("\n=== Starting data migration ===\n")
    total = 0
    for table in ORDERED_TABLES:
        sqlite_cur.execute("SELECT name FROM sqlite_master WHERE type='table' AND name=?", (table,))
        if not sqlite_cur.fetchone():
            print(f"  ⚠ Table `{table}` not in SQLite, skipping")
            continue
        count = migrate_table(sqlite_cur, mysql_conn, mysql_cur, table)
        total += count

    # Reset auto-increment
    print("\n=== Resetting AUTO_INCREMENT values ===\n")
    skip_tables = {"cot_chains", "sync_status", "user_stats", "data_store"}
    for table in ORDERED_TABLES:
        if table not in skip_tables:
            reset_auto_increment(mysql_cur, mysql_conn, table)

    # Re-enable FK checks
    mysql_cur.execute("SET FOREIGN_KEY_CHECKS = 1")
    mysql_conn.commit()

    # Verify
    print("\n=== Verification: Row counts ===\n")
    for table in ORDERED_TABLES:
        try:
            mysql_cur.execute(f"SELECT COUNT(*) FROM `{table}`")
            mc = mysql_cur.fetchone()[0]
            sqlite_cur.execute(f"SELECT COUNT(*) FROM [{table}]")
            sc = sqlite_cur.fetchone()[0]
            status = "✓" if mc >= sc else "✗ MISMATCH"
            print(f"  {status} `{table}`: MySQL={mc}, SQLite={sc}")
        except Exception as e:
            print(f"  ✗ `{table}`: {e}")

    print(f"\n=== Migration complete: {total} total rows migrated ===")

    sqlite_conn.close()
    mysql_conn.close()


if __name__ == "__main__":
    main()
